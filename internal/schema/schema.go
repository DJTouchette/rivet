// Package schema is the rivet-side shim around the schema-intel subsystem.
//
// It exposes two entry points:
//   NewCommand — the cobra tree, used by `rivet schema ...` and by MCP.
//   Run        — an in-process runner that captures stdout/stderr into strings,
//                matching internal/recon/recon.go. This is what the MCP
//                executor calls for schema.* capabilities.
package schema

import (
	"bytes"

	"github.com/spf13/cobra"

	// Ensure catalog driver side-effect imports run.
	_ "github.com/djtouchette/rivet/internal/schema/catalog/mssql"
	_ "github.com/djtouchette/rivet/internal/schema/catalog/postgres"

	schemacli "github.com/djtouchette/rivet/internal/schema/cli"
)

// NewCommand returns the cobra tree for schema-intel, suitable for embedding
// in rivet's command tree.
func NewCommand(use string) *cobra.Command {
	return schemacli.NewRoot(use, "embedded")
}

// Run executes a schema command in-process. Mirrors internal/recon.Run:
// (stdout, stderr, exitCode, fatalErr).
func Run(args []string) (stdout, stderr string, exitCode int, err error) {
	cmd := NewCommand("schema")

	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)

	cmd.SilenceErrors = true
	cmd.SilenceUsage = true

	if runErr := cmd.Execute(); runErr != nil {
		return outBuf.String(), errBuf.String() + runErr.Error() + "\n", 1, nil
	}
	return outBuf.String(), errBuf.String(), 0, nil
}
