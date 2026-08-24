package cli

import (
	"fmt"

	"github.com/djtouchette/rivet/internal/provider"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	var providerSpec string

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Add any missing Rivet files without overwriting config",
		Long: `Bring an existing Rivet project up to date without overwriting .rivet/config.yaml.

This command creates any missing Rivet directories and installs missing MCP config,
skills, and subagents. Existing files are preserved. It also retires the bash nudge
hooks older versions installed — nudging now comes from the MCP server itself.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			providers, err := provider.Resolve(providerSpec, ".")
			if err != nil {
				return err
			}

			actions, err := ensureProjectSetup(false, providers)
			if err != nil {
				return err
			}

			fmt.Println("Rivet updated:")
			for _, a := range actions {
				fmt.Printf("  + %s\n", a)
			}
			fmt.Println()
			fmt.Println("Next steps:")
			if hasProvider(providers, provider.Claude().Name()) {
				fmt.Println("  /agents                  Confirm rivet-explorer and rivet-investigator are available")
			}
			fmt.Printf("  rivet sync               Update %s with Rivet rules and capabilities\n", instructionFileList(providers))
			return nil
		},
	}

	cmd.Flags().StringVar(&providerSpec, "provider", "auto", providerFlagUsage)
	return cmd
}
