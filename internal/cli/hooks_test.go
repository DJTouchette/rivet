package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeSettings drops a settings.json into the current directory's .claude/.
func writeSettings(t *testing.T, body string) string {
	t.Helper()
	if err := os.MkdirAll(".claude", 0755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	path := filepath.Join(".claude", "settings.json")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	return path
}

// writeLegacyHookScripts recreates what an older `rivet init` left on disk.
func writeLegacyHookScripts(t *testing.T) {
	t.Helper()
	dir := filepath.Join(".rivet", "hooks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	for _, name := range legacyHookScripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/bash\n"), 0755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

// readSettings parses settings.json back into a map.
func readSettings(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	return m
}

const legacySettings = `{
  "model": "opus",
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "mcp__rivet__recon_grep|mcp__rivet__recon_search",
        "hooks": [{"type": "command", "command": ".rivet/hooks/learn-nudge.sh"}]
      },
      {
        "matcher": "mcp__rivet__rivet_learn",
        "hooks": [{"type": "command", "command": ".rivet/hooks/compact-check.sh"}]
      }
    ]
  }
}`

func TestRemoveLegacyHooksDeletesScriptsAndSettings(t *testing.T) {
	t.Chdir(t.TempDir())

	path := writeSettings(t, legacySettings)
	writeLegacyHookScripts(t)

	msg, err := removeLegacyHooks()
	if err != nil {
		t.Fatalf("removeLegacyHooks: %v", err)
	}
	if msg == "no legacy hooks to remove" {
		t.Errorf("expected a removal message, got %q", msg)
	}

	for _, name := range legacyHookScripts {
		if _, err := os.Stat(filepath.Join(".rivet", "hooks", name)); !os.IsNotExist(err) {
			t.Errorf("%s should have been deleted", name)
		}
	}

	// The hooks dir held nothing else, so it should be gone too.
	if _, err := os.Stat(filepath.Join(".rivet", "hooks")); !os.IsNotExist(err) {
		t.Error(".rivet/hooks should have been removed once empty")
	}

	settings := readSettings(t, path)
	if _, ok := settings["hooks"]; ok {
		t.Error("hooks key should be gone once rivet's were the only entries")
	}
	// Unrelated settings must survive untouched.
	if settings["model"] != "opus" {
		t.Errorf("unrelated settings clobbered: %v", settings)
	}
}

func TestRemoveLegacyHooksPreservesForeignHooks(t *testing.T) {
	t.Chdir(t.TempDir())

	path := writeSettings(t, `{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{"type": "command", "command": "./my-own-hook.sh"}]
      },
      {
        "matcher": "mcp__rivet__recon_grep",
        "hooks": [{"type": "command", "command": ".rivet/hooks/learn-nudge.sh"}]
      }
    ]
  }
}`)

	if _, err := removeLegacyHooks(); err != nil {
		t.Fatalf("removeLegacyHooks: %v", err)
	}

	settings := readSettings(t, path)
	hooks, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		t.Fatal("hooks key should survive — a foreign hook remains")
	}
	entries, _ := hooks["PostToolUse"].([]interface{})
	if len(entries) != 1 {
		t.Fatalf("expected exactly the foreign hook to remain, got %d entries", len(entries))
	}
	em := entries[0].(map[string]interface{})
	if em["matcher"] != "Bash" {
		t.Errorf("wrong entry survived: %v", em)
	}
}

// A single entry can carry several commands. Removing ours must leave the
// others in place rather than dropping the whole entry.
func TestRemoveLegacyHooksKeepsSiblingCommandsInSameEntry(t *testing.T) {
	t.Chdir(t.TempDir())

	path := writeSettings(t, `{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "mcp__rivet__recon_grep",
        "hooks": [
          {"type": "command", "command": ".rivet/hooks/learn-nudge.sh"},
          {"type": "command", "command": "./audit.sh"}
        ]
      }
    ]
  }
}`)

	if _, err := removeLegacyHooks(); err != nil {
		t.Fatalf("removeLegacyHooks: %v", err)
	}

	settings := readSettings(t, path)
	hooks := settings["hooks"].(map[string]interface{})
	entries := hooks["PostToolUse"].([]interface{})
	inner := entries[0].(map[string]interface{})["hooks"].([]interface{})
	if len(inner) != 1 {
		t.Fatalf("expected 1 surviving command, got %d", len(inner))
	}
	if cmd := inner[0].(map[string]interface{})["command"]; cmd != "./audit.sh" {
		t.Errorf("wrong command survived: %v", cmd)
	}
}

// An even older rivet registered learn-nudge under Stop. Sweeping every event
// means that copy gets cleaned up too.
func TestRemoveLegacyHooksSweepsNonPostToolUseEvents(t *testing.T) {
	t.Chdir(t.TempDir())

	path := writeSettings(t, `{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": ".rivet/hooks/learn-nudge.sh"}]
      }
    ]
  }
}`)

	if _, err := removeLegacyHooks(); err != nil {
		t.Fatalf("removeLegacyHooks: %v", err)
	}

	settings := readSettings(t, path)
	if _, ok := settings["hooks"]; ok {
		t.Errorf("Stop hook should have been swept: %v", settings["hooks"])
	}
}

func TestRemoveLegacyHooksIsIdempotent(t *testing.T) {
	t.Chdir(t.TempDir())

	writeSettings(t, legacySettings)
	writeLegacyHookScripts(t)

	if _, err := removeLegacyHooks(); err != nil {
		t.Fatalf("first run: %v", err)
	}
	msg, err := removeLegacyHooks()
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if msg != "no legacy hooks to remove" {
		t.Errorf("second run should be a no-op, got %q", msg)
	}
}

// A fresh project has neither scripts nor a settings file. That's not an error.
func TestRemoveLegacyHooksOnCleanProject(t *testing.T) {
	t.Chdir(t.TempDir())

	msg, err := removeLegacyHooks()
	if err != nil {
		t.Fatalf("removeLegacyHooks on clean project: %v", err)
	}
	if msg != "no legacy hooks to remove" {
		t.Errorf("expected no-op message, got %q", msg)
	}
}

// A user-created file in .rivet/hooks/ means the directory is theirs now.
func TestRemoveLegacyHooksKeepsNonEmptyHooksDir(t *testing.T) {
	t.Chdir(t.TempDir())

	writeLegacyHookScripts(t)
	custom := filepath.Join(".rivet", "hooks", "my-hook.sh")
	if err := os.WriteFile(custom, []byte("#!/bin/bash\n"), 0755); err != nil {
		t.Fatalf("write custom hook: %v", err)
	}

	if _, err := removeLegacyHooks(); err != nil {
		t.Fatalf("removeLegacyHooks: %v", err)
	}

	if _, err := os.Stat(custom); err != nil {
		t.Errorf("user's own hook should survive: %v", err)
	}
}
