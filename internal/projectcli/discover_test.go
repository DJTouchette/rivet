package projectcli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRunDiscoverWithMissingBinary(t *testing.T) {
	result, err := RunDiscover("/nonexistent/binary")
	if err != nil {
		t.Fatalf("RunDiscover() error: %v (should return nil for missing binary)", err)
	}
	if result != nil {
		t.Error("RunDiscover() should return nil for missing binary")
	}
}

func TestRunDiscoverWithNonDiscoverBinary(t *testing.T) {
	// Use a real binary that doesn't have rivet-discover.
	binary := "ls"
	if runtime.GOOS == "windows" {
		binary = "cmd"
	}

	path, err := exec.LookPath(binary)
	if err != nil {
		t.Skip("ls not found in PATH")
	}

	result, err := RunDiscover(path)
	if err != nil {
		t.Fatalf("RunDiscover() error: %v (should return nil for non-discover binary)", err)
	}
	if result != nil {
		t.Errorf("RunDiscover() should return nil for binary without rivet-discover")
	}
}

func TestRunDiscoverWithScriptBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test not supported on windows")
	}

	// Create a script that outputs valid discover JSON.
	dir := t.TempDir()
	script := filepath.Join(dir, "fakecli")

	content := `#!/bin/sh
if [ "$1" = "rivet-discover" ]; then
  cat <<'ENDJSON'
{
  "capabilities": [
    {
      "name": "test.hello",
      "kind": "project_command",
      "description": "Say hello",
      "command": ["fakecli", "hello"],
      "output": "json",
      "safety": "safe"
    },
    {
      "name": "test.dangerous",
      "description": "Do something dangerous",
      "command": ["fakecli", "dangerous"],
      "safety": "dangerous"
    }
  ]
}
ENDJSON
  exit 0
fi
exit 1
`
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}

	result, err := RunDiscover(script)
	if err != nil {
		t.Fatalf("RunDiscover() error: %v", err)
	}
	if result == nil {
		t.Fatal("RunDiscover() returned nil")
	}
	if len(result.Capabilities) != 2 {
		t.Fatalf("got %d capabilities, want 2", len(result.Capabilities))
	}

	// Check first capability.
	c := result.Capabilities[0]
	if c.Name != "test.hello" {
		t.Errorf("Name = %q, want test.hello", c.Name)
	}
	if c.Kind != "project_command" {
		t.Errorf("Kind = %q, want project_command", c.Kind)
	}
	if c.Safety != "safe" {
		t.Errorf("Safety = %q, want safe", c.Safety)
	}

	// Check defaults applied to second capability.
	c2 := result.Capabilities[1]
	if c2.Kind != "project_command" {
		t.Errorf("default Kind = %q, want project_command", c2.Kind)
	}
	if c2.Output != "json" {
		t.Errorf("default Output = %q, want json", c2.Output)
	}
}

func TestRunDiscoverWithBadJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test not supported on windows")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "badjson")
	content := `#!/bin/sh
if [ "$1" = "rivet-discover" ]; then
  echo "not json at all"
  exit 0
fi
exit 1
`
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}

	_, err := RunDiscover(script)
	if err == nil {
		t.Error("RunDiscover() should return error for bad JSON")
	}
}
