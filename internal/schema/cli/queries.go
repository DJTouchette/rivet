package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/djtouchette/rivet/internal/schema/cache"
	"github.com/djtouchette/rivet/internal/schema/types"
)

func queriesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queries",
		Short: "Inspect SQL queries found in application source or the database log",
	}
	cmd.AddCommand(queriesExtractedCmd(), queriesSlowCmd())
	return cmd
}

func queriesExtractedCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "extracted",
		Short: "SQL statements found in application source (Dapper, Go, Python, Node)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			queries, err := scanQueries(cfg)
			if err != nil {
				return err
			}
			if flagHuman {
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "%d queries:\n", len(queries))
				tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "  LANG\tKIND\tLOC\tSQL")
				for _, q := range queries {
					fmt.Fprintf(tw, "  %s\t%s\t%s:%d\t%s\n",
						q.Lang, q.Kind, q.File, q.Line, truncate(q.SQL, 100))
				}
				tw.Flush()
				return nil
			}
			return outputJSON(cmd, queries)
		},
	}
}

func queriesSlowCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "slow",
		Short: "Top expensive queries from the engine log",
		Long: `Pulls from:
  - Postgres: pg_stat_statements (requires extension)
  - MSSQL:    sys.dm_exec_query_stats + dm_exec_sql_text

Returns the top N queries by total elapsed time.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			db, err := resolveDB(cfg)
			if err != nil {
				return err
			}
			// The limit is applied when the snapshot is captured, not when it's
			// printed, so it has to travel with the request: a snapshot taken
			// with limit 25 can't answer --limit 50 and must be re-read.
			entry, err := loadOrFetch(cfg, db, need{SlowQueryLimit: limit})
			if err != nil {
				return err
			}
			reportFreshness(cmd, entry)
			// This command's entire output is the slow-query list, so if that is
			// the section the snapshot is missing, say so before printing an
			// empty one.
			reportGap(cmd, entry, cache.FeatureSlowQueries, "slow queries")
			slow := entry.SlowQueries
			if limit > 0 && len(slow) > limit {
				slow = slow[:limit]
			}
			if flagHuman {
				printSlowHuman(cmd, slow, entry.Gap(cache.FeatureSlowQueries) == nil)
				return nil
			}
			return outputJSON(cmd, slow)
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 25, "max queries to return")
	return cmd
}

// printSlowHuman renders the list. captured is false when the snapshot has a gap
// for slow queries, in which case the empty-list message must not be printed at
// all: it would assert "none" over data that was never read. reportGap has
// already explained the absence.
func printSlowHuman(cmd *cobra.Command, slow []types.SlowQuery, captured bool) {
	w := cmd.OutOrStdout()
	if len(slow) == 0 {
		if captured {
			fmt.Fprintln(w, "No slow queries in the snapshot — the server reported none.")
		}
		return
	}
	fmt.Fprintf(w, "Top %d queries by total time:\n", len(slow))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  CALLS\tTOTAL(ms)\tMEAN(ms)\tSQL")
	for _, q := range slow {
		fmt.Fprintf(tw, "  %d\t%.1f\t%.2f\t%s\n",
			q.Calls, q.TotalMS, q.MeanMS, truncate(q.Text, 80))
	}
	tw.Flush()
}
