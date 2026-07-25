package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/djtouchette/rivet/internal/schema/migrations"
	"github.com/djtouchette/rivet/internal/schema/types"
)

func overviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "overview",
		Short: "Summary of configured databases, table counts, and migration status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			ov := &types.Overview{}
			for _, db := range cfg.Databases {
				ov.Sources = append(ov.Sources, db.Name)
			}

			store, err := openCache()
			if err != nil {
				return err
			}

			maxAge, err := cfg.Cache.MaxAgeDuration()
			if err != nil {
				return err
			}
			now := time.Now().UTC()

			for i := range cfg.Databases {
				db := &cfg.Databases[i]
				summary := types.DatabaseSummary{
					Name: db.Name, Engine: db.Engine, Host: db.Host,
				}

				// Connected has to mean "answered just now". Pinging costs one
				// short dial per configured database (pingTimeout), which is
				// the price of the field being true only when it's true.
				var problems []string
				if err := pingDatabase(db); err != nil {
					problems = append(problems, "connect failed: "+err.Error())
				} else {
					summary.Connected = true
				}

				entry, err := store.Load(db.Name)
				if err != nil || entry == nil {
					problems = append(problems, "no snapshot — run 'rivet schema refresh'")
					summary.Error = strings.Join(problems, "; ")
					ov.Databases = append(ov.Databases, summary)
					continue
				}

				if !entry.FetchedAt.IsZero() {
					summary.SnapshotFetchedAt = entry.FetchedAt.Format(time.RFC3339)
				}
				summary.SnapshotAge = humanAge(entry.Age(now))
				summary.SnapshotStale = entry.IsStale(now, maxAge)
				if summary.SnapshotStale {
					problems = append(problems, "snapshot is stale — run 'rivet schema refresh'")
				}
				summary.Error = strings.Join(problems, "; ")

				if entry.Schema != nil {
					summary.Tables = len(entry.Schema.Tables)
					summary.Views = len(entry.Schema.Views)
					for _, t := range entry.Schema.Tables {
						summary.Indexes += len(t.Indexes)
					}
				}
				ov.Databases = append(ov.Databases, summary)
			}

			// Migrations
			dirs := cfg.Migrations.AllDirs()
			for _, d := range dirs {
				res, err := migrations.Parse(d, migrations.Options{Dialect: cfg.Migrations.Dialect})
				if err != nil {
					continue
				}
				ov.Migrations = &res.Summary
				break
			}

			if flagHuman {
				printOverviewHuman(cmd, ov)
				return nil
			}
			return outputJSON(cmd, ov)
		},
	}
}

func printOverviewHuman(cmd *cobra.Command, ov *types.Overview) {
	w := cmd.OutOrStdout()
	fmt.Fprintln(w, "Databases:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  NAME\tENGINE\tHOST\tSTATUS\tSNAPSHOT\tTABLES\tINDEXES\tNOTE")
	for _, d := range ov.Databases {
		status := "offline"
		if d.Connected {
			status = "connected"
		}
		// Counts come from the snapshot, so its age is shown next to them
		// rather than left to be inferred from STATUS.
		snap := "none"
		if d.SnapshotAge != "" {
			snap = d.SnapshotAge + " old"
			if d.SnapshotStale {
				snap += " (STALE)"
			}
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
			d.Name, d.Engine, d.Host, status, snap, d.Tables, d.Indexes, d.Error)
	}
	tw.Flush()

	if ov.Migrations != nil {
		m := ov.Migrations
		fmt.Fprintf(w, "\nMigrations: %d files in %s (%d tables, %d indexes)",
			m.Files, m.Directory, m.Tables, m.Indexes)
		if len(m.Unparsed) > 0 {
			fmt.Fprintf(w, "  — %d files partially unparsed", len(m.Unparsed))
		}
		fmt.Fprintln(w)
	}
}
