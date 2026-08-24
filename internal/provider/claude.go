package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// claude is Claude Code. Its MCP registration is a per-project .mcp.json at the
// repo root, which is committable, so cloning a rivet project gives you the
// server for free.
type claude struct{}

// Claude returns the Claude Code provider.
func Claude() Provider { return claude{} }

func (claude) Name() string { return "claude" }

func (claude) InstructionFile() string { return "CLAUDE.md" }

func (claude) SkillsDir() string { return filepath.Join(".claude", "skills") }

func (claude) AgentsDir() string { return filepath.Join(".claude", "agents") }

func (claude) Detect(dir string) bool {
	return existsIn(dir, ".claude") || existsIn(dir, "CLAUDE.md")
}

// WriteMCPConfig adds the rivet server to .mcp.json, creating the file if
// needed. Non-destructive: it preserves existing servers and only adds or
// updates the "rivet" entry.
func (claude) WriteMCPConfig(dir string) (string, error) {
	type mcpServer struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	type mcpConfig struct {
		Servers map[string]mcpServer `json:"mcpServers"`
	}

	path := filepath.Join(dir, ".mcp.json")
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
