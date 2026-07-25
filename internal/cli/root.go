package cli

import "github.com/spf13/cobra"

func NewRootCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rivet",
		Short:   "Project capability layer for Claude Code",
		Long:    "Rivet packages project-specific capabilities and exposes them to Claude Code via MCP.",
		Version: version,

		// main already prints the error and sets the exit code. Without this,
		// cobra prints it too and every failure appears twice — inherited by
		// every subcommand, so it is fixed once here rather than per command.
		SilenceErrors: true,
	}

	cmd.AddCommand(
		newInitCmd(),
		newUpdateCmd(),
		newDoctorCmd(),
		newInspectCmd(),
		newProjectCmd(),
		newContextCmd(),
		newRunbookCmd(),
		newLearningsCmd(),
		newPolicyCmd(),
		newServeCmd(version),
		newSyncCmd(),
		newReconCmd(),
		newSchemaCmd(),
		newVaultyCmd(),
		newWitnessCmd(),
		newRunCmd(),
	)

	return cmd
}
