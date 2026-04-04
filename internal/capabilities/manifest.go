package capabilities

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Manifest is the capabilities.yaml file that declares project CLI capabilities
// with typed parameters. This is the primary way projects expose capabilities
// to Claude Code via MCP.
type Manifest struct {
	CLI          string       `yaml:"cli"`
	Capabilities []ManifestCap `yaml:"capabilities"`
}

// ManifestCap is a capability as declared in capabilities.yaml.
type ManifestCap struct {
	Name        string  `yaml:"name"`
	Description string  `yaml:"description"`
	Command     []string `yaml:"command"`     // subcommand args (appended to cli binary)
	Output      string  `yaml:"output"`       // json or text
	Safety      string  `yaml:"safety"`       // safe, guarded, dangerous
	Params      []Param `yaml:"params,omitempty"`
}

// DefaultManifestPath returns the standard location for the manifest.
func DefaultManifestPath() string {
	return filepath.Join(".rivet", "capabilities.yaml")
}

// LoadManifest reads and parses a capabilities.yaml file.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}

	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}
	return &m, nil
}

// LoadManifestOrNil reads the manifest if it exists, returns nil otherwise.
func LoadManifestOrNil(path string) *Manifest {
	m, err := LoadManifest(path)
	if err != nil {
		return nil
	}
	return m
}

// ToCapabilities converts manifest entries to runtime Capability values.
// The cli binary path is prepended to each capability's command.
func (m *Manifest) ToCapabilities() ([]Capability, error) {
	var caps []Capability
	for _, mc := range m.Capabilities {
		kind := KindProjectCommand

		safety, err := ParseSafetyLevel(mc.Safety)
		if err != nil {
			safety = SafetyLevelSafe
		}

		// Build full command: [cli, ...subcommand]
		cmd := make([]string, 0, 1+len(mc.Command))
		cmd = append(cmd, m.CLI)
		cmd = append(cmd, mc.Command...)

		caps = append(caps, Capability{
			Name:        mc.Name,
			Kind:        kind,
			Description: mc.Description,
			Command:     cmd,
			Output:      mc.Output,
			Safety:      safety,
			Params:      mc.Params,
		})
	}
	return caps, nil
}

// StarterManifest returns a capabilities.yaml for a newly scaffolded project CLI.
func StarterManifest(cliPath, cliName string) string {
	var b strings.Builder
	b.WriteString("# Project CLI capabilities exposed to Claude Code via Rivet's MCP server.\n")
	b.WriteString("# Each capability becomes an MCP tool with typed parameters.\n")
	b.WriteString("#\n")
	b.WriteString("# Param types: string, number, integer, boolean\n")
	b.WriteString("# Params with required: true become required tool inputs.\n")
	b.WriteString("# The flag field overrides the CLI flag name (default: --<name>).\n\n")

	b.WriteString(fmt.Sprintf("cli: %s\n\n", cliPath))
	b.WriteString("capabilities:\n")

	b.WriteString(fmt.Sprintf("  - name: %s.status\n", cliName))
	b.WriteString("    description: Show project status summary\n")
	b.WriteString("    command: [query, status]\n")
	b.WriteString("    output: json\n")
	b.WriteString("    safety: safe\n\n")

	b.WriteString(fmt.Sprintf("  - name: %s.health\n", cliName))
	b.WriteString("    description: Run health checks\n")
	b.WriteString("    command: [check, health]\n")
	b.WriteString("    output: json\n")
	b.WriteString("    safety: safe\n\n")

	b.WriteString(fmt.Sprintf("  - name: %s.seed\n", cliName))
	b.WriteString("    description: Seed development data\n")
	b.WriteString("    command: [task, seed]\n")
	b.WriteString("    output: json\n")
	b.WriteString("    safety: guarded\n")
	b.WriteString("    params:\n")
	b.WriteString("      - name: count\n")
	b.WriteString("        type: integer\n")
	b.WriteString("        description: Number of records to seed\n")

	return b.String()
}

// StarterManifestElixir returns a capabilities.yaml for Elixir Mix task scaffolding.
func StarterManifestElixir(cliName string) string {
	var b strings.Builder
	b.WriteString("# Project CLI capabilities exposed to Claude Code via Rivet's MCP server.\n")
	b.WriteString("# Each capability becomes an MCP tool with typed parameters.\n")
	b.WriteString("#\n")
	b.WriteString("# Param types: string, number, integer, boolean\n")
	b.WriteString("# Params with required: true become required tool inputs.\n")
	b.WriteString("# The flag field overrides the CLI flag name (default: --<name>).\n\n")

	b.WriteString("cli: mix\n\n")
	b.WriteString("capabilities:\n")

	b.WriteString(fmt.Sprintf("  - name: %s.status\n", cliName))
	b.WriteString("    description: Show project status summary\n")
	b.WriteString(fmt.Sprintf("    command: [\"%s.query.status\"]\n", cliName))
	b.WriteString("    output: json\n")
	b.WriteString("    safety: safe\n\n")

	b.WriteString(fmt.Sprintf("  - name: %s.health\n", cliName))
	b.WriteString("    description: Run health checks\n")
	b.WriteString(fmt.Sprintf("    command: [\"%s.check.health\"]\n", cliName))
	b.WriteString("    output: json\n")
	b.WriteString("    safety: safe\n\n")

	b.WriteString(fmt.Sprintf("  - name: %s.seed\n", cliName))
	b.WriteString("    description: Seed development data\n")
	b.WriteString(fmt.Sprintf("    command: [\"%s.task.seed\"]\n", cliName))
	b.WriteString("    output: json\n")
	b.WriteString("    safety: guarded\n")
	b.WriteString("    params:\n")
	b.WriteString("      - name: count\n")
	b.WriteString("        type: integer\n")
	b.WriteString("        description: Number of records to seed\n")

	return b.String()
}
