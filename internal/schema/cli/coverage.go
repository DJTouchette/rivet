package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/djtouchette/rivet/internal/schema/analyze"
	"github.com/djtouchette/rivet/internal/schema/types"
)

func coverageCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "coverage",
		Short: "Map application queries to tables and show which predicates have a covering index",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			db, err := resolveDB(cfg)
			if err != nil {
				return err
			}
			entry, err := loadOrFetch(db)
			if err != nil {
				return err
			}
			queries, err := scanQueries(cfg)
			if err != nil {
				return err
			}
			report := analyze.BuildCoverage(entry.Schema, queries)
			if flagHuman {
				printCoverageHuman(cmd, report)
				return nil
			}
			return outputJSON(cmd, report)
		},
	}
}

func printCoverageHuman(cmd *cobra.Command, r *types.CoverageReport) {
	w := cmd.OutOrStdout()
	if r == nil || len(r.Tables) == 0 {
		fmt.Fprintln(w, "No coverage data — no extracted queries matched any configured table.")
		return
	}
	for _, t := range r.Tables {
		fmt.Fprintf(w, "\n%s.%s (%d queries hit this table)\n", t.Schema, t.Table, t.QueriesHit)
		if len(t.Predicates) == 0 {
			continue
		}
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  CLAUSE\tCOLUMNS\tHITS\tCOVERED\tBY")
		for _, p := range t.Predicates {
			covered := "no"
			if p.Covered {
				covered = "yes"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%d\t%s\t%s\n",
				p.Clause, strings.Join(p.Columns, ","), p.Occurrences, covered, p.CoveringIndex)
		}
		tw.Flush()
	}
}
