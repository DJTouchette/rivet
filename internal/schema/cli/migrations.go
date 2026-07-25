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
			// Every configured root, merged — this used to be dirs[0] only, which
			// gave a project with split migrations a silently half-built schema.
			res, err := migrations.ParseAll(dirs, migrations.Options{Dialect: cfg.Migrations.Dialect})
			if err != nil {
				return err
			}
			if flagHuman {
				printMigrationsHuman(cmd, res)
				return nil
			}
			// A root that failed or contributed nothing is in Summary.Sources on
			// stdout; the warnings also go to stderr so they are impossible to
			// miss when only the schema is being read.
			reportMigrationProblems(cmd, res)
			return outputJSON(cmd, res)
		},
	}
}

// printMigrationsHuman lists every root and what it contributed, so "half my
// migrations are missing" is visible from the output instead of inferred from a
// table count that looks plausible.
func printMigrationsHuman(cmd *cobra.Command, res *migrations.Result) {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Parsed %d migration file(s) from %d root(s):\n", res.Summary.Files, len(res.Summary.Sources))
	for _, s := range res.Summary.Sources {
		if s.Error != "" {
			fmt.Fprintf(w, "  %s: FAILED — %s\n", s.Directory, s.Error)
			continue
		}
		fmt.Fprintf(w, "  %s: %d file(s)\n", s.Directory, s.Files)
	}
	fmt.Fprintf(w, "Reconstructed: %d tables, %d indexes\n", res.Summary.Tables, res.Summary.Indexes)
	if len(res.Unparsed) > 0 {
		fmt.Fprintf(w, "Partially unparsed: %d file(s)\n", len(res.Unparsed))
	}
	for _, warn := range res.Summary.Warnings {
		fmt.Fprintf(w, "warning: %s\n", warn)
	}
}

// reportMigrationProblems echoes failed roots and merge warnings out of band,
// following the same rule as reportFreshness: stdout in human mode, stderr in
// JSON mode so stdout stays one parseable document.
func reportMigrationProblems(cmd *cobra.Command, res *migrations.Result) {
	if res == nil {
		return
	}
	for _, s := range res.Summary.Sources {
		if s.Error != "" {
			reportLine(cmd, fmt.Sprintf("WARNING: migration root %s could not be read (%s) — the reconstructed schema is incomplete", s.Directory, s.Error))
		}
	}
	for _, warn := range res.Summary.Warnings {
		reportLine(cmd, "WARNING: "+warn)
	}
}
