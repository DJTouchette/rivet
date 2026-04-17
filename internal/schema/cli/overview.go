package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

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

			for i := range cfg.Databases {
				db := &cfg.Databases[i]
				summary := types.DatabaseSummary{
					Name: db.Name, Engine: db.Engine, Host: db.Host,
				}

				entry, err := store.Load(db.Name)
				if err != nil || entry == nil {
					summary.Error = "no snapshot — run 'rivet schema refresh'"
					ov.Databases = append(ov.Databases, summary)
					continue
				}
				summary.Connected = true
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
	fmt.Fprintln(tw, "  NAME\tENGINE\tHOST\tSTATUS\tTABLES\tINDEXES")
	for _, d := range ov.Databases {
		status := "connected"
		if !d.Connected {
			status = "offline"
			if d.Error != "" {
				status = d.Error
			}
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%d\t%d\n",
			d.Name, d.Engine, d.Host, status, d.Tables, d.Indexes)
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

var _ = strings.Index
