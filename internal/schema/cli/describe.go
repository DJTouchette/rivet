package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/djtouchette/rivet/internal/schema/types"
)

func describeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "describe <table>",
		Short: "Show columns, indexes, and foreign keys for a single table",
		Args:  cobra.ExactArgs(1),
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
			if entry.Schema == nil {
				return fmt.Errorf("schema snapshot is empty")
			}
			t := findTable(entry.Schema, args[0])
			if t == nil {
				return fmt.Errorf("table %q not found — try schema.tables first", args[0])
			}
			if flagHuman {
				printTableHuman(cmd, t)
				return nil
			}
			return outputJSON(cmd, t)
		},
	}
}

func findTable(s *types.Schema, needle string) *types.Table {
	needle = strings.ToLower(needle)
	for i := range s.Tables {
		t := &s.Tables[i]
		if strings.ToLower(t.Name) == needle ||
			strings.ToLower(t.QualifiedName()) == needle {
			return t
		}
	}
	return nil
}

func printTableHuman(cmd *cobra.Command, t *types.Table) {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Table: %s (%d rows, %s)\n\n", t.QualifiedName(), t.RowEstimate, humanBytes(t.SizeBytes))

	fmt.Fprintln(w, "Columns:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  POS\tNAME\tTYPE\tNULL\tDEFAULT")
	for _, c := range t.Columns {
		null := "YES"
		if !c.Nullable {
			null = "NO"
		}
		fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\t%s\n", c.Position, c.Name, c.DataType, null, c.Default)
	}
	tw.Flush()

	if len(t.PrimaryKey) > 0 {
		fmt.Fprintf(w, "\nPrimary Key: %s\n", strings.Join(t.PrimaryKey, ", "))
	}

	if len(t.Indexes) > 0 {
		fmt.Fprintln(w, "\nIndexes:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  NAME\tCOLUMNS\tUNIQUE\tPRIMARY\tINCLUDE")
		for _, idx := range t.Indexes {
			fmt.Fprintf(tw, "  %s\t%s\t%t\t%t\t%s\n",
				idx.Name, strings.Join(idx.Columns, ","), idx.Unique, idx.Primary, strings.Join(idx.Include, ","))
		}
		tw.Flush()
	}

	if len(t.ForeignKeys) > 0 {
		fmt.Fprintln(w, "\nForeign Keys:")
		for _, fk := range t.ForeignKeys {
			target := fk.ReferencedTable
			if fk.ReferencedSchema != "" && fk.ReferencedSchema != "dbo" && fk.ReferencedSchema != "public" {
				target = fk.ReferencedSchema + "." + target
			}
			fmt.Fprintf(w, "  %s: (%s) → %s(%s)\n",
				fk.Name,
				strings.Join(fk.Columns, ","),
				target,
				strings.Join(fk.ReferencedColumns, ","))
		}
	}
}
