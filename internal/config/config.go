package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the top-level rivet configuration, parsed from .rivet/config.yaml.
type Config struct {
	ProjectCLI   ProjectCLIConfig `yaml:"project_cli,omitempty"`
	Context      ContextConfig    `yaml:"context,omitempty"`
	Capabilities []CapabilityDef  `yaml:"capabilities,omitempty"`
	Policies     []PolicyDef      `yaml:"policies,omitempty"`

	path string
}

// ContextConfig controls how the context system behaves.
type ContextConfig struct {
	// AutoCompact controls whether the MCP server nudges Claude to
	// consolidate context docs when they get long. When false, learnings
	// accumulate until someone explicitly runs /rivet-compact-context
	// (e.g. after merging branches). Default: true.
	AutoCompact *bool `yaml:"auto_compact,omitempty"`
}

// ShouldAutoCompact returns whether auto-compaction nudges are enabled.
// Defaults to true if not set.
func (c ContextConfig) ShouldAutoCompact() bool {
	if c.AutoCompact == nil {
		return true
	}
	return *c.AutoCompact
}

// ProjectCLIConfig describes the project-local CLI binary.
type ProjectCLIConfig struct {
	Command string `yaml:"command,omitempty"`
}

// PolicyDef is a policy rule as declared in config.yaml.
type PolicyDef struct {
	Name        string          `yaml:"name"`
	Description string          `yaml:"description,omitempty"`
	Match       PolicyMatchDef  `yaml:"match"`
	RequireEnv  []string        `yaml:"require_env,omitempty"`
	DenyEnv     []string        `yaml:"deny_env,omitempty"`
}

// PolicyMatchDef describes which capabilities a policy rule applies to.
type PolicyMatchDef struct {
	Safety       string   `yaml:"safety,omitempty"`
	Kind         string   `yaml:"kind,omitempty"`
	Capabilities []string `yaml:"capabilities,omitempty"`
}

// CapabilityDef is a capability as declared in config.yaml.
type CapabilityDef struct {
	Name             string   `yaml:"name"`
	Kind             string   `yaml:"kind"`
	Description      string   `yaml:"description,omitempty"`
	Command          []string `yaml:"command,omitempty"`
	Output           string   `yaml:"output,omitempty"`
	Safety           string   `yaml:"safety,omitempty"`
	RequiresApproval bool     `yaml:"requires_approval,omitempty"`
}

// LoadOrDefault loads config from the given path, or searches common locations.
// Search order: .rivet/config.yaml, then ~/.config/rivet/config.yaml.
// Returns a default config if no file is found.
func LoadOrDefault(path string) (*Config, error) {
	if path != "" {
		return loadFrom(path)
	}

	dirs := []string{".rivet"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".config", "rivet"))
	}

	for _, dir := range dirs {
		p := filepath.Join(dir, "config.yaml")
		if _, err := os.Stat(p); err == nil {
			return loadFrom(p)
		}
	}

	cfg := defaultConfig()
	cfg.path = filepath.Join(".rivet", "config.yaml")
	return cfg, nil
}

// Write saves the config to its path.
func (c *Config) Write() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(c.path, data, 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// Path returns the file path this config was loaded from or will be written to.
func (c *Config) Path() string {
	return c.path
}

func defaultConfig() *Config {
	return &Config{
		Capabilities: []CapabilityDef{},
	}
}

func loadFrom(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	cfg.path = path
	return cfg, nil
}

// StarterConfigYAML returns a hand-crafted starter config.yaml with comments.
func StarterConfigYAML() []byte {
	return []byte(`# Rivet configuration
# See: https://github.com/djtouchette/rivet

# Context — controls how the context system behaves.
# auto_compact: true (default) — nudge Claude to consolidate docs when they get long.
#               false — let learnings accumulate and compact manually (e.g. after merges).
#               Set to false for team workflows where branches add learnings independently
#               and compaction happens on main after merge.
# context:
#   auto_compact: false

# Project CLI — the repo-local CLI that exposes project-specific operations.
# Uncomment and set the path to your project CLI binary.
# project_cli:
#   command: "./bin/projectcli"

# Capabilities — project-specific tools that Rivet exposes to Claude Code via MCP.
# Each capability has a name, kind, description, command, output format, and safety level.
#
# Safety levels:
#   safe      — read-only, low-risk, can run automatically
#   guarded   — potentially meaningful, may require checks
#   dangerous — can mutate important state, requires explicit approval
#
# Capability kinds:
#   project_command — a command from your project CLI
#   tool            — a standalone tool (e.g., vaulty)
#   mcp             — an external MCP server
#   workflow        — a multi-step workflow
#
# Example:
# capabilities:
#   - name: "db.patient-summary"
#     kind: "project_command"
#     description: "Read-only patient summary report"
#     command: ["./bin/projectcli", "db", "patient-summary"]
#     output: "json"
#     safety: "safe"
#
#   - name: "vaulty"
#     kind: "tool"
#     description: "Secret access and command brokering"
#     command: ["vaulty"]
#     output: "json"
#     safety: "guarded"

capabilities: []

# Policies — rules that gate capability execution based on environment.
# Each policy has a match (which capabilities it applies to) and constraints.
#
# Match fields (all optional, AND logic when multiple are set):
#   safety       — match by safety level (safe, guarded, dangerous)
#   kind         — match by capability kind
#   capabilities — match by specific capability names
#
# Constraints:
#   require_env — env vars that must be set (non-empty) before execution
#   deny_env    — env vars that must NOT be set during execution
#
# Example:
# policies:
#   - name: "no-dangerous-in-ci"
#     description: "Block dangerous capabilities in CI environments"
#     match:
#       safety: "dangerous"
#     deny_env: ["CI"]
#
#   - name: "prod-gate"
#     description: "Require explicit prod approval for migrations"
#     match:
#       capabilities: ["db.migrate", "search.reindex"]
#     require_env: ["PROD_APPROVED"]

policies: []
`)
}
