// Package witness provides in-process execution of witness commands
// via the embedded github.com/djtouchette/witness dependency.
// When running inside rivet, the cache is stored in .rivet/recon/
// so witness shares recon's cache.
package witness

import (
	"bytes"
	"strings"

	"github.com/djtouchette/rivet/internal/recon"
	witnessapp "github.com/djtouchette/witness/pkg/embedded"
)

// Run executes a witness command in-process and captures its output.
// The args slice should contain the subcommand and its arguments
// (e.g. ["select", "lib/app/foo.ex"]).
//
// The recon cache is stored in .rivet/recon/ to share with the
// embedded recon instance.
//
// Witness fails closed: it exits non-zero rather than emit a test command it
// cannot get right (a language it has no runner for, --fallback=fail, a
// --format paths selection it cannot vouch for). Everything that makes such a
// failure legible has to survive this function, because the caller — an MCP
// tool result, or `rivet project run` — has nothing else to go on. A bare
// "exit code: 1" with no reason is a failure the agent will route around.
//
// Which of those behaviours the embedded build actually has depends on the
// TAGGED version in go.mod, not on the sibling checkout — see PinnedVersion.
// The capability descriptions in internal/capabilities/builtins.go are written
// to be true of any witness rivet might embed for exactly that reason.
func Run(args []string) (stdout, stderr string, exitCode int, err error) {
	cmd := witnessapp.NewCommand("embedded")

	// Use rivet's recon cache directory (shared with the recon adapter).
	fullArgs := append([]string{"--cache-dir", recon.CacheDir()}, args...)

	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(fullArgs)

	cmd.SilenceErrors = true
	cmd.SilenceUsage = true

	if runErr := cmd.Execute(); runErr != nil {
		// SilenceErrors is what keeps cobra off the host's real stderr, which
		// under `rivet serve` is carrying the MCP stdio stream — so nothing
		// prints runErr and its text ends here unless it is appended. That text
		// IS the explanation: "no test runner known for language \"java\"",
		// "witness cannot prove which tests cover this change". Dropping it left
		// the agent with an unexplained exit code next to an empty stdout, which
		// reads exactly like "there was nothing to run".
		//
		// The exit code stays 1 for every failure. `witness run` carries the
		// test runner's own code in an error type that pkg/embedded does not
		// export at PinnedVersion, and no rivet capability invokes `run` (the
		// witness.* builtins are all `select`); non-zero is the part that
		// matters.
		//
		// witness v0.5.0 exports it as embedded.ExitCodeError, with
		// embedded.TestsFailed(err) (int, bool) to read it. When the pin moves,
		// return that code here instead of a flat 1 so a caller can tell "the
		// tests ran and failed" from "witness could not run them" — the same
		// distinction the descriptions above ask the agent to make.
		return outBuf.String(), appendError(errBuf.String(), runErr), 1, nil
	}

	return outBuf.String(), errBuf.String(), 0, nil
}

// appendError adds the command's error to whatever it already wrote to stderr,
// prefixed so the reader can tell witness's diagnosis from its progress notes.
func appendError(stderr string, err error) string {
	var b strings.Builder
	b.WriteString(stderr)
	if stderr != "" && !strings.HasSuffix(stderr, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("witness: ")
	b.WriteString(err.Error())
	b.WriteString("\n")
	return b.String()
}
