package projectcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffold(t *testing.T) {
	dir := t.TempDir()

	result, err := Scaffold(dir, "myapp", "github.com/example/myapp")
	if err != nil {
		t.Fatalf("Scaffold() error: %v", err)
	}

	if result.Dir != dir {
		t.Errorf("Dir = %q, want %q", result.Dir, dir)
	}

	// Should create all expected files.
	expected := []string{
		"go.mod",
		"main.go",
		"commands/root.go",
		"commands/discover.go",
		"commands/query_status.go",
		"commands/check_health.go",
		"commands/task_seed.go",
		"Makefile",
	}

	if len(result.Files) != len(expected) {
		t.Errorf("Files count = %d, want %d", len(result.Files), len(expected))
	}

	for _, f := range expected {
		path := filepath.Join(dir, f)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file %s to exist", f)
		}
	}

	if len(result.Skipped) != 0 {
		t.Errorf("Skipped = %v, want empty", result.Skipped)
	}
}

func TestScaffoldSkipsExisting(t *testing.T) {
	dir := t.TempDir()

	// First scaffold.
	_, err := Scaffold(dir, "myapp", "github.com/example/myapp")
	if err != nil {
		t.Fatalf("first Scaffold() error: %v", err)
	}

	// Second scaffold should skip everything.
	result, err := Scaffold(dir, "myapp", "github.com/example/myapp")
	if err != nil {
		t.Fatalf("second Scaffold() error: %v", err)
	}

	if len(result.Files) != 0 {
		t.Errorf("Files = %v, want empty on re-scaffold", result.Files)
	}
	if len(result.Skipped) != 8 {
		t.Errorf("Skipped count = %d, want 8", len(result.Skipped))
	}
}

func TestScaffoldGoModContent(t *testing.T) {
	dir := t.TempDir()

	_, err := Scaffold(dir, "myapp", "github.com/example/myapp")
	if err != nil {
		t.Fatalf("Scaffold() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "module github.com/example/myapp") {
		t.Error("go.mod missing module path")
	}
	if !strings.Contains(content, "github.com/spf13/cobra") {
		t.Error("go.mod missing cobra dependency")
	}
}

func TestScaffoldMainContent(t *testing.T) {
	dir := t.TempDir()

	_, err := Scaffold(dir, "myapp", "github.com/example/myapp")
	if err != nil {
		t.Fatalf("Scaffold() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, `"github.com/example/myapp/commands"`) {
		t.Error("main.go missing import of commands package")
	}
	if !strings.Contains(content, "commands.Execute()") {
		t.Error("main.go missing Execute() call")
	}
}

func TestScaffoldRootContent(t *testing.T) {
	dir := t.TempDir()

	_, err := Scaffold(dir, "my-app", "github.com/example/my-app")
	if err != nil {
		t.Fatalf("Scaffold() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "commands", "root.go"))
	if err != nil {
		t.Fatalf("reading root.go: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, `Use:   "my-app"`) {
		t.Error("root.go missing CLI name in Use field")
	}
	if !strings.Contains(content, `"json"`) {
		t.Error("root.go missing --json flag")
	}
	if !strings.Contains(content, "queryCmd") {
		t.Error("root.go missing query command category")
	}
	if !strings.Contains(content, "checkCmd") {
		t.Error("root.go missing check command category")
	}
	if !strings.Contains(content, "taskCmd") {
		t.Error("root.go missing task command category")
	}
}

func TestScaffoldDiscoverContent(t *testing.T) {
	dir := t.TempDir()

	_, err := Scaffold(dir, "myapp", "github.com/example/myapp")
	if err != nil {
		t.Fatalf("Scaffold() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "commands", "discover.go"))
	if err != nil {
		t.Fatalf("reading discover.go: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "rivet-discover") {
		t.Error("discover.go missing rivet-discover command")
	}
	if !strings.Contains(content, "myapp.status") {
		t.Error("discover.go missing myapp.status capability")
	}
	if !strings.Contains(content, "myapp.health") {
		t.Error("discover.go missing myapp.health capability")
	}
	if !strings.Contains(content, "myapp.seed") {
		t.Error("discover.go missing myapp.seed capability")
	}
}

func TestScaffoldDefaultName(t *testing.T) {
	dir := t.TempDir()

	result, err := Scaffold(dir, "", "")
	if err != nil {
		t.Fatalf("Scaffold() error: %v", err)
	}

	// Should default to "projectcli".
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	if !strings.Contains(string(data), "module projectcli") {
		t.Error("default scaffold should use 'projectcli' as module name")
	}

	if len(result.Files) != 8 {
		t.Errorf("Files count = %d, want 8", len(result.Files))
	}
}

func TestScaffoldMakefileContent(t *testing.T) {
	dir := t.TempDir()

	_, err := Scaffold(dir, "myapp", "github.com/example/myapp")
	if err != nil {
		t.Fatalf("Scaffold() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "Makefile"))
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "BIN := myapp") {
		t.Error("Makefile should reference the CLI name")
	}
}

func TestDiscoverCapabilities(t *testing.T) {
	caps := DiscoverCapabilities("./bin/myapp", "myapp")
	if len(caps) != 3 {
		t.Fatalf("DiscoverCapabilities() returned %d caps, want 3", len(caps))
	}

	names := make(map[string]bool)
	for _, c := range caps {
		names[c.Name] = true
		if len(c.Command) == 0 {
			t.Errorf("capability %q has no command", c.Name)
		}
		if c.Output == "" {
			t.Errorf("capability %q has no output format", c.Name)
		}
		if c.Safety == "" {
			t.Errorf("capability %q has no safety level", c.Name)
		}
	}

	for _, expected := range []string{"myapp.status", "myapp.health", "myapp.seed"} {
		if !names[expected] {
			t.Errorf("missing expected capability %q", expected)
		}
	}
}
