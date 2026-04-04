// Package vaulty provides in-process execution of vaulty commands
// via the embedded github.com/djtouchette/vaulty dependency.
package vaulty

import (
	"bytes"

	vaultyapp "github.com/djtouchette/vaulty/pkg/embedded"
)

// Run executes a vaulty command in-process and captures its output.
// The args slice should contain the subcommand and its arguments
// (e.g. ["list"] or ["proxy", "GET", "https://example.com"]).
func Run(args []string) (stdout, stderr string, exitCode int, err error) {
	cmd := vaultyapp.NewCommand("embedded")

	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)

	// Silence cobra's own error/usage printing — we capture everything.
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true

	if runErr := cmd.Execute(); runErr != nil {
		return outBuf.String(), errBuf.String(), 1, nil
	}

	return outBuf.String(), errBuf.String(), 0, nil
}
