package cli

import "github.com/spf13/cobra"

func NewRootCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rivet",
		Short:   "Project capability layer for Claude Code",
		Long:    "Rivet packages project-specific capabilities and exposes them to Claude Code via MCP.",
		Version: version,
	}

	cmd.AddCommand(
		newInitCmd(),
		newUpdateCmd(),
		newDoctorCmd(),
		newInspectCmd(),
		newProjectCmd(),
		newContextCmd(),
		newPolicyCmd(),
		newServeCmd(version),
		newSyncCmd(),
		newReconCmd(),
		newVaultyCmd(),
		newWitnessCmd(),
		newRunCmd(),
	)

	return cmd
}
