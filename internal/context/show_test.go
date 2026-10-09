package context

import (
	"fmt"
	"strings"
	"testing"
)

// bigDoc builds a document of roughly n sections of ~size bytes each, every
// one with a unique marker so a test can tell which sections a page holds.
func bigDoc(n, size int) *Document {
	var b strings.Builder
	b.WriteString("# Certificates\n\nIntro.\n\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "## Section %02d\n\n", i)
		fmt.Fprintf(&b, "- marker-%02d %s\n\n", i, strings.Repeat("x", size))
		if i == 3 {
			b.WriteString("### Owner visibility\n\n- Owners see a certificate only once the lab result is negative.\n\n")
		}
	}
	return &Document{Name: "Certificates", Kind: KindDomain, Body: b.String()}
}

func TestShowSmallDocIsReturnedWhole(t *testing.T) {
	doc := &Document{Name: "small", Kind: KindDomain, Body: "# Small\n\n## A\n\ntext\n"}
	out, err := Show(doc, []*Document{doc}, ShowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out != doc.Body {
		t.Errorf("a doc within budget must be returned unchanged, got %q", out)
	}
	if _, err := Show(doc, nil, ShowOptions{Page: 2}); err == nil {
		t.Error("page 2 of a one-page doc should be an error")
	}
}

func TestShowOverBudgetReturnsOutlineAndPagingHint(t *testing.T) {
	doc := bigDoc(60, 1500) // ~90 KB
	const budget = 4000
	out, err := Show(doc, []*Document{doc}, ShowOptions{Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > budget*bytesPerToken {
		t.Fatalf("output is %d bytes, over the %d-byte budget", len(out), budget*bytesPerToken)
	}
	for _, want := range []string{
		"over the 4000-token show budget",
		"page 1 of",
		"Outline",
		"- Section 00 (~",
		"  - Owner visibility (~", // nested heading, indented
		`rivet context show Certificates --section "<heading>"`,
		"rivet context show Certificates --page 2",
		"marker-00", // page 1 carries the opening sections, not just the outline
	} {
		if !strings.Contains(out, want) {
			t.Errorf("page 1 missing %q", want)
		}
	}
}

func TestShowPagesCoverTheDocWithinBudget(t *testing.T) {
	doc := bigDoc(60, 1500)
	const budget = 4000
	first, _ := Show(doc, nil, ShowOptions{Budget: budget})
	var total int
	if _, err := fmt.Sscanf(first[strings.Index(first, "page 1 of ")+len("page 1 of "):], "%d", &total); err != nil || total < 2 {
		t.Fatalf("could not read page count from %q", first[:200])
	}

	var all strings.Builder
	for p := 1; p <= total; p++ {
		out, err := Show(doc, nil, ShowOptions{Budget: budget, Page: p})
		if err != nil {
			t.Fatalf("page %d: %v", p, err)
		}
		if len(out) > budget*bytesPerToken {
			t.Errorf("page %d is %d bytes, over budget", p, len(out))
		}
		if p < total && !strings.Contains(out, fmt.Sprintf("--page %d", p+1)) {
			t.Errorf("page %d does not say how to get page %d", p, p+1)
		}
		all.WriteString(out)
	}
	for i := 0; i < 60; i++ {
		if !strings.Contains(all.String(), fmt.Sprintf("marker-%02d", i)) {
			t.Errorf("section %d appears on no page", i)
		}
	}
	if _, err := Show(doc, nil, ShowOptions{Budget: budget, Page: total + 1}); err == nil {
		t.Error("a page past the end should be an error")
	}
}

func TestShowSectionFetch(t *testing.T) {
	doc := bigDoc(60, 1500)

	for _, sel := range []string{"Owner visibility", "owner VISIBILITY", "Section 03 > Owner visibility", "## Owner visibility"} {
		out, err := Show(doc, nil, ShowOptions{Section: sel})
		if err != nil {
			t.Fatalf("section %q: %v", sel, err)
		}
		if !strings.HasPrefix(out, "### Owner visibility") || !strings.Contains(out, "lab result is negative") {
			t.Errorf("section %q returned %q", sel, out)
		}
	}

	// A parent section includes its subsections.
	out, err := Show(doc, nil, ShowOptions{Section: "Section 03"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "marker-03") || !strings.Contains(out, "Owner visibility") || strings.Contains(out, "marker-04") {
		t.Errorf("Section 03 should hold its own text and its subsection only:\n%s", out)
	}

	_, err = Show(doc, nil, ShowOptions{Section: "No such heading"})
	if err == nil || !strings.Contains(err.Error(), "Section 00") {
		t.Errorf("an unknown section should error with the outline, got %v", err)
	}
}

func TestShowPagesAnOversizedSection(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Doc\n\n## Learnings\n\n")
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "- learning %03d %s\n", i, strings.Repeat("y", 100))
	}
	doc := &Document{Name: "Doc", Body: b.String()}
	const budget = 2000

	out, err := Show(doc, nil, ShowOptions{Section: "Learnings", Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > budget*bytesPerToken {
		t.Fatalf("section page is %d bytes, over budget", len(out))
	}
	if !strings.Contains(out, `--section Learnings --page 2`) {
		t.Errorf("oversized section should point at its next page:\n%s", out[len(out)-200:])
	}
	out2, err := Show(doc, nil, ShowOptions{Section: "Learnings", Budget: budget, Page: 2})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out2, "learning 000") || len(out2) > budget*bytesPerToken {
		t.Error("page 2 of the section should continue, within budget")
	}
}

func TestShowNeverExceedsBudgetOnOneHugeLine(t *testing.T) {
	doc := &Document{Name: "blob", Body: "# Blob\n\n## Data\n\n" + strings.Repeat("é", 60000) + "\n"}
	const budget = 1000
	for p := 1; ; p++ {
		out, err := Show(doc, nil, ShowOptions{Budget: budget, Page: p})
		if err != nil {
			if p == 1 {
				t.Fatal(err)
			}
			break
		}
		if len(out) > budget*bytesPerToken {
			t.Fatalf("page %d is %d bytes, over the %d budget", p, len(out), budget*bytesPerToken)
		}
		if !strings.Contains(out, "page") {
			t.Fatalf("page %d lost its header", p)
		}
		if p > 200 {
			t.Fatal("paging did not terminate")
		}
	}
}

func TestShowMCPHintsUseToolSyntax(t *testing.T) {
	doc := bigDoc(60, 1500)
	out, err := Show(doc, nil, ShowOptions{Budget: 4000, MCP: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `rivet.context-show {"name": "Certificates", "page": 2}`) {
		t.Errorf("MCP page hint should be a tool call:\n%s", out[:600])
	}
	if strings.Contains(out, "rivet context show") {
		t.Error("MCP output should not tell the agent to run a CLI command")
	}
}
