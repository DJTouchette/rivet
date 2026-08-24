package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/djtouchette/rivet/internal/provider"
)

func TestEnsureAgents_WritesRivetExplorer(t *testing.T) {
	tmp := t.TempDir()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(cwd)

	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	actions, err := ensureAgents(provider.Claude())
	if err != nil {
		t.Fatalf("ensureAgents: %v", err)
	}

	if len(actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(actions))
	}

	path := filepath.Join(tmp, ".claude", "agents", "rivet-explorer.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading agent file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "name: rivet-explorer") {
		t.Fatalf("agent file missing frontmatter name: %s", content)
	}
	if !strings.Contains(content, "model: haiku") {
		t.Fatalf("agent file missing model override: %s", content)
	}
	if !strings.Contains(content, "tools: Read, Grep, Glob, mcp__rivet__rivet_context_recommend") {
		t.Fatalf("agent file missing tool restrictions: %s", content)
	}
	if strings.Contains(content, "mcp__rivet__rivet_learn") {
		t.Fatalf("read-only explorer should not include rivet.learn: %s", content)
	}
	if !strings.Contains(content, "`rivet.context-recommend`") {
		t.Fatalf("agent file missing rivet workflow guidance: %s", content)
	}
	if !strings.Contains(content, "`recon.search`") {
		t.Fatalf("agent file missing recon guidance: %s", content)
	}

	investigatorPath := filepath.Join(tmp, ".claude", "agents", "rivet-investigator.md")
	investigatorData, err := os.ReadFile(investigatorPath)
	if err != nil {
		t.Fatalf("reading investigator file: %v", err)
	}
	investigator := string(investigatorData)
	if !strings.Contains(investigator, "name: rivet-investigator") {
		t.Fatalf("investigator file missing frontmatter name: %s", investigator)
	}
	if !strings.Contains(investigator, "model: sonnet") {
		t.Fatalf("investigator file missing sonnet model override: %s", investigator)
	}
	if !strings.Contains(investigator, "mcp__rivet__rivet_learn") {
		t.Fatalf("investigator file missing rivet.learn access: %s", investigator)
	}
}

// Generated agent briefs are refreshed, not skipped.
//
// Skip-if-exists froze them at whatever version first installed a project: the
// brief that tells the investigator how to search could be improved upstream
// and never reach anyone who already had one. A local edit is still not thrown
// away — it is moved aside and named in the action.
func TestEnsureAgents_RefreshesExistingAndKeepsTheOldCopy(t *testing.T) {
	t.Chdir(t.TempDir())

	path := filepath.Join(".claude", "agents", "rivet-explorer.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("stale brief"), 0644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	actions, err := ensureAgents(provider.Claude())
	if err != nil {
		t.Fatalf("ensureAgents: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading refreshed file: %v", err)
	}
	if string(data) == "stale brief" {
		t.Error("an outdated brief was left in place")
	}
	if !strings.Contains(string(data), "recon.grep") {
		t.Errorf("refreshed brief does not look like the shipped one: %.60q", data)
	}

	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("previous version was not preserved: %v", err)
	}
	if string(backup) != "stale brief" {
		t.Errorf("backup does not hold the previous contents: %q", backup)
	}

	var mentioned bool
	for _, a := range actions {
		if strings.Contains(a, "updated") && strings.Contains(a, ".bak") {
			mentioned = true
		}
	}
	if !mentioned {
		t.Errorf("the action should say the previous version was saved: %#v", actions)
	}
}

// An already-current file is left alone entirely — no churn, no stray backup.
func TestEnsureAgents_LeavesCurrentFilesAlone(t *testing.T) {
	t.Chdir(t.TempDir())

	if _, err := ensureAgents(provider.Claude()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	actions, err := ensureAgents(provider.Claude())
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	for _, a := range actions {
		if !strings.Contains(a, "already current") {
			t.Errorf("expected no-op on an unchanged file, got %q", a)
		}
	}
	if _, err := os.Stat(filepath.Join(".claude", "agents", "rivet-explorer.md.bak")); err == nil {
		t.Error("an unchanged file should not produce a backup")
	}
}
