package cli

import (
	reconapp "github.com/djtouchette/recon/pkg/embedded"
	"github.com/djtouchette/rivet/internal/recon"
	"github.com/spf13/cobra"
)

func newReconCmd() *cobra.Command {
	cmd := reconapp.NewCommand("rivet recon")
	cmd.Use = "recon"
	cmd.Short = "Repo intelligence — overview, symbols, search, hotspots, and more"
	useRivetCacheDir(cmd)

	return cmd
}

// useRivetCacheDir repoints an embedded recon/witness command tree at
// .rivet/recon/, matching what the MCP adapters (internal/recon,
// internal/witness) inject on every call.
//
// Without this the CLI subtrees fall back to recon's own default of
// <root>/.recon/, so `rivet recon search foo` builds and maintains a second
// index that `rivet serve` never reads — duplicated scan cost plus two caches
// that answer the same question differently as they age apart.
//
// The flag is rewritten rather than hard-wired so an explicit
// `--cache-dir` on the command line still wins: pflag parses after this and
// overwrites the value. DefValue is updated too so `--help` doesn't advertise a
// default the command no longer uses.
//
// Both flag sets are checked at every level because the two tools bind the flag
// differently: recon declares it as a persistent flag on its root, witness
// declares it per-subcommand on `select` and `run`.
func useRivetCacheDir(cmd *cobra.Command) {
	dir := recon.CacheDir()
	if f := cmd.PersistentFlags().Lookup("cache-dir"); f != nil {
		_ = f.Value.Set(dir)
		f.DefValue = dir
	}
	if f := cmd.Flags().Lookup("cache-dir"); f != nil {
		_ = f.Value.Set(dir)
		f.DefValue = dir
	}
	for _, sub := range cmd.Commands() {
		useRivetCacheDir(sub)
	}
}
