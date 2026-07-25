package projectcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldElixirCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	result, err := ScaffoldElixir(dir, "project")
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Files) != 4 {
		t.Fatalf("expected 4 files, got %d: %v", len(result.Files), result.Files)
	}

	expectedFiles := []string{
		"lib/mix/tasks/project/query/status.ex",
		"lib/mix/tasks/project/check/health.ex",
		"lib/mix/tasks/project/task/seed.ex",
		"lib/mix/tasks/project/rivet_discover.ex",
	}

	for _, expected := range expectedFiles {
		path := filepath.Join(dir, expected)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file %s to exist", expected)
		}
	}
}

func TestScaffoldElixirSkipsExisting(t *testing.T) {
	dir := t.TempDir()

	// Create one file first.
	statusDir := filepath.Join(dir, "lib", "mix", "tasks", "project", "query")
	os.MkdirAll(statusDir, 0755)
	os.WriteFile(filepath.Join(statusDir, "status.ex"), []byte("existing"), 0644)

	result, err := ScaffoldElixir(dir, "project")
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Skipped) != 1 {
		t.Errorf("expected 1 skipped, got %d", len(result.Skipped))
	}
	if len(result.Files) != 3 {
		t.Errorf("expected 3 new files, got %d", len(result.Files))
	}
}

func TestScaffoldElixirModuleNames(t *testing.T) {
	dir := t.TempDir()
	_, err := ScaffoldElixir(dir, "project")
	if err != nil {
		t.Fatal(err)
	}

	// Check that the status task has the right module name.
	data, _ := os.ReadFile(filepath.Join(dir, "lib", "mix", "tasks", "project", "query", "status.ex"))
	content := string(data)
	if !strings.Contains(content, "Mix.Tasks.Project.Query.Status") {
		t.Error("expected module Mix.Tasks.Project.Query.Status in status task")
	}
}

func TestScaffoldElixirCustomName(t *testing.T) {
	dir := t.TempDir()
	_, err := ScaffoldElixir(dir, "my_app")
	if err != nil {
		t.Fatal(err)
	}

	// Files should use custom namespace.
	path := filepath.Join(dir, "lib", "mix", "tasks", "my_app", "query", "status.ex")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected file with my_app namespace")
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "Mix.Tasks.MyApp.Query.Status") {
		t.Error("expected module Mix.Tasks.MyApp.Query.Status")
	}
}

func TestElixirModName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"project", "Project"},
		{"my_cli", "MyCli"},
		{"my-cli", "MyCli"},
		{"quote_pilot", "QuotePilot"},
		{"app", "App"},
	}
	for _, tt := range tests {
		got := elixirModName(tt.input)
		if got != tt.want {
			t.Errorf("elixirModName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
