package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveNamed(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		got, err := Resolve(name, t.TempDir())
		if err != nil {
			t.Fatalf("Resolve(%q): %v", name, err)
		}
		if len(got) != 1 || got[0].Name() != name {
			t.Fatalf("Resolve(%q) = %v, want one %s", name, providerNames(got), name)
		}
	}
}

func TestResolveBoth(t *testing.T) {
	got, err := Resolve("both", t.TempDir())
	if err != nil {
		t.Fatalf("Resolve(both): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Resolve(both) = %v, want both providers", providerNames(got))
	}
}

func TestResolveUnknown(t *testing.T) {
	if _, err := Resolve("cursor", t.TempDir()); err == nil {
		t.Fatal("Resolve(cursor) succeeded, want an error naming the known providers")
	}
}

// A project with no markers at all has to come out claude. Rivet has always
// written CLAUDE.md, and adding a second provider must not quietly take that
// away from a project that never asked for one.
func TestResolveAutoDefaultsToClaude(t *testing.T) {
	got, err := Resolve("auto", t.TempDir())
	if err != nil {
		t.Fatalf("Resolve(auto): %v", err)
	}
	if len(got) != 1 || got[0].Name() != "claude" {
		t.Fatalf("Resolve(auto) on an empty project = %v, want claude", providerNames(got))
	}
}

func TestResolveAutoDetectsMarkers(t *testing.T) {
	cases := []struct {
		name    string
		markers []string
		want    []string
	}{
		{"claude md", []string{"CLAUDE.md"}, []string{"claude"}},
		{"claude dir", []string{".claude/"}, []string{"claude"}},
		{"agents md", []string{"AGENTS.md"}, []string{"codex"}},
		{"codex dir", []string{".codex/"}, []string{"codex"}},
		{"both", []string{"CLAUDE.md", "AGENTS.md"}, []string{"claude", "codex"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, m := range tc.markers {
				path := filepath.Join(dir, filepath.Clean(m))
				if m[len(m)-1] == '/' {
					if err := os.MkdirAll(path, 0755); err != nil {
						t.Fatalf("creating %s: %v", path, err)
					}
					continue
				}
				if err := os.WriteFile(path, nil, 0644); err != nil {
					t.Fatalf("writing %s: %v", path, err)
				}
			}

			got, err := Resolve("auto", dir)
			if err != nil {
				t.Fatalf("Resolve(auto): %v", err)
			}
			if !equal(providerNames(got), tc.want) {
				t.Fatalf("Resolve(auto) = %v, want %v", providerNames(got), tc.want)
			}
		})
	}
}

// The empty spec is what a caller that never set the flag passes. It has to
// behave like auto rather than erroring.
func TestResolveEmptySpecIsAuto(t *testing.T) {
	got, err := Resolve("", t.TempDir())
	if err != nil {
		t.Fatalf(`Resolve(""): %v`, err)
	}
	if len(got) != 1 || got[0].Name() != "claude" {
		t.Fatalf(`Resolve("") = %v, want claude`, providerNames(got))
	}
}

func TestClaudeWriteMCPConfigPreservesOtherServers(t *testing.T) {
	dir := t.TempDir()
	existing := `{"mcpServers":{"other":{"command":"other","args":["serve"]}}}`
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(existing), 0644); err != nil {
		t.Fatalf("seeding .mcp.json: %v", err)
	}

	if _, err := Claude().WriteMCPConfig(dir); err != nil {
		t.Fatalf("WriteMCPConfig: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatalf("reading .mcp.json: %v", err)
	}
	for _, want := range []string{`"other"`, `"rivet"`} {
		if !strings.Contains(string(got), want) {
			t.Errorf(".mcp.json lost %s:\n%s", want, got)
		}
	}
}

// Codex has no per-project MCP file, so WriteMCPConfig must never create one.
// A stray .mcp.json in a codex-only project would be dead weight the agent
// never reads.
func TestCodexWriteMCPConfigWritesNothingIntoTheProject(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", t.TempDir())

	if _, err := Codex().WriteMCPConfig(dir); err != nil {
		t.Fatalf("WriteMCPConfig: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	if len(entries) != 0 {
		t.Fatalf("codex WriteMCPConfig created %d project files, want none", len(entries))
	}
}

func providerNames(ps []Provider) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name())
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
