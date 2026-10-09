package context

import (
	"fmt"
	"strings"
	"testing"
)

// excerptCorpus is a small doc that states the answer in one bullet, and a
// large one that mentions the same words across many unrelated sections.
func excerptCorpus() []*Document {
	small := &Document{Name: "authorization", Kind: KindDomain, Title: "Authorization", Body: `# Authorization

## Overview

Relationship-based access control for every resource.

## Gotchas

- **Owner certificate visibility is one filter.** An owner sees a certificate only once the lab result is negative; trainers are unfiltered.
- Grants are OR-unioned across memberships.
`}

	var b strings.Builder
	b.WriteString("# Certificates\n\n")
	topics := []string{"owner", "certificate", "lab", "result", "visibility"}
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "## Topic %02d\n\n", i)
		// Each section names one or two of the query's words in passing.
		fmt.Fprintf(&b, "- The %s handling here is about pdf layout and signing %d.\n", topics[i%len(topics)], i)
		fmt.Fprintf(&b, "- Unrelated: the %s column is indexed for paging.\n\n", topics[(i+2)%len(topics)])
	}
	large := &Document{Name: "certificates", Kind: KindDomain, Title: "Certificates", Body: b.String()}
	return []*Document{small, large}
}

const excerptQuery = "can an owner see a certificate before the lab result"

// The headline ranking defect: a large general doc beat a small exact one,
// because whole-body scoring credited it for query words scattered across
// forty sections. Section-level scoring must put the exact doc first.
func TestRecommendSmallExactDocOutranksLargeGeneralDoc(t *testing.T) {
	docs := excerptCorpus()
	tokens := tokenize(excerptQuery)
	idf := tokenIDF(docs, tokens)

	// Precondition: the old whole-body signal really did prefer the large
	// doc, so this test is exercising the fix and not a corpus that was easy.
	smallWhole, _ := scoreBodyMatchWeighted(docs[0].Body, tokens, idf)
	largeWhole, _ := scoreBodyMatchWeighted(docs[1].Body, tokens, idf)
	if largeWhole <= smallWhole {
		t.Fatalf("fixture no longer reproduces the bias: whole-body large %.3f <= small %.3f", largeWhole, smallWhole)
	}

	recs := Recommend(docs, excerptQuery, 5)
	if len(recs) < 2 {
		t.Fatalf("want both docs recommended, got %d", len(recs))
	}
	if recs[0].Name != "authorization" {
		t.Errorf("rank 1 = %s (%.3f), want authorization; large doc scored %.3f", recs[0].Name, recs[0].Score, recs[1].Score)
	}
}

func TestRecommendExcerptsQuoteTheMatchingBullet(t *testing.T) {
	recs := Recommend(excerptCorpus(), excerptQuery, 5, WithExcerpts(DefaultExcerptBudget))
	if len(recs) == 0 || len(recs[0].Excerpts) == 0 {
		t.Fatalf("expected excerpts on the top doc, got %+v", recs)
	}
	ex := recs[0].Excerpts[0]
	if !strings.Contains(ex.Text, "Owner certificate visibility is one filter") {
		t.Errorf("best excerpt = %q, want the visibility bullet", ex.Text)
	}
	if ex.Section != "Gotchas" || ex.Heading != "Gotchas" {
		t.Errorf("excerpt section = %q / heading %q, want Gotchas", ex.Section, ex.Heading)
	}
	if strings.Contains(ex.Text, "OR-unioned") {
		t.Error("an excerpt should be one bullet, not the whole section")
	}
}

func TestRecommendExcerptsStayWithinBudget(t *testing.T) {
	// Every section of this doc matches, and each passage is ~1 KB, so the
	// budget — not the candidate count — is what has to stop selection.
	var b strings.Builder
	b.WriteString("# Big\n\n")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&b, "## Part %d\n\n- owner certificate lab result %d %s\n\n", i, i, strings.Repeat("filler ", 150))
	}
	docs := []*Document{
		{Name: "big", Kind: KindDomain, Body: b.String()},
		{Name: "other", Kind: KindDomain, Body: "# Other\n\n## A\n\n- owner certificate lab result here.\n"},
	}

	for _, budget := range []int{300, 1000, DefaultExcerptBudget} {
		recs := Recommend(docs, "owner certificate lab result", 5, WithExcerpts(budget))
		used := 0
		count := 0
		for _, r := range recs {
			if len(r.Excerpts) > maxExcerptsPerDoc {
				t.Errorf("budget %d: %s has %d excerpts, cap is %d", budget, r.Name, len(r.Excerpts), maxExcerptsPerDoc)
			}
			for _, ex := range r.Excerpts {
				used += len(ex.Text) + len(ex.Section) + len(r.Name) + 16
				count++
			}
		}
		if used > budget*bytesPerToken {
			t.Errorf("budget %d tokens: excerpts used %d bytes, over %d", budget, used, budget*bytesPerToken)
		}
		if count == 0 {
			t.Errorf("budget %d: no excerpts at all", budget)
		}
		out := FormatRecommendations("q", recs, false)
		if len(out) > budget*bytesPerToken+2000 { // list + labels on top of the excerpt budget
			t.Errorf("budget %d: formatted output %d bytes", budget, len(out))
		}
	}
}

func TestRecommendWithoutExcerptsHasNone(t *testing.T) {
	for _, opts := range [][]Option{nil, {WithExcerpts(0)}} {
		for _, r := range Recommend(excerptCorpus(), excerptQuery, 5, opts...) {
			if len(r.Excerpts) != 0 {
				t.Errorf("%s has excerpts without a budget", r.Name)
			}
		}
	}
}

func TestBM25PrefersAnExactPassageOverARepetitiveTable(t *testing.T) {
	table := "| user | role |\n|---|---|\n"
	for i := 0; i < 30; i++ {
		table += fmt.Sprintf("| owner%d@leroy.local | owner |\n", i)
	}
	texts := []string{
		strings.ToLower(table),
		"an owner sees a certificate only once the lab result is negative.",
		"unrelated text about pdf layout.",
	}
	s := bm25(texts, tokenize("owner certificate lab result"))
	if !(s[1] > s[0]) {
		t.Errorf("exact passage %.3f should beat the repetitive table %.3f", s[1], s[0])
	}
	if s[2] != 0 {
		t.Errorf("a passage sharing no token scored %.3f", s[2])
	}
}

func TestFormatRecommendationsShowsExcerptsAndHints(t *testing.T) {
	recs := Recommend(excerptCorpus(), excerptQuery, 5, WithExcerpts(DefaultExcerptBudget))

	cli := FormatRecommendations(excerptQuery, recs, false)
	for _, want := range []string{"Recommended context for", "Most relevant passages", "── authorization › Gotchas", "Owner certificate visibility", `rivet context show <name> --section "<heading>"`} {
		if !strings.Contains(cli, want) {
			t.Errorf("CLI output missing %q:\n%s", want, cli)
		}
	}
	mcp := FormatRecommendations(excerptQuery, recs, true)
	if !strings.Contains(mcp, `rivet.context-show {"name": "<name>", "section": "<heading>"}`) {
		t.Errorf("MCP output should give the tool-call form:\n%s", mcp)
	}
	if strings.Index(cli, "Most relevant passages") < strings.Index(cli, "uri: ") {
		t.Error("the ranked list should come before the excerpts")
	}
}

func TestShowHintQuotesWhenNeeded(t *testing.T) {
	if got := ShowHint(false, "Leroy.Certificates", "Lab tests (EIA / Piro)", 0); got != `rivet context show Leroy.Certificates --section "Lab tests (EIA / Piro)"` {
		t.Errorf("got %s", got)
	}
	if got := ShowHint(false, "auth", "Gotchas", 3); got != "rivet context show auth --section Gotchas --page 3" {
		t.Errorf("got %s", got)
	}
}
