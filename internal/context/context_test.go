package context

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEmpty(t *testing.T) {
	dir := t.TempDir()
	docs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected 0 documents, got %d", len(docs))
	}
}

func TestLoadDocuments(t *testing.T) {
	dir := t.TempDir()

	// Create domain context
	domainDir := filepath.Join(dir, "domains")
	os.MkdirAll(domainDir, 0755)
	os.WriteFile(filepath.Join(domainDir, "billing.md"),
		[]byte("# Billing Domain\n\nHandles invoices and payment retries."), 0644)
	os.WriteFile(filepath.Join(domainDir, "auth.md"),
		[]byte("# Authentication\n\nUser auth and session management."), 0644)

	// Create module context
	moduleDir := filepath.Join(dir, "modules")
	os.MkdirAll(moduleDir, 0755)
	os.WriteFile(filepath.Join(moduleDir, "patient-search.md"),
		[]byte("# Patient Search\n\nSearch module for patient records."), 0644)

	// Create paradigm context (no heading — should fallback to filename)
	paradigmDir := filepath.Join(dir, "paradigms")
	os.MkdirAll(paradigmDir, 0755)
	os.WriteFile(filepath.Join(paradigmDir, "sql-views.md"),
		[]byte("SQL views are used for read-only aggregations."), 0644)

	docs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 4 {
		t.Fatalf("expected 4 documents, got %d", len(docs))
	}

	// Sorted by kind then name: domain < module < paradigm
	// domains: auth, billing
	if docs[0].Name != "auth" || docs[0].Kind != KindDomain {
		t.Errorf("expected auth domain at [0], got %s %s", docs[0].Name, docs[0].Kind)
	}
	if docs[0].Title != "Authentication" {
		t.Errorf("expected title 'Authentication', got %q", docs[0].Title)
	}

	if docs[1].Name != "billing" || docs[1].Kind != KindDomain {
		t.Errorf("expected billing domain at [1], got %s %s", docs[1].Name, docs[1].Kind)
	}
	if docs[1].Title != "Billing Domain" {
		t.Errorf("expected title 'Billing Domain', got %q", docs[1].Title)
	}

	// module: patient-search
	if docs[2].Name != "patient-search" || docs[2].Kind != KindModule {
		t.Errorf("expected patient-search module at [2], got %s %s", docs[2].Name, docs[2].Kind)
	}

	// paradigm: sql-views (no heading, title = filename)
	if docs[3].Name != "sql-views" || docs[3].Kind != KindParadigm {
		t.Errorf("expected sql-views paradigm at [3], got %s %s", docs[3].Name, docs[3].Kind)
	}
	if docs[3].Title != "sql-views" {
		t.Errorf("expected title fallback 'sql-views', got %q", docs[3].Title)
	}
}

func TestLoadSkipsNonMarkdown(t *testing.T) {
	dir := t.TempDir()
	domainDir := filepath.Join(dir, "domains")
	os.MkdirAll(domainDir, 0755)
	os.WriteFile(filepath.Join(domainDir, "billing.md"), []byte("# Billing"), 0644)
	os.WriteFile(filepath.Join(domainDir, "notes.txt"), []byte("not markdown"), 0644)
	os.MkdirAll(filepath.Join(domainDir, "subdir"), 0755) // directories are skipped

	docs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	if docs[0].Name != "billing" {
		t.Errorf("expected 'billing', got %q", docs[0].Name)
	}
}

func TestLoadBodyContent(t *testing.T) {
	dir := t.TempDir()
	domainDir := filepath.Join(dir, "domains")
	os.MkdirAll(domainDir, 0755)

	body := "# Billing Domain\n\n## Purpose\nHandles invoices.\n\n## Invariants\n- Retries must be idempotent\n"
	os.WriteFile(filepath.Join(domainDir, "billing.md"), []byte(body), 0644)

	docs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatal("expected 1 document")
	}
	if docs[0].Body != body {
		t.Errorf("body mismatch:\nwant: %q\ngot:  %q", body, docs[0].Body)
	}
}

func TestLoadPathIsSet(t *testing.T) {
	dir := t.TempDir()
	moduleDir := filepath.Join(dir, "modules")
	os.MkdirAll(moduleDir, 0755)
	os.WriteFile(filepath.Join(moduleDir, "search.md"), []byte("# Search"), 0644)

	docs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(moduleDir, "search.md")
	if docs[0].Path != expected {
		t.Errorf("expected path %q, got %q", expected, docs[0].Path)
	}
}

func TestDocumentURI(t *testing.T) {
	tests := []struct {
		name string
		kind Kind
		want string
	}{
		{"billing", KindDomain, "rivet://context/domains/billing"},
		{"patient-search", KindModule, "rivet://context/modules/patient-search"},
		{"sql-views", KindParadigm, "rivet://context/paradigms/sql-views"},
	}

	for _, tt := range tests {
		doc := &Document{Name: tt.name, Kind: tt.kind}
		if got := doc.URI(); got != tt.want {
			t.Errorf("URI() for %s %s = %q, want %q", tt.kind, tt.name, got, tt.want)
		}
	}
}

func TestExtractTitle(t *testing.T) {
	tests := []struct {
		body     string
		fallback string
		want     string
	}{
		{"# My Title\n\nContent here.", "default", "My Title"},
		{"Some text\n# Late Title\nMore text", "default", "Late Title"},
		{"No heading at all", "default", "default"},
		{"", "empty", "empty"},
		{"## Not H1\n### Also not", "fallback", "fallback"},
		{"  # Indented Title  \nContent", "fallback", "Indented Title"},
	}

	for _, tt := range tests {
		got := extractTitle(tt.body, tt.fallback)
		if got != tt.want {
			t.Errorf("extractTitle(%q, %q) = %q, want %q", tt.body, tt.fallback, got, tt.want)
		}
	}
}
