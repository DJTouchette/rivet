package cli

import (
	witnessapp "github.com/djtouchette/witness/pkg/embedded"
	"github.com/spf13/cobra"
)

func newWitnessCmd() *cobra.Command {
	cmd := witnessapp.NewCommand("rivet witness")
	cmd.Use = "witness"
	cmd.Short = "Test selector — find which tests to run for changed files"

	return cmd
}
