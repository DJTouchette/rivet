package cli

import (
	vaultyapp "github.com/djtouchette/vaulty/pkg/embedded"
	"github.com/spf13/cobra"
)

func newVaultyCmd() *cobra.Command {
	cmd := vaultyapp.NewCommand("rivet vaulty")
	cmd.Use = "vaulty"
	cmd.Short = "Secret management — list, set, remove, exec, proxy"

	return cmd
}
