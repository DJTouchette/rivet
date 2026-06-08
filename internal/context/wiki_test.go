package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(docs []*Document) map[string]*Document {
	m := map[string]*Document{}
	for _, d := range docs {
		m[d.Name] = d
	}
	return m
}

func TestLoadWiki_NativeAndNested(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".rivet/wiki/onboarding.md"), "# Onboarding\nWelcome.")
	writeFile(t, filepath.Join(root, ".rivet/wiki/arch/services.md"), "# Services\nHow services talk.")
	// ADO artifacts that must be skipped.
	writeFile(t, filepath.Join(root, ".rivet/wiki/.order"), "onboarding\narch")
	writeFile(t, filepath.Join(root, ".rivet/wiki/.attachments/diagram.png"), "binary")

	docs, err := LoadWiki(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := names(docs)
	if len(docs) != 2 {
		t.Fatalf("got %d wiki docs, want 2: %v", len(docs), m)
	}
	if _, ok := m["onboarding"]; !ok {
		t.Error("missing onboarding")
	}
	// Nested page keeps its slash-relative name.
	nested, ok := m["arch/services"]
	if !ok {
		t.Fatalf("missing nested arch/services: %v", m)
	}
	if nested.Kind != KindWiki {
		t.Errorf("kind = %q, want wiki", nested.Kind)
	}
	if nested.URI() != "rivet://wiki/arch/services" {
		t.Errorf("uri = %q", nested.URI())
	}
}

func TestLoadWiki_ExtraPathsWithGlob(t *testing.T) {
	root := t.TempDir()
	// External docs tree referenced via a "docs/**" glob.
	writeFile(t, filepath.Join(root, "docs/guide.md"), "# Guide")
	writeFile(t, filepath.Join(root, "docs/deep/topic.md"), "# Topic")

	docs, err := LoadWiki(root, []string{"docs/**"})
	if err != nil {
		t.Fatal(err)
	}
	m := names(docs)
	if _, ok := m["guide"]; !ok {
		t.Errorf("glob root not indexed: %v", m)
	}
	if _, ok := m["deep/topic"]; !ok {
		t.Errorf("nested glob page not indexed: %v", m)
	}
}

func TestLoadWiki_MissingRootIsNotError(t *testing.T) {
	docs, err := LoadWiki(t.TempDir(), []string{"does/not/exist/**"})
	if err != nil {
		t.Fatalf("missing root should not error: %v", err)
	}
	if len(docs) != 0 {
		t.Errorf("expected no docs, got %d", len(docs))
	}
}

func TestLoadRunbooks_SkipsDrafts(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".rivet/runbooks/recover.md"),
		"---\ntriggers: [outage]\nseverity: high\nlast_tested: 2026-01-01\n---\n# Recover\nsteps")
	writeFile(t, filepath.Join(root, ".rivet/runbooks/drafts/wip.md"),
		"---\ntriggers: [wip]\n---\n# WIP")

	docs, err := LoadRunbooks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Name != "recover" {
		t.Fatalf("drafts not excluded: %+v", names(docs))
	}
	rb := docs[0]
	if rb.Kind != KindRunbook {
		t.Errorf("kind = %q, want runbook", rb.Kind)
	}
	if len(rb.Triggers) != 1 || rb.Triggers[0] != "outage" {
		t.Errorf("triggers = %v", rb.Triggers)
	}
	if rb.Severity != "high" {
		t.Errorf("severity = %q", rb.Severity)
	}
	if rb.LastTested.IsZero() {
		t.Error("last_tested not parsed")
	}
	if rb.URI() != "rivet://runbook/recover" {
		t.Errorf("uri = %q", rb.URI())
	}
}

func TestEmbeddingTextIncludesTriggers(t *testing.T) {
	d := &Document{Title: "Recover", Triggers: []string{"db down", "outage"}, Tags: []string{"ops"}, Body: "steps here"}
	txt := d.EmbeddingText()
	for _, want := range []string{"Recover", "db down", "outage", "ops", "steps here"} {
		if !strings.Contains(txt, want) {
			t.Errorf("embedding text missing %q: %q", want, txt)
		}
	}
}
