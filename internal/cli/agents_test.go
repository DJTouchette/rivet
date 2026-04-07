package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	actions, err := ensureAgents()
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

func TestEnsureAgents_DoesNotOverwriteExisting(t *testing.T) {
	tmp := t.TempDir()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(cwd)

	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	path := filepath.Join(".claude", "agents", "rivet-explorer.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("custom"), 0644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	actions, err := ensureAgents()
	if err != nil {
		t.Fatalf("ensureAgents: %v", err)
	}

	if len(actions) != 2 {
		t.Fatalf("expected 2 actions, got %#v", actions)
	}
	if !strings.Contains(actions[0], "already exists, skipped") {
		t.Fatalf("unexpected actions: %#v", actions)
	}
	if !strings.Contains(actions[1], "added rivet-investigator agent") {
		t.Fatalf("unexpected actions: %#v", actions)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading existing file: %v", err)
	}
	if string(data) != "custom" {
		t.Fatalf("existing agent was overwritten: %q", string(data))
	}
}
