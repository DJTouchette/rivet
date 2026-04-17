// Package cli is the cobra command tree for schema-intel. It's exposed via
// schema.NewCommand so rivet can embed it (as a subcommand of `rivet schema`)
// and also run it in-process for MCP tool calls.
package cli

import (
	"github.com/spf13/cobra"
)

var (
	flagHuman   bool
	flagDB      string
	flagConfig  string
	flagCacheDir string
)

// NewRoot returns the cobra tree. `use` is the command name ("schema" when
// invoked standalone, "rivet schema" when embedded).
func NewRoot(use, version string) *cobra.Command {
	root := &cobra.Command{
		Use:   use,
		Short: "Database schema intelligence — tables, indexes, missing/unused detection",
		Long: `Schema-intel reads your configured database(s), your SQL migrations,
and your application source code to answer questions like:

  - Which indexes is nobody using?
  - Which indexes are redundant with others?
  - What indexes should we add? (from engine hints + code analysis)
  - Which queries hit this table, and are they covered by an index?

Configuration lives in .rivet/config.yaml under the 'schema:' key.
Connections are read-only — only system catalogs are queried.`,
	}

	root.PersistentFlags().BoolVar(&flagHuman, "human", false, "human-readable output instead of JSON")
	root.PersistentFlags().StringVar(&flagDB, "db", "", "database to target (default: the one marked default in config)")
	root.PersistentFlags().StringVar(&flagConfig, "config", "", "path to rivet config.yaml (default: .rivet/config.yaml)")
	root.PersistentFlags().StringVar(&flagCacheDir, "cache-dir", "", "snapshot cache directory (default: .rivet/schema/)")

	root.AddCommand(
		overviewCmd(),
		tablesCmd(),
		describeCmd(),
		indexesCmd(),
		queriesCmd(),
		coverageCmd(),
		migrationsCmd(),
		refreshCmd(),
		versionCmd(version),
	)

	return root
}

func versionCmd(v string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Version info",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _ = cmd.OutOrStdout().Write([]byte("schema " + v + "\n"))
			return nil
		},
	}
}
