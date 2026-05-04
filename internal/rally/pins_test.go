package rally

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTicketFile(t *testing.T, root, provider, id, title, body string) {
	t.Helper()
	dir := filepath.Join(root, ticketsDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	content := "# " + id + ": " + title + "\n\n" + body
	path := filepath.Join(dir, provider+"-"+id+".md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func writePinsFile(t *testing.T, root, content string) {
	t.Helper()
	dir := filepath.Join(root, ".rally")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, pinsPath), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPinProvider_Source(t *testing.T) {
	p := &PinProvider{Root: "."}
	if p.Source() != "rally" {
		t.Fatalf("Source: %q", p.Source())
	}
}

func TestPinProvider_List_NoPinsFile(t *testing.T) {
	root := t.TempDir()
	p := &PinProvider{Root: root}

	items, err := p.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items, got %d", len(items))
	}
}

func TestPinProvider_List_WithPinsAndTitles(t *testing.T) {
	root := t.TempDir()
	writeTicketFile(t, root, "jira", "RAL-1", "Fix the thing", "body of RAL-1")
	writePinsFile(t, root, `{"pins":[{"ticket_id":"RAL-1","pinned_at":"2026-05-04T00:00:00Z","note":"WIP"}]}`)

	p := &PinProvider{Root: root}
	items, err := p.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].URI != "rally://pinned/RAL-1" {
		t.Fatalf("URI: %q", items[0].URI)
	}
	if !strings.Contains(items[0].Name, "Fix the thing") {
		t.Fatalf("Name should include title, got %q", items[0].Name)
	}
	if items[0].Description != "WIP" {
		t.Fatalf("Description should be the note, got %q", items[0].Description)
	}
	if items[0].MimeType != "text/markdown" {
		t.Fatalf("MimeType: %q", items[0].MimeType)
	}
}

func TestPinProvider_List_PinnedButNotSynced(t *testing.T) {
	root := t.TempDir()
	// Pin exists but no matching markdown file — should still show, just without title.
	writePinsFile(t, root, `{"pins":[{"ticket_id":"RAL-99","pinned_at":"2026-05-04T00:00:00Z"}]}`)

	p := &PinProvider{Root: root}
	items, err := p.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Name != "RAL-99" {
		t.Fatalf("Name should fall back to ID when title unknown, got %q", items[0].Name)
	}
}

func TestPinProvider_Read_FullBody(t *testing.T) {
	root := t.TempDir()
	writeTicketFile(t, root, "linear", "RAL-2", "Other thing", "the body content")
	writePinsFile(t, root, `{"pins":[{"ticket_id":"RAL-2","pinned_at":"2026-05-04T00:00:00Z"}]}`)

	p := &PinProvider{Root: root}
	item, err := p.Read("rally://pinned/RAL-2")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(item.Body, "the body content") {
		t.Fatalf("Body should contain ticket content, got %q", item.Body)
	}
	if !strings.HasPrefix(item.Body, "# RAL-2: Other thing") {
		t.Fatalf("Body should start with heading, got %q", item.Body)
	}
}

func TestPinProvider_Read_NotPinned(t *testing.T) {
	root := t.TempDir()
	writeTicketFile(t, root, "jira", "RAL-1", "Fix the thing", "body")
	writePinsFile(t, root, `{"pins":[]}`)

	p := &PinProvider{Root: root}
	_, err := p.Read("rally://pinned/RAL-1")
	if err == nil {
		t.Fatal("expected error reading unpinned ticket")
	}
}

func TestPinProvider_Read_BadURI(t *testing.T) {
	p := &PinProvider{Root: t.TempDir()}
	_, err := p.Read("witness://pinned/x")
	if err == nil {
		t.Fatal("expected error for non-rally URI")
	}
}

func TestPinProvider_PinAndUnpin(t *testing.T) {
	root := t.TempDir()
	p := &PinProvider{Root: root}

	if err := p.Pin("RAL-1", "WIP"); err != nil {
		t.Fatalf("Pin: %v", err)
	}

	pinned, err := p.readPins()
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned) != 1 || pinned[0].TicketID != "RAL-1" || pinned[0].Note != "WIP" {
		t.Fatalf("unexpected pins after Pin: %+v", pinned)
	}

	// Re-pinning is idempotent and does not refresh PinnedAt.
	first := pinned[0].PinnedAt
	if err := p.Pin("RAL-1", ""); err != nil {
		t.Fatal(err)
	}
	pinned, _ = p.readPins()
	if !pinned[0].PinnedAt.Equal(first) {
		t.Fatal("re-pinning should not refresh PinnedAt")
	}
	if pinned[0].Note != "WIP" {
		t.Fatalf("empty note should preserve existing note, got %q", pinned[0].Note)
	}

	// Pin with new non-empty note updates the note.
	if err := p.Pin("RAL-1", "review feedback"); err != nil {
		t.Fatal(err)
	}
	pinned, _ = p.readPins()
	if pinned[0].Note != "review feedback" {
		t.Fatalf("note should update to 'review feedback', got %q", pinned[0].Note)
	}

	if err := p.Unpin("RAL-1"); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	pinned, _ = p.readPins()
	if len(pinned) != 0 {
		t.Fatalf("expected empty pins after Unpin, got %+v", pinned)
	}

	// Unpinning a missing pin is a no-op.
	if err := p.Unpin("RAL-1"); err != nil {
		t.Fatalf("Unpin missing: %v", err)
	}
}

func TestPinProvider_Pin_EmptyID(t *testing.T) {
	p := &PinProvider{Root: t.TempDir()}
	if err := p.Pin("", "x"); err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestParseFirstHeading(t *testing.T) {
	id, title := parseFirstHeading("# RAL-1: My title\n\nbody")
	if id != "RAL-1" || title != "My title" {
		t.Fatalf("got id=%q title=%q", id, title)
	}

	id, title = parseFirstHeading("# Just a title\n")
	if id != "" || title != "Just a title" {
		t.Fatalf("expected fallback to title-only, got id=%q title=%q", id, title)
	}

	id, title = parseFirstHeading("not a heading")
	if id != "" || title != "" {
		t.Fatalf("expected empty for non-heading content")
	}
}
