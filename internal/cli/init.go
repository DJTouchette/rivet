package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/djtouchette/rivet/internal/config"
	"github.com/djtouchette/rivet/internal/provider"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var force bool
	var providerSpec string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize Rivet in the current project",
		Long:  "Creates the .rivet/ directory structure, starter config.yaml, and MCP config.",
		RunE: func(cmd *cobra.Command, args []string) error {
			rivetDir := ".rivet"

			if info, err := os.Stat(rivetDir); err == nil && info.IsDir() {
				if !force {
					return fmt.Errorf(".rivet/ already exists (use 'rivet update' to add missing files, or --force to overwrite config.yaml)")
				}
			}

			providers, err := provider.Resolve(providerSpec, ".")
			if err != nil {
				return err
			}

			actions, err := ensureProjectSetup(force, providers)
			if err != nil {
				return err
			}

			// Summary.
			fmt.Println("Rivet initialized:")
			for _, a := range actions {
				fmt.Printf("  + %s\n", a)
			}
			fmt.Println()
			printInitNextSteps(providers)

			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing config.yaml")
	cmd.Flags().StringVar(&providerSpec, "provider", "auto", providerFlagUsage)
	return cmd
}

// providerFlagUsage is shared by init, update and sync so the three commands
// describe the flag the same way.
var providerFlagUsage = "agent harness to write artifacts for: " +
	strings.Join(provider.Names(), ", ") + ", both, or auto (detect, defaulting to claude)"

func printInitNextSteps(providers []provider.Provider) {
	claude := hasProvider(providers, provider.Claude().Name())

	fmt.Println("Next steps:")
	if claude {
		fmt.Println("  /rivet-setup             Run in Claude Code to scaffold + fill context docs automatically")
		fmt.Println("  /agents                  Confirm rivet-explorer and rivet-investigator are available")
		fmt.Println()
		fmt.Println("Or manually:")
	}
	fmt.Println("  rivet context scaffold   Generate starter context docs from your codebase")
	if claude {
		fmt.Println("  /rivet-fill-context      Run in Claude Code to fill out context docs using recon")
	}
	fmt.Println("  rivet update             Add any missing Rivet files without overwriting config.yaml")
	fmt.Printf("  rivet sync               Update %s with rivet capabilities\n", instructionFileList(providers))
}

func hasProvider(providers []provider.Provider, name string) bool {
	for _, p := range providers {
		if p.Name() == name {
			return true
		}
	}
	return false
}

func instructionFileList(providers []provider.Provider) string {
	var files []string
	for _, p := range providers {
		files = append(files, p.InstructionFile())
	}
	return strings.Join(files, " and ")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ensureProjectSetup(force bool, providers []provider.Provider) ([]string, error) {
	rivetDir := ".rivet"
	var actions []string

	dirs := []string{
		rivetDir,
		filepath.Join(rivetDir, "context", "domains"),
		filepath.Join(rivetDir, "context", "modules"),
		filepath.Join(rivetDir, "context", "paradigms"),
		filepath.Join(rivetDir, "wiki"),
		filepath.Join(rivetDir, "runbooks"),
		filepath.Join(rivetDir, "capabilities"),
		// rivet.learn creates this lazily, but scaffolding it up front makes
		// the learning log discoverable before the first entry exists.
		filepath.Join(rivetDir, "learnings"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return nil, fmt.Errorf("creating %s: %w", d, err)
		}
	}
	actions = append(actions, "created .rivet/ directory structure")

	configPath := filepath.Join(rivetDir, "config.yaml")
	if force || !fileExists(configPath) {
		if err := os.WriteFile(configPath, config.StarterConfigYAML(), 0644); err != nil {
			return nil, fmt.Errorf("writing config.yaml: %w", err)
		}
		actions = append(actions, "wrote .rivet/config.yaml")
	} else {
		actions = append(actions, ".rivet/config.yaml already exists, skipped")
	}

	for _, p := range providers {
		mcpAction, err := p.WriteMCPConfig(".")
		if err != nil {
			return nil, fmt.Errorf("configuring MCP for %s: %w", p.Name(), err)
		}
		actions = append(actions, mcpAction)

		skillActions, err := ensureSkills(p)
		if err != nil {
			return nil, fmt.Errorf("installing %s skills: %w", p.Name(), err)
		}
		actions = append(actions, skillActions...)

		agentActions, err := ensureAgents(p)
		if err != nil {
			return nil, fmt.Errorf("installing %s agents: %w", p.Name(), err)
		}
		actions = append(actions, agentActions...)
	}

	// Nudging lives in the MCP server now; clear out the bash hooks that
	// older versions installed so they don't double-nudge.
	hookAction, err := removeLegacyHooks()
	if err != nil {
		return nil, fmt.Errorf("removing legacy hooks: %w", err)
	}
	actions = append(actions, hookAction)

	runbookActions, err := ensureRunbooks()
	if err != nil {
		return nil, fmt.Errorf("installing runbooks: %w", err)
	}
	actions = append(actions, runbookActions...)

	return actions, nil
}
