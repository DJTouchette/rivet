package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestToolsConfigResolution(t *testing.T) {
	on, off := true, false

	tests := []struct {
		name     string
		tools    ToolsConfig
		detected bool
		wantSch  bool
		wantVlt  bool
	}{
		{"unset falls back to detection (found)", ToolsConfig{}, true, true, true},
		{"unset falls back to detection (missing)", ToolsConfig{}, false, false, false},
		{"force on without signal", ToolsConfig{Schema: &on, Vaulty: &on}, false, true, true},
		{"force off despite signal", ToolsConfig{Schema: &off, Vaulty: &off}, true, false, false},
		{"groups resolve independently", ToolsConfig{Schema: &on}, false, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tools.SchemaEnabled(tt.detected); got != tt.wantSch {
				t.Errorf("SchemaEnabled(%v) = %v, want %v", tt.detected, got, tt.wantSch)
			}
			if got := tt.tools.VaultyEnabled(tt.detected); got != tt.wantVlt {
				t.Errorf("VaultyEnabled(%v) = %v, want %v", tt.detected, got, tt.wantVlt)
			}
		})
	}
}

func TestToolsConfigParsedFromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("tools:\n  schema: true\n  vaulty: false\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.Schema == nil || !*cfg.Tools.Schema {
		t.Errorf("expected tools.schema true, got %v", cfg.Tools.Schema)
	}
	if cfg.Tools.Vaulty == nil || *cfg.Tools.Vaulty {
		t.Errorf("expected tools.vaulty false, got %v", cfg.Tools.Vaulty)
	}

	// An absent tools: section must stay nil so auto-detection wins.
	bare, err := loadFrom(writeTempConfig(t, "capabilities: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if bare.Tools.Schema != nil || bare.Tools.Vaulty != nil {
		t.Errorf("expected unset tool overrides, got %v / %v", bare.Tools.Schema, bare.Tools.Vaulty)
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Write used to marshal the struct straight over the file. Config has no Schema
// field — that section belongs to internal/schema/config — so registering a
// project CLI silently deleted a user's database credentials, and with schema
// tooling now gated on that section, twelve MCP tools with them.
func TestWritePreservesUnmodelledSections(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".rivet", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	original := `# Keep this comment.
context:
  wiki_paths:
    - "docs/**"

# And this one, above a section Config does not model.
schema:
  databases:
    - name: prod
      engine: postgres
      password: ${SCHEMA_PW}
  migrations:
    dir: ./db/migrations
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadOrDefault(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.ProjectCLI.Command = "./mycli"
	if err := cfg.Write(); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	text := string(got)

	for _, want := range []string{
		"schema:",      // the whole section survives
		"name: prod",   // ...including its contents
		"${SCHEMA_PW}", // ...and unexpanded env references
		"dir: ./db/migrations",
		"# Keep this comment.",
		"# And this one",
		"command: ./mycli", // and the update actually landed
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q after Write:\n%s", want, text)
		}
	}
}

// The modelled keys must actually be updated, not merely left alone.
func TestWriteUpdatesModelledKeysInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("project_cli:\n  command: ./old\nschema:\n  keep: me\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadOrDefault(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.ProjectCLI.Command = "./new"
	if err := cfg.Write(); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got, _ := os.ReadFile(path)
	text := string(got)
	if !strings.Contains(text, "./new") {
		t.Errorf("update did not land:\n%s", text)
	}
	if strings.Contains(text, "./old") {
		t.Errorf("stale value survived:\n%s", text)
	}
	if !strings.Contains(text, "keep: me") {
		t.Errorf("unmodelled key lost:\n%s", text)
	}
	// The key must be replaced, not duplicated.
	if n := strings.Count(text, "project_cli:"); n != 1 {
		t.Errorf("project_cli appears %d times, want 1:\n%s", n, text)
	}
}

// A brand-new project has no file to merge into; that path must still work.
func TestWriteCreatesFreshFile(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{path: filepath.Join(dir, ".rivet", "config.yaml")}
	cfg.ProjectCLI.Command = "./mycli"

	if err := cfg.Write(); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := os.ReadFile(cfg.path)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if !strings.Contains(string(got), "command: ./mycli") {
		t.Errorf("fresh file missing content:\n%s", got)
	}
}

// Round-tripping must not corrupt the file: a second Write with no changes
// should leave it byte-identical.
func TestWriteIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("# c\nproject_cli:\n  command: ./x\nschema:\n  keep: me\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	first := func() string {
		cfg, err := LoadOrDefault(path)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if err := cfg.Write(); err != nil {
			t.Fatalf("write: %v", err)
		}
		b, _ := os.ReadFile(path)
		return string(b)
	}

	a := first()
	b := first()
	if a != b {
		t.Errorf("Write is not idempotent:\nfirst:\n%s\nsecond:\n%s", a, b)
	}
}

// LoadOrDefault resolves to ~/.config/rivet/config.yaml when a project has no
// config of its own, and cfg.path follows whatever it loaded. Writing through
// that meant `rivet project register-cli` in a fresh project edited the user's
// global config on that project's behalf. LoadProject never leaves the project.
func TestLoadProjectIgnoresUserLevelConfig(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "rivet"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	globalPath := filepath.Join(home, ".config", "rivet", "config.yaml")
	if err := os.WriteFile(globalPath, []byte("project_cli:\n  command: ./global\n"), 0644); err != nil {
		t.Fatalf("write global: %v", err)
	}

	t.Setenv("HOME", home)
	t.Chdir(t.TempDir()) // a project with no .rivet/

	cfg, err := LoadProject()
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}

	// It must target the project, so a later Write can't escape it.
	if want := filepath.Join(".rivet", "config.yaml"); cfg.path != want {
		t.Errorf("path = %q, want %q", cfg.path, want)
	}
	if cfg.ProjectCLI.Command == "./global" {
		t.Error("LoadProject picked up the user-level config")
	}

	cfg.ProjectCLI.Command = "./local"
	if err := cfg.Write(); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The global file must be byte-for-byte untouched.
	got, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("reread global: %v", err)
	}
	if string(got) != "project_cli:\n  command: ./global\n" {
		t.Errorf("global config was modified:\n%s", got)
	}
}

// When the project does have a config, LoadProject uses it.
func TestLoadProjectUsesProjectConfigWhenPresent(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(".rivet", 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".rivet", "config.yaml"), []byte("project_cli:\n  command: ./local\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadProject()
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	if cfg.ProjectCLI.Command != "./local" {
		t.Errorf("command = %q, want ./local", cfg.ProjectCLI.Command)
	}
}
