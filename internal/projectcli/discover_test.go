package projectcli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// A project CLI that describes a destructive command but omits "safety" used to
// be registered as safe — i.e. auto-runnable over MCP with no approval, on the
// one axis that has to fail closed. An omitted field means the author didn't
// say, so it defaults to the only level the executor actually gates
// (dangerous), and says so on stderr: a capability whose safety level was
// decided for it silently is its own trap. Explicit declarations, including an
// explicit "safe", must survive untouched.
func TestApplyDefaults(t *testing.T) {
	tests := []struct {
		name       string
		in         DiscoveredCapability
		wantKind   string
		wantOutput string
		wantSafety string
		wantWarn   bool
	}{
		{
			name:       "omitted safety fails closed",
			in:         DiscoveredCapability{Name: "db.reset", Command: []string{"cli", "db", "reset"}},
			wantKind:   "project_command",
			wantOutput: "json",
			wantSafety: "dangerous",
			wantWarn:   true,
		},
		{
			name:       "explicit safe is preserved",
			in:         DiscoveredCapability{Name: "db.status", Safety: "safe"},
			wantKind:   "project_command",
			wantOutput: "json",
			wantSafety: "safe",
			wantWarn:   false,
		},
		{
			name:       "explicit guarded is preserved",
			in:         DiscoveredCapability{Name: "db.seed", Safety: "guarded"},
			wantKind:   "project_command",
			wantOutput: "json",
			wantSafety: "guarded",
			wantWarn:   false,
		},
		{
			name:       "explicit dangerous is preserved without a warning",
			in:         DiscoveredCapability{Name: "db.drop", Safety: "dangerous"},
			wantKind:   "project_command",
			wantOutput: "json",
			wantSafety: "dangerous",
			wantWarn:   false,
		},
		{
			name:       "explicit kind and output are preserved",
			in:         DiscoveredCapability{Name: "db.dump", Kind: "tool", Output: "text", Safety: "safe"},
			wantKind:   "tool",
			wantOutput: "text",
			wantSafety: "safe",
			wantWarn:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := []DiscoveredCapability{tt.in}
			var warn bytes.Buffer
			applyDefaults(caps, &warn)

			got := caps[0]
			if got.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", got.Kind, tt.wantKind)
			}
			if got.Output != tt.wantOutput {
				t.Errorf("Output = %q, want %q", got.Output, tt.wantOutput)
			}
			if got.Safety != tt.wantSafety {
				t.Errorf("Safety = %q, want %q", got.Safety, tt.wantSafety)
			}

			warned := warn.Len() > 0
			if warned != tt.wantWarn {
				t.Errorf("warned = %v (%q), want %v", warned, warn.String(), tt.wantWarn)
			}
			// The warning has to name the capability, or a user with twenty
			// discovered commands can't tell which one needs a safety field.
			if tt.wantWarn && !strings.Contains(warn.String(), tt.in.Name) {
				t.Errorf("warning %q does not name capability %q", warn.String(), tt.in.Name)
			}
		})
	}
}

// Defaulting is per-capability: one unlabelled command must not drag its
// well-behaved neighbours to dangerous, and must not be skipped because a
// neighbour was labelled.
func TestApplyDefaultsMixedBatch(t *testing.T) {
	caps := []DiscoveredCapability{
		{Name: "a.safe", Safety: "safe"},
		{Name: "b.unlabelled"},
		{Name: "c.guarded", Safety: "guarded"},
	}
	var warn bytes.Buffer
	applyDefaults(caps, &warn)

	want := []string{"safe", "dangerous", "guarded"}
	for i, w := range want {
		if caps[i].Safety != w {
			t.Errorf("caps[%d] (%s) Safety = %q, want %q", i, caps[i].Name, caps[i].Safety, w)
		}
	}
	if strings.Count(warn.String(), "warning:") != 1 {
		t.Errorf("expected exactly one warning, got: %q", warn.String())
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
