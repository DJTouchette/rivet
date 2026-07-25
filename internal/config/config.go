package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the top-level rivet configuration, parsed from .rivet/config.yaml.
type Config struct {
	ProjectCLI   ProjectCLIConfig `yaml:"project_cli,omitempty"`
	Context      ContextConfig    `yaml:"context,omitempty"`
	Tools        ToolsConfig      `yaml:"tools,omitempty"`
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

	// WikiPaths are extra roots (directories or globs like "docs/**" or
	// "../project.wiki/**") indexed as wiki reference docs, in addition to the
	// native .rivet/wiki/ tree. Useful for pointing rivet at a checked-out
	// Azure DevOps wiki or an existing docs/ tree. The team is responsible for
	// keeping those checkouts current; rivet only reads the markdown.
	WikiPaths []string `yaml:"wiki_paths,omitempty"`
}

// ShouldAutoCompact returns whether auto-compaction nudges are enabled.
// Defaults to true if not set.
func (c ContextConfig) ShouldAutoCompact() bool {
	if c.AutoCompact == nil {
		return true
	}
	return *c.AutoCompact
}

// ToolsConfig is the escape hatch for built-in tool groups that are otherwise
// registered only when rivet can detect the feature is in use. Each field is
// tri-state — unset means auto-detect, true forces the group on, false forces
// it off. Detection reads project state (a configured database, an existing
// vault), and a user may legitimately want the tools before that state exists,
// or never want them at all.
type ToolsConfig struct {
	Schema *bool `yaml:"schema,omitempty"`
	Vaulty *bool `yaml:"vaulty,omitempty"`
}

// SchemaEnabled resolves the schema.* group against the auto-detected signal.
func (t ToolsConfig) SchemaEnabled(detected bool) bool {
	return resolveToolGroup(t.Schema, detected)
}

// VaultyEnabled resolves the vaulty.* group against the auto-detected signal.
func (t ToolsConfig) VaultyEnabled(detected bool) bool {
	return resolveToolGroup(t.Vaulty, detected)
}

func resolveToolGroup(override *bool, detected bool) bool {
	if override == nil {
		return detected
	}
	return *override
}

// ProjectCLIConfig describes the project-local CLI binary.
type ProjectCLIConfig struct {
	Command string `yaml:"command,omitempty"`
}

// PolicyDef is a policy rule as declared in config.yaml.
type PolicyDef struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description,omitempty"`
	Match       PolicyMatchDef `yaml:"match"`
	RequireEnv  []string       `yaml:"require_env,omitempty"`
	DenyEnv     []string       `yaml:"deny_env,omitempty"`
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

// LoadProject loads the project's own config, ignoring the user-level fallback,
// and defaults to .rivet/config.yaml when the project has none.
//
// Any command that WRITES config must use this rather than LoadOrDefault.
// LoadOrDefault resolves to ~/.config/rivet/config.yaml when a project has no
// config of its own, and cfg.path follows whatever it loaded — so writing
// through it edits the user's global config on behalf of a single project.
func LoadProject() (*Config, error) {
	path := filepath.Join(".rivet", "config.yaml")
	if _, err := os.Stat(path); err == nil {
		return loadFrom(path)
	}

	cfg := defaultConfig()
	cfg.path = path
	return cfg, nil
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

// Write saves the config to its path, updating only the keys this struct models
// and leaving everything else in the file untouched.
//
// It used to marshal the struct straight over the file, which destroyed data.
// Config has no Schema field — the schema: section is owned by
// internal/schema/config — so a single `rivet project register-cli` silently
// deleted a user's database credentials and migrations config along with every
// comment in the file. Now that schema tooling is registered based on that
// section existing, it would also have made twelve MCP tools disappear.
func (c *Config) Write() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	existing, err := os.ReadFile(c.path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading config: %w", err)
	}

	data, err := c.mergeInto(existing)
	if err != nil {
		return err
	}

	if err := os.WriteFile(c.path, data, 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// mergeInto renders the config over an existing YAML document, replacing the
// values of keys the struct models and preserving unknown keys, key order, and
// comments. An absent or unparseable document is rendered from scratch.
func (c *Config) mergeInto(existing []byte) ([]byte, error) {
	own, err := c.ownKeys()
	if err != nil {
		return nil, err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(existing, &doc); err != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		// Nothing usable to merge into — a fresh file, or something we'd rather
		// not half-rewrite. Render the struct alone.
		return encodeYAML(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{own}})
	}

	root := doc.Content[0]
	for i := 0; i+1 < len(own.Content); i += 2 {
		setMappingValue(root, own.Content[i], own.Content[i+1])
	}

	return encodeYAML(&doc)
}

// ownKeys renders the struct to a mapping node, so its keys can be merged
// individually rather than as one wholesale document.
func (c *Config) ownKeys() (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(c); err != nil {
		return nil, fmt.Errorf("marshaling config: %w", err)
	}
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("marshaling config: expected a mapping, got kind %d", n.Kind)
	}
	return &n, nil
}

// setMappingValue replaces the value for a key, or appends the pair when the key
// is absent. The existing key node is kept rather than overwritten, because it
// carries any comment the user wrote above that section.
func setMappingValue(mapping, key, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key.Value {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content, key, value)
}

// encodeYAML renders a node at two-space indentation, matching the starter
// config and ordinary YAML convention rather than yaml.Marshal's four.
func encodeYAML(node *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return nil, fmt.Errorf("marshaling config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("marshaling config: %w", err)
	}
	return buf.Bytes(), nil
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
# wiki_paths:   extra markdown roots indexed as wiki reference docs, in addition
#               to .rivet/wiki/. Point at an existing docs/ tree or a checked-out
#               Azure DevOps wiki (itself a git repo of markdown). Your team keeps
#               those checkouts current; rivet only reads them.
# context:
#   auto_compact: false
#   wiki_paths:
#     - "../project.wiki/**"
#     - "docs/**"

# Tools — which built-in tool groups Rivet exposes over MCP.
# recon.* and witness.* are always on. schema.* and vaulty.* are registered only
# when Rivet detects you use them (a schema: section below / an existing vault),
# so a project that uses neither spends no context window on their definitions.
# Set either to true to force it on, or false to force it off.
# tools:
#   schema: true
#   vaulty: false

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

# Schema — database intelligence.
# Configure one or more read-only database connections. All queries Rivet makes
# target system catalogs and stats views — never application data.
#
# Environment variables in user/password/dsn are expanded at runtime, so you
# can reference secrets from your shell or vaulty instead of committing them.
#
# Example:
# schema:
#   databases:
#     - name: prod
#       engine: postgres            # postgres | mssql
#       host: db.example.com
#       port: 5432
#       user: readonly
#       password: ${SCHEMA_PW}      # expanded from env
#       database: production
#       sslmode: require            # postgres only
#       default: true
#     - name: prod-mssql
#       engine: mssql
#       host: sql.example.com
#       port: 1433
#       user: ${MSSQL_USER}
#       password: ${MSSQL_PW}
#       database: app_prod
#   migrations:
#     dir: ./db/migrations          # or dirs: [...] for multiple roots
#     dialect: postgres             # optional hint
#   code_scan:
#     roots: [./src, ./backend]     # where to look for SQL-bearing source
#     languages: [csharp, go]       # optional filter
#     exclude: ["**/node_modules/**", "**/bin/**"]
#   cache:
#     max_age: 24h                  # re-read the catalog once a snapshot is
#                                   # older than this. 0s never uses the cache.
#                                   # Every command prints the snapshot's age.

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
