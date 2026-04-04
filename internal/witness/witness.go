// Package witness provides in-process execution of witness commands
// via the embedded github.com/djtouchette/witness dependency.
// When running inside rivet, the cache is stored in .rivet/recon/
// so witness shares recon's cache.
package witness

import (
	"bytes"
	"path/filepath"

	witnessapp "github.com/djtouchette/witness/pkg/embedded"
)

// Run executes a witness command in-process and captures its output.
// The args slice should contain the subcommand and its arguments
// (e.g. ["select", "lib/app/foo.ex"]).
//
// The recon cache is stored in .rivet/recon/ to share with the
// embedded recon instance.
func Run(args []string) (stdout, stderr string, exitCode int, err error) {
	cmd := witnessapp.NewCommand("embedded")

	// Use rivet's recon cache directory.
	cacheDir := filepath.Join(".rivet", "recon")
	fullArgs := append([]string{"--cache-dir", cacheDir}, args...)

	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(fullArgs)

	cmd.SilenceErrors = true
	cmd.SilenceUsage = true

	if runErr := cmd.Execute(); runErr != nil {
		return outBuf.String(), errBuf.String(), 1, nil
	}

	return outBuf.String(), errBuf.String(), 0, nil
}
