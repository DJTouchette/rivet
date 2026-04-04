package cli

import (
	"fmt"

	"github.com/djtouchette/rivet/internal/config"
	"github.com/spf13/cobra"
)

func newInspectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Inspect Rivet configuration and state",
	}

	cmd.AddCommand(
		newInspectCapabilitiesCmd(),
	)

	return cmd
}

func newInspectCapabilitiesCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "capabilities",
		Short:   "List registered capabilities",
		Aliases: []string{"caps"},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadOrDefault("")
			if err != nil {
				return err
			}

			reg := buildRegistry(cfg)
			caps := reg.List()
			if len(caps) == 0 {
				fmt.Println("No capabilities registered.")
				fmt.Println("Edit .rivet/config.yaml to add capabilities.")
				return nil
			}

			fmt.Printf("%-30s %-18s %-12s %s\n", "NAME", "KIND", "SAFETY", "DESCRIPTION")
			for _, c := range caps {
				fmt.Printf("%-30s %-18s %-12s %s\n",
					c.Name, c.Kind, c.Safety, c.Description)
			}
			return nil
		},
	}
}
