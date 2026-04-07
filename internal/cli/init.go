package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/djtouchette/rivet/internal/config"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var force bool

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

			actions, err := ensureProjectSetup(force)
			if err != nil {
				return err
			}

			// Summary.
			fmt.Println("Rivet initialized:")
			for _, a := range actions {
				fmt.Printf("  + %s\n", a)
			}
			fmt.Println()
			fmt.Println("Next steps:")
			fmt.Println("  /rivet-setup             Run in Claude Code to scaffold + fill context docs automatically")
			fmt.Println("  /agents                  Confirm rivet-explorer and rivet-investigator are available")
			fmt.Println()
			fmt.Println("Or manually:")
			fmt.Println("  rivet context scaffold   Generate starter context docs from your codebase")
			fmt.Println("  /rivet-fill-context      Run in Claude Code to fill out context docs using recon")
			fmt.Println("  rivet update             Add any missing Rivet files without overwriting config.yaml")
			fmt.Println("  rivet sync               Update CLAUDE.md with rivet capabilities")

			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing config.yaml")
	return cmd
}

// ensureMCPConfig adds the rivet server to .mcp.json, creating the file if needed.
// Non-destructive: preserves existing servers and only adds/updates the "rivet" entry.
func ensureMCPConfig(path string) (string, error) {
	type mcpServer struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	type mcpConfig struct {
		Servers map[string]mcpServer `json:"mcpServers"`
	}

	cfg := mcpConfig{Servers: make(map[string]mcpServer)}

	// Load existing config if present.
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return "", fmt.Errorf("parsing %s: %w", path, err)
		}
		if cfg.Servers == nil {
			cfg.Servers = make(map[string]mcpServer)
		}
	}

	// Check if rivet is already configured.
	if existing, ok := cfg.Servers["rivet"]; ok {
		if existing.Command == "rivet" {
			return fmt.Sprintf("%s already has rivet MCP server", path), nil
		}
	}

	// Add rivet server.
	cfg.Servers["rivet"] = mcpServer{
		Command: "rivet",
		Args:    []string{"serve"},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling MCP config: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}

	return fmt.Sprintf("added rivet MCP server to %s", path), nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ensureProjectSetup(force bool) ([]string, error) {
	rivetDir := ".rivet"
	var actions []string

	dirs := []string{
		rivetDir,
		filepath.Join(rivetDir, "context", "domains"),
		filepath.Join(rivetDir, "context", "modules"),
		filepath.Join(rivetDir, "context", "paradigms"),
		filepath.Join(rivetDir, "capabilities"),
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

	mcpAction, err := ensureMCPConfig(".mcp.json")
	if err != nil {
		return nil, fmt.Errorf("configuring MCP: %w", err)
	}
	actions = append(actions, mcpAction)

	skillActions, err := ensureSkills()
	if err != nil {
		return nil, fmt.Errorf("installing skills: %w", err)
	}
	actions = append(actions, skillActions...)

	agentActions, err := ensureAgents()
	if err != nil {
		return nil, fmt.Errorf("installing agents: %w", err)
	}
	actions = append(actions, agentActions...)

	hookAction, err := ensureHooks()
	if err != nil {
		return nil, fmt.Errorf("installing hooks: %w", err)
	}
	actions = append(actions, hookAction)

	return actions, nil
}
