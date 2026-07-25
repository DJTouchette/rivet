package cli

import (
	witnessapp "github.com/djtouchette/witness/pkg/embedded"
	"github.com/spf13/cobra"
)

func newWitnessCmd() *cobra.Command {
	cmd := witnessapp.NewCommand("rivet witness")
	cmd.Use = "witness"
	cmd.Short = "Test selector — find which tests to run for changed files"
	// Witness reads recon's index; point it at .rivet/recon/ like every other
	// rivet entry point so it doesn't build its own .recon/ alongside.
	useRivetCacheDir(cmd)

	return cmd
}
