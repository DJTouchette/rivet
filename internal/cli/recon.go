package cli

import (
	reconapp "github.com/djtouchette/recon/pkg/embedded"
	"github.com/spf13/cobra"
)

func newReconCmd() *cobra.Command {
	cmd := reconapp.NewCommand("rivet recon")
	cmd.Use = "recon"
	cmd.Short = "Repo intelligence — overview, symbols, search, hotspots, and more"

	return cmd
}
