package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/djtouchette/rivet/internal/capabilities"
	rivetctx "github.com/djtouchette/rivet/internal/context"
)

func TestGenerateClaudeMD_Empty(t *testing.T) {
	section := GenerateClaudeMD(nil, nil)

	if !strings.Contains(section, markerStart) {
		t.Error("missing start marker")
	}
	if !strings.Contains(section, markerEnd) {
		t.Error("missing end marker")
	}
	if !strings.Contains(section, "No capabilities registered") {
		t.Error("expected empty capabilities message")
	}
	if !strings.Contains(section, "rivet-explorer") {
		t.Error("expected agent guidance in Rivet rules")
	}
}

func TestGenerateClaudeMD_WithCaps(t *testing.T) {
	caps := []capabilities.Capability{
		{Name: "db.summary", Kind: "project_command", Description: "DB summary", Safety: "safe"},
		{Name: "search.reindex", Kind: "project_command", Description: "Rebuild index", Safety: "dangerous"},
	}

	section := GenerateClaudeMD(caps, nil)

	if !strings.Contains(section, "`db.summary`") {
		t.Error("expected db.summary in output")
	}
	if !strings.Contains(section, "Safe (read-only") {
		t.Error("expected Safe section header")
	}
	if !strings.Contains(section, "Dangerous (requires approval") {
		t.Error("expected Dangerous section header")
	}
}

func TestGenerateClaudeMD_WithContext(t *testing.T) {
	docs := []*rivetctx.Document{
		{Name: "billing", Kind: rivetctx.KindDomain},
		{Name: "caching", Kind: rivetctx.KindParadigm},
	}

	section := GenerateClaudeMD(nil, docs)

	if !strings.Contains(section, "billing") {
		t.Error("expected billing in context section")
	}
	if !strings.Contains(section, "caching") {
		t.Error("expected caching in context section")
	}
	if !strings.Contains(section, "Domains:") {
		t.Error("expected Domains label")
	}
	if !strings.Contains(section, "Paradigms:") {
		t.Error("expected Paradigms label")
	}
}

func TestWriteClaudeMD_NewFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "CLAUDE.md")

	section := GenerateClaudeMD(nil, nil)
	if err := WriteClaudeMD(path, section); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}

	if !strings.Contains(string(content), markerStart) {
		t.Error("missing start marker in new file")
	}
}

func TestWriteClaudeMD_PreservesUserContent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "CLAUDE.md")

	// Write initial file with user content and rivet section.
	initial := "# My Project\n\nCustom notes here.\n\n" +
		markerStart + "\nold rivet content\n" + markerEnd + "\n\n" +
		"# More User Stuff\n\nDon't touch this.\n"
	os.WriteFile(path, []byte(initial), 0644)

	section := GenerateClaudeMD(nil, nil)
	if err := WriteClaudeMD(path, section); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}

	s := string(content)

	if !strings.Contains(s, "# My Project") {
		t.Error("user content before markers was lost")
	}
	if !strings.Contains(s, "# More User Stuff") {
		t.Error("user content after markers was lost")
	}
	if !strings.Contains(s, "Don't touch this.") {
		t.Error("trailing user content was lost")
	}
	if strings.Contains(s, "old rivet content") {
		t.Error("old rivet content should have been replaced")
	}
	if !strings.Contains(s, "Rivet — Project Capabilities") {
		t.Error("new rivet content should be present")
	}
}

func TestWriteClaudeMD_NoMarkers_Prepends(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "CLAUDE.md")

	// Existing file with no rivet markers.
	os.WriteFile(path, []byte("# My Notes\n\nSome stuff.\n"), 0644)

	section := GenerateClaudeMD(nil, nil)
	if err := WriteClaudeMD(path, section); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}

	s := string(content)

	// Rivet section should be prepended.
	startIdx := strings.Index(s, markerStart)
	userIdx := strings.Index(s, "# My Notes")
	if startIdx < 0 || userIdx < 0 {
		t.Fatal("missing expected content")
	}
	if startIdx > userIdx {
		t.Error("rivet section should be prepended before user content")
	}
}

func TestReplaceSection_ExactMarkers(t *testing.T) {
	content := "before\n" + markerStart + "\nold\n" + markerEnd + "\nafter\n"
	updated := replaceSection(content, "NEW_SECTION")

	if !strings.Contains(updated, "before\n") {
		t.Error("before content lost")
	}
	if !strings.Contains(updated, "\nafter\n") {
		t.Error("after content lost")
	}
	if !strings.Contains(updated, "NEW_SECTION") {
		t.Error("new section not inserted")
	}
	if strings.Contains(updated, "old") {
		t.Error("old section content should be gone")
	}
}
