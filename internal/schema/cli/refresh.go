package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/djtouchette/rivet/internal/schema/types"
)

func refreshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Re-read schema and stats from every configured database",
		Long: `For each configured database this connects, reads system catalogs and
stats views, and writes a snapshot to the on-disk cache. All operations are
strictly read-only.

If --db is passed only that database is refreshed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			store, err := openCache()
			if err != nil {
				return err
			}

			var targets []int
			if flagDB != "" {
				for i, d := range cfg.Databases {
					if d.Name == flagDB {
						targets = []int{i}
						break
					}
				}
				if len(targets) == 0 {
					return fmt.Errorf("database %q not found in config", flagDB)
				}
			} else {
				for i := range cfg.Databases {
					targets = append(targets, i)
				}
			}

			if len(targets) == 0 {
				return fmt.Errorf("no databases configured (add schema.databases to .rivet/config.yaml)")
			}

			w := cmd.OutOrStdout()
			for _, i := range targets {
				db := &cfg.Databases[i]
				start := time.Now()
				cat, err := openCatalog(db)
				if err != nil {
					fmt.Fprintf(w, "  %s: connect failed — %v\n", db.Name, err)
					continue
				}
				entry, err := pullSnapshot(cat, db, defaultSlowQueryLimit)
				cat.Close()
				if err != nil {
					fmt.Fprintf(w, "  %s: refresh failed — %v\n", db.Name, err)
					continue
				}
				if err := store.Save(entry); err != nil {
					fmt.Fprintf(w, "  %s: save failed — %v\n", db.Name, err)
					continue
				}
				fmt.Fprintf(w, "  %s: %d tables, %d indexes (%v)\n",
					db.Name, len(entry.Schema.Tables), countIndexes(entry.Schema.Tables), time.Since(start).Round(time.Millisecond))
			}
			return nil
		},
	}
}

func countIndexes(tables []types.Table) int {
	n := 0
	for _, t := range tables {
		n += len(t.Indexes)
	}
	return n
}
