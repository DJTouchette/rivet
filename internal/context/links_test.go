package context

import (
	"strings"
	"testing"
)

func TestWikiLinks(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []WikiLink
	}{
		{
			name: "single link",
			body: "See [[mcp]] for details.",
			want: []WikiLink{{Target: "mcp"}},
		},
		{
			name: "several links across lines",
			body: "See [[mcp]].\nAlso [[capabilities]] and [[context]].",
			want: []WikiLink{{Target: "mcp"}, {Target: "capabilities"}, {Target: "context"}},
		},
		{
			name: "alias keeps target and display text",
			body: "See [[tool-embedding|how tools compose]].",
			want: []WikiLink{{Target: "tool-embedding", Alias: "how tools compose"}},
		},
		{
			name: "duplicates collapse, first occurrence wins",
			body: "[[mcp]] then [[capabilities]] then [[mcp]] again.",
			want: []WikiLink{{Target: "mcp"}, {Target: "capabilities"}},
		},
		{
			name: "whitespace inside brackets is trimmed",
			body: "See [[  mcp  ]].",
			want: []WikiLink{{Target: "mcp"}},
		},
		{
			name: "no links",
			body: "# Title\n\nPlain prose with [a markdown link](http://example.com).",
			want: nil,
		},
		{
			name: "empty target is not a link",
			body: "An empty [[]] is nothing.",
			want: nil,
		},
		{
			name: "unterminated opener is not a link",
			body: "A dangling [[mcp and then nothing.",
			want: nil,
		},
		{
			name: "single brackets are not links",
			body: "An array index like arr[0] or [not a link].",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WikiLinks(tt.body)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d links %v, want %d %v", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("link %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// A doc that documents the link syntax, or shows one in a snippet, must not
// acquire a dependency on a doc named after the example.
func TestWikiLinksIgnoresCodeContent(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"inline code span", "Write `[[doc-name]]` to link."},
		{"fenced block", "```\nSee [[example]]\n```"},
		{"tilde fence", "~~~\nSee [[example]]\n~~~"},
		{"fenced with language", "```markdown\n[[example]]\n```"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WikiLinks(tt.body); len(got) != 0 {
				t.Errorf("expected no links, got %v", got)
			}
		})
	}
}

// Only the fenced region is exempt — real links on either side still count.
func TestWikiLinksAroundCodeFences(t *testing.T) {
	body := "Before [[real-one]].\n```\n[[in-fence]]\n```\nAfter [[other-one]]."

	got := WikiLinkTargets(body)
	want := []string{"real-one", "other-one"}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResolveWikiLinks(t *testing.T) {
	docs := []*Document{
		{Name: "mcp", Kind: KindDomain, Title: "Mcp"},
		{Name: "capabilities", Kind: KindDomain, Title: "Capabilities"},
	}

	resolved, broken := ResolveWikiLinks("See [[mcp]] and [[nope]] and [[capabilities]].", docs)

	if len(resolved) != 2 {
		t.Fatalf("resolved %d docs, want 2: %v", len(resolved), resolved)
	}
	if resolved[0].Name != "mcp" || resolved[1].Name != "capabilities" {
		t.Errorf("wrong docs resolved: %s, %s", resolved[0].Name, resolved[1].Name)
	}
	if len(broken) != 1 || broken[0] != "nope" {
		t.Errorf("broken = %v, want [nope]", broken)
	}
}

func TestResolveWikiLinksNoLinks(t *testing.T) {
	resolved, broken := ResolveWikiLinks("No links here.", []*Document{{Name: "mcp"}})
	if len(resolved) != 0 || len(broken) != 0 {
		t.Errorf("expected nothing, got resolved=%v broken=%v", resolved, broken)
	}
}

func TestFormatWikiLinks(t *testing.T) {
	all := []*Document{
		{Name: "orders", Kind: KindDomain, Title: "Orders & Invoicing", Body: "See [[retry]] and [[ghost]]."},
		{Name: "retry", Kind: KindModule, Title: "Retry Scheduler"},
	}

	got := FormatWikiLinks(all[0], all)

	// The resolved link needs the name (the argument context-show takes), its
	// tier, and the title.
	for _, want := range []string{"retry", "module", "Retry Scheduler"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	// A broken link is shown, not hidden — a reader who can see the target is
	// missing won't go hunting for it.
	if !strings.Contains(got, "ghost") || !strings.Contains(got, "MISSING") {
		t.Errorf("broken link should be surfaced:\n%s", got)
	}
}

// A doc with no links must add nothing at all, so output stays clean.
func TestFormatWikiLinksEmptyWhenNoLinks(t *testing.T) {
	doc := &Document{Name: "orders", Kind: KindDomain, Body: "No links."}
	if got := FormatWikiLinks(doc, []*Document{doc}); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// Titles are optional; the name is the fallback so a row is never blank.
func TestFormatWikiLinksFallsBackToName(t *testing.T) {
	all := []*Document{
		{Name: "a", Kind: KindDomain, Body: "See [[b]]."},
		{Name: "b", Kind: KindModule},
	}
	if got := FormatWikiLinks(all[0], all); !strings.Contains(got, "b") {
		t.Errorf("expected the name as fallback title:\n%s", got)
	}
}
