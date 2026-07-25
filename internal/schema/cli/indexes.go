package cli

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/djtouchette/rivet/internal/schema/analyze"
	"github.com/djtouchette/rivet/internal/schema/config"
	"github.com/djtouchette/rivet/internal/schema/queryextract"
	"github.com/djtouchette/rivet/internal/schema/types"
)

func indexesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "indexes",
		Short: "Analyze indexes: unused, redundant, missing",
	}
	cmd.AddCommand(
		indexesUnusedCmd(),
		indexesRedundantCmd(),
		indexesMissingCmd(),
		indexesListCmd(),
	)
	return cmd
}

func indexesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every index in the target database",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			db, err := resolveDB(cfg)
			if err != nil {
				return err
			}
			entry, err := loadOrFetch(cfg, db, need{})
			if err != nil {
				return err
			}
			if entry.Schema == nil {
				return fmt.Errorf("schema snapshot is empty; refresh may have failed")
			}
			reportFreshness(cmd, entry)
			type row struct {
				Schema  string   `json:"schema"`
				Table   string   `json:"table"`
				Name    string   `json:"name"`
				Columns []string `json:"columns"`
				Unique  bool     `json:"unique"`
				Primary bool     `json:"primary,omitempty"`
				Size    int64    `json:"size_bytes"`
			}
			var rows []row
			for _, t := range entry.Schema.Tables {
				for _, idx := range t.Indexes {
					rows = append(rows, row{
						Schema: t.Schema, Table: t.Name, Name: idx.Name,
						Columns: idx.Columns, Unique: idx.Unique, Primary: idx.Primary,
						Size: idx.SizeBytes,
					})
				}
			}
			sort.Slice(rows, func(i, j int) bool {
				if rows[i].Table != rows[j].Table {
					return rows[i].Table < rows[j].Table
				}
				return rows[i].Name < rows[j].Name
			})
			if flagHuman {
				w := cmd.OutOrStdout()
				tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "  TABLE\tINDEX\tCOLUMNS\tUNIQUE\tSIZE")
				for _, r := range rows {
					fmt.Fprintf(tw, "  %s.%s\t%s\t%s\t%t\t%s\n",
						r.Schema, r.Table, r.Name, strings.Join(r.Columns, ","), r.Unique, humanBytes(r.Size))
				}
				tw.Flush()
				return nil
			}
			return outputJSON(cmd, rows)
		},
	}
}

func indexesUnusedCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unused",
		Short: "Indexes with zero reads (pure write cost) — candidates for dropping",
		Long: `Reads per index come from the live engine:
  - Postgres: pg_stat_user_indexes.idx_scan
  - MSSQL:    sys.dm_db_index_usage_stats (user_seeks + user_scans + user_lookups)

A snapshot must exist first — run 'rivet schema refresh'.
These numbers reset when the server restarts, so interpret them in context
of your server's uptime. They also reflect only the database you're connected
to — unused-in-dev doesn't mean unused-in-prod.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			db, err := resolveDB(cfg)
			if err != nil {
				return err
			}
			entry, err := loadOrFetch(cfg, db, need{})
			if err != nil {
				return err
			}
			reportFreshness(cmd, entry)
			unused := analyze.DetectUnused(entry.Schema, entry.IndexUsage)
			if flagHuman {
				printUnusedHuman(cmd, unused)
				return nil
			}
			return outputJSON(cmd, unused)
		},
	}
}

func indexesRedundantCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "redundant",
		Short: "Indexes fully covered by another index on the same table",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			db, err := resolveDB(cfg)
			if err != nil {
				return err
			}
			entry, err := loadOrFetch(cfg, db, need{})
			if err != nil {
				return err
			}
			reportFreshness(cmd, entry)
			red := analyze.DetectRedundant(entry.Schema)
			if flagHuman {
				printRedundantHuman(cmd, red)
				return nil
			}
			return outputJSON(cmd, red)
		},
	}
}

func indexesMissingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "missing",
		Short: "Indexes the engine and/or code analysis suggest adding",
		Long: `Combines two signals:

  (1) Engine hints — MSSQL's sys.dm_db_missing_index_details and Postgres'
      pg_qualstats record predicates the optimizer would have benefited from.

  (2) Code analysis — WHERE, JOIN, and ORDER BY columns extracted from your
      application source (C# Dapper, Go database/sql, Python, Node) that
      aren't covered by any existing index.

When both signals converge on the same table+columns, confidence is "high".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			db, err := resolveDB(cfg)
			if err != nil {
				return err
			}
			entry, err := loadOrFetch(cfg, db, need{})
			if err != nil {
				return err
			}
			reportFreshness(cmd, entry)
			queries, err := scanQueries(cfg)
			if err != nil {
				return err
			}
			missing := analyze.DetectMissing(entry.Schema, entry.Hints, queries)
			if flagHuman {
				printMissingHuman(cmd, missing)
				return nil
			}
			return outputJSON(cmd, missing)
		},
	}
}

// scanQueries walks the configured code-scan roots and extracts SQL.
func scanQueries(cfg *config.Config) ([]types.QueryRef, error) {
	opts := queryextract.Options{
		Roots:     cfg.CodeScan.Roots,
		Include:   cfg.CodeScan.Include,
		Exclude:   cfg.CodeScan.Exclude,
		Languages: cfg.CodeScan.Languages,
	}
	return queryextract.Scan(opts)
}

// --- humanizers ---

func printUnusedHuman(cmd *cobra.Command, u []types.UnusedIndex) {
	w := cmd.OutOrStdout()
	if len(u) == 0 {
		fmt.Fprintln(w, "No unused indexes detected.")
		return
	}
	fmt.Fprintf(w, "%d unused index%s:\n", len(u), pluralS(len(u)))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  TABLE\tINDEX\tWRITES\tSIZE\tREASON")
	for _, r := range u {
		fmt.Fprintf(tw, "  %s.%s\t%s\t%d\t%s\t%s\n",
			r.Schema, r.Table, r.Index, r.Writes, humanBytes(r.SizeBytes), r.Reason)
	}
	tw.Flush()
}

func printRedundantHuman(cmd *cobra.Command, r []types.RedundantIndex) {
	w := cmd.OutOrStdout()
	if len(r) == 0 {
		fmt.Fprintln(w, "No redundant indexes detected.")
		return
	}
	fmt.Fprintf(w, "%d redundant index%s:\n", len(r), pluralS(len(r)))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  TABLE\tINDEX\tCOVERED BY\tREASON")
	for _, x := range r {
		fmt.Fprintf(tw, "  %s.%s\t%s\t%s\t%s\n",
			x.Schema, x.Table, x.Index, x.CoveredBy, x.Reason)
	}
	tw.Flush()
}

func printMissingHuman(cmd *cobra.Command, m []types.MissingIndex) {
	w := cmd.OutOrStdout()
	if len(m) == 0 {
		fmt.Fprintln(w, "No missing-index candidates detected.")
		return
	}
	fmt.Fprintf(w, "%d missing-index candidate%s:\n\n", len(m), pluralS(len(m)))
	for _, c := range m {
		qname := c.Table
		if c.Schema != "" {
			qname = c.Schema + "." + c.Table
		}
		fmt.Fprintf(w, "  [%s / %s] %s (%s)\n", c.Confidence, c.Source, qname, strings.Join(c.Columns, ","))
		for _, e := range c.Evidence {
			fmt.Fprintf(w, "      evidence: %s\n", e)
		}
		for _, q := range c.SampleQueries {
			fmt.Fprintf(w, "      %s:%d  %s\n", q.File, q.Line, truncate(q.SQL, 100))
		}
		fmt.Fprintln(w)
	}
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
