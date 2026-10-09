package context

import (
	"strings"
	"testing"
)

const sectionsFixture = `# Billing

Intro paragraph.

## Overview

Billing owns invoices.

## Gotchas

- **Retries replay the key.** The second attempt reuses the
  idempotency key, so it returns the first response.
  - nested detail stays with its parent
- Second bullet.

### Deep dive

` + "```" + `
# not a heading, it is code
` + "```" + `

Closing paragraph.

## Learnings

1. Numbered learning.
2. Another one.
`

func TestSplitSectionsTilesTheBody(t *testing.T) {
	secs := SplitSections(sectionsFixture)

	var b strings.Builder
	for _, s := range secs {
		b.WriteString(s.Text)
	}
	if b.String() != sectionsFixture {
		t.Fatalf("sections do not reproduce the body:\n%s", b.String())
	}

	want := []struct {
		heading string
		level   int
		path    string
	}{
		{"Billing", 1, ""},
		{"Overview", 2, "Overview"},
		{"Gotchas", 2, "Gotchas"},
		{"Deep dive", 3, "Gotchas › Deep dive"},
		{"Learnings", 2, "Learnings"},
	}
	if len(secs) != len(want) {
		t.Fatalf("got %d sections, want %d: %+v", len(secs), len(want), secs)
	}
	for i, w := range want {
		if secs[i].Heading != w.heading || secs[i].Level != w.level || secs[i].PathString() != w.path {
			t.Errorf("section %d = (%q, %d, %q), want (%q, %d, %q)",
				i, secs[i].Heading, secs[i].Level, secs[i].PathString(), w.heading, w.level, w.path)
		}
	}
	if !strings.Contains(secs[3].Text, "# not a heading") {
		t.Error("a # line inside a fenced code block was treated as a heading")
	}
}

func TestSplitSectionsHandlesCRLFAndPreamble(t *testing.T) {
	body := "Preamble line.\r\n\r\n## One\r\ntext\r\n"
	secs := SplitSections(body)
	if len(secs) != 2 {
		t.Fatalf("got %d sections, want preamble + 1", len(secs))
	}
	if secs[0].Level != 0 || secs[0].Heading != "" {
		t.Errorf("first section should be the level-0 preamble, got %+v", secs[0])
	}
	if secs[1].Heading != "One" {
		t.Errorf("CRLF heading parsed as %q, want %q", secs[1].Heading, "One")
	}
}

func TestSectionSpanIncludesSubsections(t *testing.T) {
	secs := SplitSections(sectionsFixture)
	span := sectionSpan(secs, 2) // Gotchas
	if !strings.Contains(span, "### Deep dive") || !strings.Contains(span, "Closing paragraph.") {
		t.Errorf("Gotchas span should include its ### subsection:\n%s", span)
	}
	if strings.Contains(span, "## Learnings") {
		t.Error("span ran past the next sibling heading")
	}
}

func TestSplitPassagesSeparatesBulletsAndParagraphs(t *testing.T) {
	secs := SplitSections(sectionsFixture)

	gotchas := SplitPassages(secs[2])
	if len(gotchas) != 2 {
		t.Fatalf("Gotchas: got %d passages, want 2 bullets: %+v", len(gotchas), gotchas)
	}
	first := gotchas[0].Text
	if !strings.Contains(first, "idempotency key") || !strings.Contains(first, "nested detail") {
		t.Errorf("continuation and nested lines must stay with their bullet, got %q", first)
	}
	if gotchas[0].Section.Heading != "Gotchas" {
		t.Errorf("passage section = %q, want Gotchas", gotchas[0].Section.Heading)
	}

	deep := SplitPassages(secs[3])
	if len(deep) != 2 {
		t.Fatalf("Deep dive: got %d passages, want code block + paragraph: %+v", len(deep), deep)
	}
	if !strings.HasPrefix(deep[0].Text, "```") || !strings.Contains(deep[0].Text, "# not a heading") {
		t.Errorf("fenced block should be one passage, got %q", deep[0].Text)
	}

	learnings := SplitPassages(secs[4])
	if len(learnings) != 2 {
		t.Errorf("numbered list: got %d passages, want 2", len(learnings))
	}
}
