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

// CacheDir returns the recon cache directory rivet uses: .rivet/recon under the
// directory rivet was invoked in, as an ABSOLUTE path.
//
// Every rivet entry point that drives recon or witness — the MCP adapters and
// the `rivet recon` / `rivet witness` CLI subtrees — must point at this one
// directory. Recon's own default is <root>/.recon/, so any path that forgets to
// override it silently builds and maintains a second index that the other paths
// never read.
//
// It is absolute because the two embedded tools do NOT resolve a relative
// --cache-dir the same way. recon resolves it against the process's working
// directory; witness from v0.5.0 resolves it against the git repository root.
// Handing both the same relative ".rivet/recon" from a subdirectory therefore
// produces two caches — recon's under the subdirectory, witness's at the repo
// root — and the "recon and witness share one cache" invariant quietly stops
// holding. An absolute path is passed through untouched by both, in every
// version, so the two agree wherever rivet is run from.
//
// The anchor is the working directory rather than the repository root because
// that is where recon ANALYSES: rivet is meant to be run from the project root,
// and pinning the cache to the repo root while recon indexed a subdirectory
// would point one cache at two different analyses.
func CacheDir() string {
	dir := filepath.Join(".rivet", "recon")
	abs, err := filepath.Abs(dir)
	if err != nil {
		// No working directory to resolve against. The relative path is what
		// both tools did before, so it is the safe thing to fall back to.
		return dir
	}
	return abs
}

// Run executes a recon command in-process and captures its output.
// The args slice should contain the subcommand and its arguments
// (e.g. ["overview"] or ["symbols", "auth"]).
//
// The cache is stored in .rivet/recon/ instead of the default .recon/
// so that projects only need a single .rivet/ directory.
func Run(args []string) (stdout, stderr string, exitCode int, err error) {
	cmd := reconapp.NewCommand("embedded")

	// Store cache inside .rivet/ instead of creating a separate .recon/.
	fullArgs := append([]string{"--cache-dir", CacheDir()}, args...)

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
