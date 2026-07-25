package context

import (
	"strings"
	"testing"
)

// themeWarnings collects the untagged-theme terms reported for one document.
func themeWarnings(t *testing.T, docs []*Document, docName string) []string {
	t.Helper()
	var terms []string
	for _, w := range Lint(docs, t.TempDir()).Warnings {
		if w.Rule == "untagged-theme" && w.Document == docName {
			terms = append(terms, w.Message)
		}
	}
	return terms
}

func hasTerm(msgs []string, term string) bool {
	for _, m := range msgs {
		if strings.Contains(m, `"`+term+`"`) {
			return true
		}
	}
	return false
}

// filler pads a corpus so document-frequency thresholds behave as they would in
// a real project rather than in a two-document set.
func filler(n int) []*Document {
	docs := make([]*Document, 0, n)
	for i := 0; i < n; i++ {
		docs = append(docs, &Document{
			Name: string(rune('a'+i)) + "-filler", Kind: KindDomain,
			Tags: []string{"filler"}, Body: "Unrelated prose about shipping and invoices.",
		})
	}
	return docs
}

// The case that motivated the rule: a doc whose body is about a subject its
// tags never mention becomes unfindable by that subject.
func TestUntaggedThemeCatchesSubjectMissingFromTags(t *testing.T) {
	doc := &Document{
		Name: "search-indexer", Kind: KindModule, Title: "Search Index Pipeline",
		Tags: []string{"search", "index", "backfill"},
		// The term has to clear themeMinOccurrences, which is deliberately high:
		// a subject a doc genuinely dwells on gets mentioned a lot, and that is
		// exactly what separates it from a passing reference.
		Body: "# Pipeline\n\nAnalyzer changes silently alter relevance. " +
			"A relevance shift after an analyzer edit is the usual cause. " +
			"Re-check relevance whenever the analyzer or its relevance weights move. " +
			"Relevance is scored per shard, so a relevance regression in one shard " +
			"skews relevance overall; compare relevance before and after, and treat " +
			"any relevance delta beyond the noise floor as a relevance bug.",
	}
	docs := append([]*Document{doc}, filler(8)...)

	got := themeWarnings(t, docs, "search-indexer")
	if !hasTerm(got, "relevance") {
		t.Errorf("expected 'relevance' to be reported as an untagged theme, got %v", got)
	}
}

func TestUntaggedThemeIgnoresTaggedSubjects(t *testing.T) {
	doc := &Document{
		Name: "cache", Kind: KindModule, Title: "Cache Layer",
		Tags: []string{"invalidation"},
		Body: "# Cache\n\nInvalidation is by prefix. Invalidation never scans. " +
			"Prefer invalidation by key over invalidation by broadcast.",
	}
	docs := append([]*Document{doc}, filler(8)...)

	if got := themeWarnings(t, docs, "cache"); hasTerm(got, "invalidation") {
		t.Errorf("a tagged subject should not be reported: %v", got)
	}
}

// Stemming both ways, so a body full of "caching" is covered by a "cache" tag —
// and the y/ies pair, which plain suffix trimming never related.
func TestUntaggedThemeRespectsInflections(t *testing.T) {
	tests := []struct {
		name string
		tag  string
		word string
	}{
		{"plural tag covers singular body", "adapters", "adapter"},
		{"singular tag covers plural body", "adapter", "adapters"},
		{"ing form", "caching", "cache"},
		{"y/ies pair", "queries", "query"},
		{"ies/y pair", "query", "queries"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := "# T\n\n" + strings.Repeat(tt.word+" is discussed here. ", 6)
			doc := &Document{Name: "d", Kind: KindModule, Tags: []string{tt.tag}, Body: body}
			docs := append([]*Document{doc}, filler(8)...)

			if got := themeWarnings(t, docs, "d"); hasTerm(got, tt.word) {
				t.Errorf("tag %q should cover body term %q, got %v", tt.tag, tt.word, got)
			}
		})
	}
}

// Identifiers, paths and flags repeat constantly in technical prose and are
// never tags. Reporting them is the noise that gets a rule switched off.
func TestUntaggedThemeIgnoresCode(t *testing.T) {
	doc := &Document{
		Name: "d", Kind: KindModule, Tags: []string{"whatever"},
		Body: "# T\n\nSee `filepath.EvalSymlinks` and `filepath.Abs` and `filepath.Join`.\n" +
			"```go\nfilepath.Walk(root, filepath.SkipDir)\nfilepath.Clean(p)\n```\n",
	}
	docs := append([]*Document{doc}, filler(8)...)

	if got := themeWarnings(t, docs, "d"); hasTerm(got, "filepath") {
		t.Errorf("code content should not produce themes: %v", got)
	}
}

// A passing mention is not a theme. The threshold is what keeps the rule quiet
// enough to gate CI.
func TestUntaggedThemeIgnoresPassingMentions(t *testing.T) {
	doc := &Document{
		Name: "d", Kind: KindModule, Tags: []string{"whatever"},
		Body: "# T\n\nThis mentions telemetry once, and otherwise discusses shipping.",
	}
	docs := append([]*Document{doc}, filler(8)...)

	if got := themeWarnings(t, docs, "d"); hasTerm(got, "telemetry") {
		t.Errorf("a single mention should not be a theme: %v", got)
	}
}

// A word most of the corpus uses says nothing about which doc owns it.
func TestUntaggedThemeIgnoresUbiquitousTerms(t *testing.T) {
	body := "# T\n\n" + strings.Repeat("deployment happens here. ", 6)
	docs := []*Document{{Name: "d", Kind: KindModule, Tags: []string{"whatever"}, Body: body}}
	// Everything else uses the same word, so it cannot distinguish anything.
	for i := 0; i < 8; i++ {
		docs = append(docs, &Document{
			Name: string(rune('a'+i)) + "-other", Kind: KindDomain,
			Tags: []string{"other"}, Body: body,
		})
	}

	if got := themeWarnings(t, docs, "d"); hasTerm(got, "deployment") {
		t.Errorf("a corpus-wide term should not be a theme: %v", got)
	}
}

// A doc with no tags is already reported by missing-tags; adding every theme it
// has would bury the one warning that matters.
func TestUntaggedThemeSilentWhenNoTagsAtAll(t *testing.T) {
	doc := &Document{
		Name: "d", Kind: KindModule,
		Body: "# T\n\n" + strings.Repeat("telemetry matters. ", 8),
	}
	docs := append([]*Document{doc}, filler(8)...)

	if got := themeWarnings(t, docs, "d"); len(got) != 0 {
		t.Errorf("an untagged doc should only get missing-tags, got %v", got)
	}
}

// One document must not be able to flood the output.
func TestUntaggedThemeCapsPerDocument(t *testing.T) {
	var b strings.Builder
	b.WriteString("# T\n\n")
	for _, w := range []string{"telemetry", "sharding", "quorum", "gossip", "compaction"} {
		b.WriteString(strings.Repeat(w+" matters here. ", 6))
	}
	doc := &Document{Name: "d", Kind: KindModule, Tags: []string{"whatever"}, Body: b.String()}
	docs := append([]*Document{doc}, filler(8)...)

	if got := themeWarnings(t, docs, "d"); len(got) > themeMaxReported {
		t.Errorf("got %d warnings, want at most %d: %v", len(got), themeMaxReported, got)
	}
}

// Inflections of one word are one theme. Counting them apart both double-
// reported the same miss and split its frequency so neither half qualified.
func TestMergeByStemCombinesInflections(t *testing.T) {
	got := mergeByStem(map[string]int{"adapter": 2, "adapters": 4, "cache": 3})

	total := 0
	forms := 0
	for term, n := range got {
		if strings.HasPrefix(term, "adapter") {
			forms++
			total = n
		}
	}
	if forms != 1 {
		t.Errorf("adapter/adapters should merge into one entry, got %d: %v", forms, got)
	}
	if total != 6 {
		t.Errorf("merged count = %d, want 6 (2+4): %v", total, got)
	}
	if got["cache"] != 3 {
		t.Errorf("unrelated term should be untouched, got %v", got)
	}
}

// The lint-side stemmer takes the y/ies equivalence that retrieval measurably
// cannot afford.
func TestLintStemHandlesYAndIes(t *testing.T) {
	if lintStem("query") != lintStem("queries") {
		t.Errorf("query=%q queries=%q should match", lintStem("query"), lintStem("queries"))
	}
	if lintStem("policy") != lintStem("policies") {
		t.Errorf("policy=%q policies=%q should match", lintStem("policy"), lintStem("policies"))
	}
	// Short words are left alone rather than mangled into near-empty probes.
	if got := lintStem("key"); got != "key" {
		t.Errorf("lintStem(\"key\") = %q, want unchanged", got)
	}
}
