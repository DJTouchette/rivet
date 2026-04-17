package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/djtouchette/rivet/internal/schema/migrations"
)

func migrationsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrations",
		Short: "Reconstruct a schema from on-disk SQL migration files (no DB connection required)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			dirs := cfg.Migrations.AllDirs()
			if len(dirs) == 0 {
				return fmt.Errorf("no migration directory configured (schema.migrations.dir in .rivet/config.yaml)")
			}
			dir := dirs[0]
			res, err := migrations.Parse(dir, migrations.Options{Dialect: cfg.Migrations.Dialect})
			if err != nil {
				return err
			}
			if flagHuman {
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "Parsed %d migration file(s) from %s\n", res.Summary.Files, dir)
				fmt.Fprintf(w, "Reconstructed: %d tables, %d indexes\n", res.Summary.Tables, res.Summary.Indexes)
				if len(res.Unparsed) > 0 {
					fmt.Fprintf(w, "Partially unparsed: %d file(s)\n", len(res.Unparsed))
				}
				return nil
			}
			return outputJSON(cmd, res)
		},
	}
}
