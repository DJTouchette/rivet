package context

import "testing"

// lintRules collects the rules fired for one document, so assertions name the
// rule rather than matching on message text.
func lintRules(t *testing.T, docs []*Document, docName string) map[string]bool {
	t.Helper()
	rules := make(map[string]bool)
	for _, w := range Lint(docs, t.TempDir()).Warnings {
		if w.Document == docName {
			rules[w.Rule] = true
		}
	}
	return rules
}

func TestLintBrokenWikilink(t *testing.T) {
	docs := []*Document{
		{Name: "orders", Kind: KindDomain, Tags: []string{"orders"}, RelatedPaths: []string{"lib/**"},
			Owner: "damien", Body: "# Orders\n\nSee [[retry]] and [[typo-here]]."},
		{Name: "retry", Kind: KindModule, Tags: []string{"retry"}, RelatedPaths: []string{"lib/**"},
			Owner: "damien", Body: "# Retry\n\nContent."},
	}

	rules := lintRules(t, docs, "orders")
	if !rules["broken-wikilink"] {
		t.Error("a link naming no known document should be flagged")
	}

	// The link that does resolve must not be flagged, or the rule is useless.
	var brokenTargets int
	for _, w := range Lint(docs, t.TempDir()).Warnings {
		if w.Rule == "broken-wikilink" {
			brokenTargets++
		}
	}
	if brokenTargets != 1 {
		t.Errorf("expected exactly 1 broken link, got %d", brokenTargets)
	}
}

func TestLintValidWikilinkIsClean(t *testing.T) {
	docs := []*Document{
		{Name: "orders", Kind: KindDomain, Tags: []string{"orders"}, RelatedPaths: []string{"lib/**"},
			Owner: "damien", Body: "# Orders\n\nSee [[retry]]."},
		{Name: "retry", Kind: KindModule, Tags: []string{"retry"}, RelatedPaths: []string{"lib/**"},
			Owner: "damien", Body: "# Retry\n\nContent."},
	}

	if lintRules(t, docs, "orders")["broken-wikilink"] {
		t.Error("a link that resolves should not be flagged")
	}
}

// Links can point at any tier — a curated doc referencing a runbook or a wiki
// page is legitimate.
func TestLintWikilinkResolvesAcrossTiers(t *testing.T) {
	docs := []*Document{
		{Name: "orders", Kind: KindDomain, Tags: []string{"orders"}, RelatedPaths: []string{"lib/**"},
			Owner: "damien", Body: "# Orders\n\nSee [[payment-backlog]] and [[architecture]]."},
		{Name: "payment-backlog", Kind: KindRunbook, Owner: "team", Body: "# Recovery\n\nSteps."},
		{Name: "architecture", Kind: KindWiki, Body: "# Architecture\n\nNarrative."},
	}

	if lintRules(t, docs, "orders")["broken-wikilink"] {
		t.Error("links to runbook and wiki tiers should resolve")
	}
}

func TestLintSelfWikilink(t *testing.T) {
	docs := []*Document{
		{Name: "orders", Kind: KindDomain, Tags: []string{"orders"}, RelatedPaths: []string{"lib/**"},
			Owner: "damien", Body: "# Orders\n\nSee [[orders]]."},
	}

	rules := lintRules(t, docs, "orders")
	if !rules["self-wikilink"] {
		t.Error("a link to its own document should be flagged")
	}
	// A self-link is its own rule, not a broken one — the target does exist.
	if rules["broken-wikilink"] {
		t.Error("a self-link should not also report broken-wikilink")
	}
}

// A link inside a code fence is an example, not a dependency, so documenting
// the syntax must not produce warnings.
func TestLintIgnoresWikilinksInCode(t *testing.T) {
	docs := []*Document{
		{Name: "guide", Kind: KindParadigm, Tags: []string{"docs"}, RelatedPaths: []string{"lib/**"},
			Owner: "damien", Body: "# Guide\n\nWrite `[[doc-name]]` to link:\n\n```\n[[some-example]]\n```\n"},
	}

	if lintRules(t, docs, "guide")["broken-wikilink"] {
		t.Error("example links in code should not be validated")
	}
}

func TestLintDuplicateName(t *testing.T) {
	docs := []*Document{
		{Name: "orders", Kind: KindDomain, Tags: []string{"a"}, RelatedPaths: []string{"lib/**"},
			Owner: "d", Body: "# Orders\n\nOne."},
		{Name: "orders", Kind: KindWiki, Body: "# Orders\n\nTwo."},
	}

	var duplicates int
	var severity Severity
	for _, w := range Lint(docs, t.TempDir()).Warnings {
		if w.Rule == "duplicate-name" {
			duplicates++
			severity = w.Severity
		}
	}

	// Reported once for the collision, not once per copy.
	if duplicates != 1 {
		t.Errorf("expected 1 duplicate-name warning, got %d", duplicates)
	}
	// It breaks lookup outright, so it's an error rather than untidiness.
	if severity != SeverityError {
		t.Errorf("duplicate-name severity = %q, want error", severity)
	}
}

func TestLintUniqueNamesProduceNoDuplicateWarning(t *testing.T) {
	docs := []*Document{
		{Name: "orders", Kind: KindDomain, Tags: []string{"a"}, RelatedPaths: []string{"lib/**"}, Owner: "d", Body: "# A\n\nOne."},
		{Name: "billing", Kind: KindDomain, Tags: []string{"b"}, RelatedPaths: []string{"lib/**"}, Owner: "d", Body: "# B\n\nTwo."},
	}

	for _, w := range Lint(docs, t.TempDir()).Warnings {
		if w.Rule == "duplicate-name" {
			t.Errorf("unexpected duplicate-name for %q", w.Document)
		}
	}
}

// Code-extracted docs come from rivet:context comments, where there is nowhere
// to put an owner or a review date. Demanding frontmatter would be noise nobody
// can act on.
func TestLintExemptsCodeDocsFromFrontmatterRules(t *testing.T) {
	docs := []*Document{
		{Name: "internal/mcp/server.go", Kind: KindCode, Body: "Never call this inside a transaction."},
	}

	rules := lintRules(t, docs, "internal/mcp/server.go")
	for _, rule := range []string{"missing-tags", "missing-related-paths", "missing-owner", "missing-review"} {
		if rules[rule] {
			t.Errorf("code-extracted docs should be exempt from %s", rule)
		}
	}
}

// Content rules still apply to code docs — an empty one is genuinely broken.
func TestLintStillChecksCodeDocContent(t *testing.T) {
	docs := []*Document{
		{Name: "internal/x.go", Kind: KindCode, Body: "# Heading only\n"},
	}

	if !lintRules(t, docs, "internal/x.go")["empty-body"] {
		t.Error("an empty code-extracted doc should still be flagged")
	}
}

func TestLintResultHasErrors(t *testing.T) {
	tests := []struct {
		name string
		docs []*Document
		want bool
	}{
		{
			name: "warnings only",
			docs: []*Document{{Name: "a", Kind: KindDomain, Body: "# A\n\nContent."}},
			want: false,
		},
		{
			name: "empty body is an error",
			docs: []*Document{{Name: "a", Kind: KindDomain, Tags: []string{"a"},
				RelatedPaths: []string{"lib/**"}, Owner: "d", Body: "# A\n"}},
			want: true,
		},
		{
			name: "nothing at all",
			docs: nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Lint(tt.docs, t.TempDir()).HasErrors(); got != tt.want {
				t.Errorf("HasErrors() = %v, want %v", got, tt.want)
			}
		})
	}
}
