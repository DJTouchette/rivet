package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/djtouchette/rivet/internal/config"
	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "run [args...]",
		Short:              "Pass-through to the registered project CLI",
		Long:               "Forwards all arguments to the project CLI binary configured in .rivet/config.yaml.",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadOrDefault("")
			if err != nil {
				return err
			}

			if cfg.ProjectCLI.Command == "" {
				return fmt.Errorf("no project CLI registered\n\nGet started:\n  rivet project init-cli              Scaffold a starter project CLI\n  rivet project register-cli <path>   Register an existing CLI")
			}

			bin := cfg.ProjectCLI.Command

			// Verify the binary exists.
			if _, err := exec.LookPath(bin); err != nil {
				if _, statErr := os.Stat(bin); statErr != nil {
					return fmt.Errorf("project CLI not found: %s\n\nRe-register with: rivet project register-cli <path>", bin)
				}
			}

			child := exec.Command(bin, args...)
			child.Stdin = os.Stdin
			child.Stdout = os.Stdout
			child.Stderr = os.Stderr

			err = child.Run()
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					os.Exit(exitErr.ExitCode())
				}
				return fmt.Errorf("running project CLI: %w", err)
			}
			return nil
		},
	}

	return cmd
}
