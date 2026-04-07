package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureProjectSetup_DoesNotOverwriteConfigWithoutForce(t *testing.T) {
	tmp := t.TempDir()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(cwd)

	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if err := os.MkdirAll(".rivet", 0755); err != nil {
		t.Fatalf("mkdir .rivet: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".rivet", "config.yaml"), []byte("custom: true\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	actions, err := ensureProjectSetup(false)
	if err != nil {
		t.Fatalf("ensureProjectSetup: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(".rivet", "config.yaml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(data) != "custom: true\n" {
		t.Fatalf("config was overwritten: %q", string(data))
	}

	foundSkip := false
	for _, action := range actions {
		if strings.Contains(action, ".rivet/config.yaml already exists, skipped") {
			foundSkip = true
			break
		}
	}
	if !foundSkip {
		t.Fatalf("expected config skip action, got %#v", actions)
	}

	if !fileExists(filepath.Join(".claude", "agents", "rivet-explorer.md")) {
		t.Fatal("missing rivet-explorer agent")
	}
	if !fileExists(filepath.Join(".claude", "agents", "rivet-investigator.md")) {
		t.Fatal("missing rivet-investigator agent")
	}
	if !fileExists(".mcp.json") {
		t.Fatal("missing .mcp.json")
	}
}

func TestEnsureProjectSetup_OverwritesConfigWithForce(t *testing.T) {
	tmp := t.TempDir()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(cwd)

	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if err := os.MkdirAll(".rivet", 0755); err != nil {
		t.Fatalf("mkdir .rivet: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".rivet", "config.yaml"), []byte("custom: true\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := ensureProjectSetup(true); err != nil {
		t.Fatalf("ensureProjectSetup(force): %v", err)
	}

	data, err := os.ReadFile(filepath.Join(".rivet", "config.yaml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(data) == "custom: true\n" {
		t.Fatal("config was not overwritten with force")
	}
}
