// Package recon provides in-process execution of recon commands
// via the embedded github.com/djtouchette/recon dependency.
// When running inside rivet, the cache is stored in .rivet/recon/
// so projects only have one dot-directory (.rivet/).
package recon

import (
	"bytes"
	"path/filepath"

	reconapp "github.com/djtouchette/recon/pkg/embedded"
)

// Run executes a recon command in-process and captures its output.
// The args slice should contain the subcommand and its arguments
// (e.g. ["overview"] or ["symbols", "auth"]).
//
// The cache is stored in .rivet/recon/ instead of the default .recon/
// so that projects only need a single .rivet/ directory.
func Run(args []string) (stdout, stderr string, exitCode int, err error) {
	cmd := reconapp.NewCommand("embedded")

	// Store cache inside .rivet/ instead of creating a separate .recon/.
	cacheDir := filepath.Join(".rivet", "recon")
	fullArgs := append([]string{"--cache-dir", cacheDir}, args...)

	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(fullArgs)

	// Silence cobra's own error/usage printing — we capture everything.
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true

	if runErr := cmd.Execute(); runErr != nil {
		return outBuf.String(), errBuf.String(), 1, nil
	}

	return outBuf.String(), errBuf.String(), 0, nil
}
