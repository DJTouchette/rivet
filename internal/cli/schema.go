package cli

import (
	"github.com/spf13/cobra"

	"github.com/djtouchette/rivet/internal/schema"
)

func newSchemaCmd() *cobra.Command {
	cmd := schema.NewCommand("schema")
	cmd.Use = "schema"
	cmd.Short = "Database schema intelligence — tables, indexes, missing/unused/redundant"
	return cmd
}
