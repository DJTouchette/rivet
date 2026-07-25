package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/djtouchette/rivet/internal/schema/types"
)

func tablesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tables",
		Short: "List tables in the target database with row estimates and sizes",
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

			if flagHuman {
				printTablesHuman(cmd, entry.Schema)
				return nil
			}
			return outputJSON(cmd, entry.Schema.Tables)
		},
	}
}

func printTablesHuman(cmd *cobra.Command, s *types.Schema) {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "%s (%s) — %d tables\n", s.Database, s.Engine, len(s.Tables))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  SCHEMA.TABLE\tCOLS\tIDX\tFKS\tROWS\tSIZE")
	for _, t := range s.Tables {
		fmt.Fprintf(tw, "  %s\t%d\t%d\t%d\t%d\t%s\n",
			t.QualifiedName(), len(t.Columns), len(t.Indexes), len(t.ForeignKeys),
			t.RowEstimate, humanBytes(t.SizeBytes))
	}
	tw.Flush()
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}
