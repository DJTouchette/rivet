package capabilities

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "capabilities.yaml")

	content := `cli: ./bin/myapp

capabilities:
  - name: myapp.status
    description: Show status
    command: [query, status]
    output: json
    safety: safe

  - name: myapp.seed
    description: Seed data
    command: [task, seed]
    output: json
    safety: guarded
    params:
      - name: count
        type: integer
        description: Number of records
        required: true
      - name: env
        type: string
        description: Target environment
        enum: [dev, staging]
        default: dev
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest() error: %v", err)
	}

	if m.CLI != "./bin/myapp" {
		t.Errorf("CLI = %q, want ./bin/myapp", m.CLI)
	}
	if len(m.Capabilities) != 2 {
		t.Fatalf("got %d capabilities, want 2", len(m.Capabilities))
	}

	// Check status capability (no params).
	status := m.Capabilities[0]
	if status.Name != "myapp.status" {
		t.Errorf("Name = %q", status.Name)
	}
	if len(status.Params) != 0 {
		t.Errorf("status should have 0 params, got %d", len(status.Params))
	}

	// Check seed capability (with params).
	seed := m.Capabilities[1]
	if len(seed.Params) != 2 {
		t.Fatalf("seed should have 2 params, got %d", len(seed.Params))
	}
	if seed.Params[0].Name != "count" {
		t.Errorf("first param = %q, want count", seed.Params[0].Name)
	}
	if seed.Params[0].Type != "integer" {
		t.Errorf("count type = %q, want integer", seed.Params[0].Type)
	}
	if !seed.Params[0].Required {
		t.Error("count should be required")
	}
	if seed.Params[1].Default != "dev" {
		t.Errorf("env default = %q, want dev", seed.Params[1].Default)
	}
	if len(seed.Params[1].Enum) != 2 {
		t.Errorf("env enum = %v, want [dev, staging]", seed.Params[1].Enum)
	}
}

func TestToCapabilities(t *testing.T) {
	m := &Manifest{
		CLI: "./bin/myapp",
		Capabilities: []ManifestCap{
			{
				Name:        "myapp.status",
				Description: "Show status",
				Command:     []string{"query", "status"},
				Output:      "json",
				Safety:      "safe",
			},
			{
				Name:        "myapp.seed",
				Description: "Seed data",
				Command:     []string{"task", "seed"},
				Output:      "json",
				Safety:      "guarded",
				Params: []Param{
					{Name: "count", Type: "integer", Description: "Number of records", Required: true},
				},
			},
		},
	}

	caps, err := m.ToCapabilities()
	if err != nil {
		t.Fatalf("ToCapabilities() error: %v", err)
	}

	if len(caps) != 2 {
		t.Fatalf("got %d caps, want 2", len(caps))
	}

	// Command should be [cli, subcommand...]
	if caps[0].Command[0] != "./bin/myapp" {
		t.Errorf("command[0] = %q, want ./bin/myapp", caps[0].Command[0])
	}
	if caps[0].Command[1] != "query" || caps[0].Command[2] != "status" {
		t.Errorf("command = %v", caps[0].Command)
	}

	// Params should be carried through.
	if len(caps[1].Params) != 1 {
		t.Fatalf("seed params = %d, want 1", len(caps[1].Params))
	}
	if caps[1].Params[0].Name != "count" {
		t.Errorf("param name = %q", caps[1].Params[0].Name)
	}

	// Kind should default to project_command.
	if caps[0].Kind != KindProjectCommand {
		t.Errorf("kind = %q, want project_command", caps[0].Kind)
	}
}

func TestParamFlagName(t *testing.T) {
	tests := []struct {
		param Param
		want  string
	}{
		{Param{Name: "count"}, "--count"},
		{Param{Name: "count", Flag: "-n"}, "-n"},
		{Param{Name: "output-format"}, "--output-format"},
	}
	for _, tt := range tests {
		got := tt.param.FlagName()
		if got != tt.want {
			t.Errorf("Param{Name: %q, Flag: %q}.FlagName() = %q, want %q",
				tt.param.Name, tt.param.Flag, got, tt.want)
		}
	}
}

func TestLoadManifestOrNilMissing(t *testing.T) {
	m := LoadManifestOrNil("/nonexistent/path")
	if m != nil {
		t.Error("should return nil for missing file")
	}
}

func TestStarterManifest(t *testing.T) {
	content := StarterManifest("./tools/myapp/myapp", "myapp")
	if content == "" {
		t.Fatal("StarterManifest returned empty string")
	}

	// Should reference the CLI path and capabilities.
	for _, want := range []string{"./tools/myapp/myapp", "myapp.status", "myapp.health", "myapp.seed", "params:"} {
		if !contains(content, want) {
			t.Errorf("StarterManifest missing %q", want)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
