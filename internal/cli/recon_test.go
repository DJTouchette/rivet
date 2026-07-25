package cli

import (
	"strings"
	"testing"

	"github.com/djtouchette/rivet/internal/recon"
	"github.com/spf13/cobra"
)

// collectCacheDirs walks a command tree and records the current value of every
// "cache-dir" flag it finds, keyed by command path. Both flag sets are checked
// because recon declares the flag persistently on its root while witness
// declares it per-subcommand.
func collectCacheDirs(cmd *cobra.Command, prefix string, out map[string]string) {
	path := strings.TrimSpace(prefix + " " + cmd.Name())
	if f := cmd.PersistentFlags().Lookup("cache-dir"); f != nil {
		out[path] = f.Value.String()
	}
	if f := cmd.Flags().Lookup("cache-dir"); f != nil {
		out[path] = f.Value.String()
	}
	for _, sub := range cmd.Commands() {
		collectCacheDirs(sub, path, out)
	}
}

// The `rivet recon` / `rivet witness` CLI subtrees must read and write the same
// index as the MCP adapters in internal/recon and internal/witness, which inject
// --cache-dir .rivet/recon. Left alone, the embedded commands default to
// <root>/.recon/, so `rivet recon search foo` builds a second, divergent index
// that `rivet serve` never reads: the scan cost is paid twice and the two caches
// answer the same question differently as they age apart.
func TestReconAndWitnessCLIUseRivetCacheDir(t *testing.T) {
	tests := []struct {
		name string
		cmd  *cobra.Command
	}{
		{"recon", newReconCmd()},
		{"witness", newWitnessCmd()},
	}

	want := recon.CacheDir()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found := map[string]string{}
			collectCacheDirs(tt.cmd, "", found)

			// A zero count means the upstream tool renamed or dropped the flag,
			// which would silently disable the injection — that's a failure, not
			// a pass.
			if len(found) == 0 {
				t.Fatalf("no cache-dir flag found in the %s command tree", tt.name)
			}

			for path, got := range found {
				if got != want {
					t.Errorf("%s: cache-dir = %q, want %q", path, got, want)
				}
			}
		})
	}
}

// The default is rewritten, not hard-wired: a human debugging a stale index must
// still be able to point the command somewhere else. pflag parses after
// construction, so an explicit flag overwrites the injected value.
func TestCacheDirOverrideStillWins(t *testing.T) {
	const custom = "/tmp/rivet-test-cache"

	tests := []struct {
		name string
		// flags returns the flag set that carries cache-dir for this tool.
		flags func() *cobra.Command
		args  []string
	}{
		{
			name:  "recon root persistent flag",
			flags: newReconCmd,
			args:  []string{"--cache-dir", custom, "overview"},
		},
		{
			name:  "witness select subcommand flag",
			flags: newWitnessCmd,
			args:  []string{"select", "--cache-dir", custom},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := tt.flags()

			target, remaining, err := root.Find(tt.args)
			if err != nil {
				t.Fatalf("find subcommand: %v", err)
			}
			// ParseFlags merges the persistent flags of the command chain, which
			// is exactly what cobra does at execution time.
			if err := target.ParseFlags(remaining); err != nil {
				t.Fatalf("parse flags: %v", err)
			}

			f := target.Flags().Lookup("cache-dir")
			if f == nil {
				t.Fatal("cache-dir flag not found after parsing")
			}
			if got := f.Value.String(); got != custom {
				t.Errorf("cache-dir = %q, want the user-supplied %q", got, custom)
			}
		})
	}
}
