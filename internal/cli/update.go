package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Add any missing Rivet files without overwriting config",
		Long: `Bring an existing Rivet project up to date without overwriting .rivet/config.yaml.

This command creates any missing Rivet directories and installs missing MCP config,
Claude hooks, skills, and subagents. Existing files are preserved.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			actions, err := ensureProjectSetup(false)
			if err != nil {
				return err
			}

			fmt.Println("Rivet updated:")
			for _, a := range actions {
				fmt.Printf("  + %s\n", a)
			}
			fmt.Println()
			fmt.Println("Next steps:")
			fmt.Println("  /agents                  Confirm rivet-explorer and rivet-investigator are available")
			fmt.Println("  rivet sync               Update CLAUDE.md with Rivet rules and capabilities")
			return nil
		},
	}
}
