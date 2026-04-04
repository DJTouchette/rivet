package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Capabilities == nil {
		t.Fatal("default config should have non-nil capabilities slice")
	}
	if len(cfg.Capabilities) != 0 {
		t.Fatalf("expected 0 capabilities, got %d", len(cfg.Capabilities))
	}
	if cfg.ProjectCLI.Command != "" {
		t.Fatalf("expected empty project CLI command, got %q", cfg.ProjectCLI.Command)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	content := []byte(`project_cli:
  command: "./bin/mycli"
capabilities:
  - name: "test.cmd"
    kind: "project_command"
    description: "A test command"
    command: ["./bin/mycli", "test"]
    output: "json"
    safety: "safe"
  - name: "deploy"
    kind: "tool"
    description: "Deploy the app"
    command: ["deploy.sh"]
    output: "text"
    safety: "dangerous"
    requires_approval: true
`)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadFrom(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ProjectCLI.Command != "./bin/mycli" {
		t.Fatalf("expected project CLI command %q, got %q", "./bin/mycli", cfg.ProjectCLI.Command)
	}
	if len(cfg.Capabilities) != 2 {
		t.Fatalf("expected 2 capabilities, got %d", len(cfg.Capabilities))
	}

	c := cfg.Capabilities[0]
	if c.Name != "test.cmd" {
		t.Errorf("expected name %q, got %q", "test.cmd", c.Name)
	}
	if c.Kind != "project_command" {
		t.Errorf("expected kind %q, got %q", "project_command", c.Kind)
	}
	if c.Safety != "safe" {
		t.Errorf("expected safety %q, got %q", "safe", c.Safety)
	}
	if len(c.Command) != 2 || c.Command[0] != "./bin/mycli" {
		t.Errorf("unexpected command: %v", c.Command)
	}

	d := cfg.Capabilities[1]
	if !d.RequiresApproval {
		t.Error("expected requires_approval to be true")
	}
	if cfg.Path() != path {
		t.Errorf("expected path %q, got %q", path, cfg.Path())
	}
}

func TestLoadOrDefaultFindsRivetDir(t *testing.T) {
	dir := t.TempDir()
	rivetDir := filepath.Join(dir, ".rivet")
	if err := os.MkdirAll(rivetDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := []byte(`capabilities:
  - name: "found"
    kind: "tool"
    safety: "safe"
`)
	if err := os.WriteFile(filepath.Join(rivetDir, "config.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}

	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(dir)

	cfg, err := LoadOrDefault("")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Capabilities) != 1 {
		t.Fatalf("expected 1 capability, got %d", len(cfg.Capabilities))
	}
	if cfg.Capabilities[0].Name != "found" {
		t.Errorf("expected capability name %q, got %q", "found", cfg.Capabilities[0].Name)
	}
}

func TestLoadOrDefaultReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(dir)

	cfg, err := LoadOrDefault("")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Capabilities) != 0 {
		t.Fatalf("expected 0 capabilities, got %d", len(cfg.Capabilities))
	}
}

func TestWriteAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".rivet", "config.yaml")

	cfg := &Config{
		ProjectCLI: ProjectCLIConfig{Command: "./bin/test"},
		Capabilities: []CapabilityDef{
			{Name: "alpha", Kind: "tool", Safety: "safe", Description: "Alpha tool"},
		},
		path: path,
	}
	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}

	loaded, err := loadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ProjectCLI.Command != "./bin/test" {
		t.Errorf("expected %q, got %q", "./bin/test", loaded.ProjectCLI.Command)
	}
	if len(loaded.Capabilities) != 1 {
		t.Fatalf("expected 1 capability, got %d", len(loaded.Capabilities))
	}
	if loaded.Capabilities[0].Name != "alpha" {
		t.Errorf("expected %q, got %q", "alpha", loaded.Capabilities[0].Name)
	}
}

func TestStarterConfigYAML(t *testing.T) {
	data := StarterConfigYAML()
	if len(data) == 0 {
		t.Fatal("starter config should not be empty")
	}

	// Should parse as valid YAML
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("starter config should be valid YAML: %v", err)
	}
	if len(cfg.Capabilities) != 0 {
		t.Fatalf("starter config should have 0 capabilities, got %d", len(cfg.Capabilities))
	}
}
