package context

import (
	"math"
	"strings"
	"testing"
)

// Path patterns used to score a flat 0.7 whatever they matched, so a broad glob
// tied exactly with a narrow one and the alphabetical name tiebreak decided the
// winner. Specificity must now order them.
func TestScorePathMatchRewardsSpecificity(t *testing.T) {
	const query = "services/billing/retry/backoff.go"

	broad, _ := scorePathMatch([]string{"services/billing/**"}, query)
	narrow, _ := scorePathMatch([]string{"services/billing/retry/**"}, query)
	exact, _ := scorePathMatch([]string{"services/billing/retry/backoff.go"}, query)

	if !(broad < narrow && narrow < exact) {
		t.Errorf("expected broad < narrow < exact, got %.3f / %.3f / %.3f", broad, narrow, exact)
	}
	if broad <= 0 {
		t.Error("a broad pattern that genuinely matches should still score")
	}
	if exact > pathMatchBase+pathMatchRange {
		t.Errorf("exact match %.3f exceeds the documented ceiling", exact)
	}
}

// A doc listing both a broad and a narrow pattern should be credited for the
// narrow one. Returning on the first match made the order of related_paths
// silently affect ranking.
func TestScorePathMatchTakesBestPatternNotFirst(t *testing.T) {
	const query = "services/billing/retry/backoff.go"

	broadFirst, _ := scorePathMatch([]string{"services/billing/**", "services/billing/retry/**"}, query)
	narrowFirst, _ := scorePathMatch([]string{"services/billing/retry/**", "services/billing/**"}, query)

	if math.Abs(broadFirst-narrowFirst) > 1e-9 {
		t.Errorf("pattern order changed the score: %.3f vs %.3f", broadFirst, narrowFirst)
	}
	if only, _ := scorePathMatch([]string{"services/billing/retry/**"}, query); math.Abs(broadFirst-only) > 1e-9 {
		t.Errorf("best-pattern score %.3f should equal the narrow pattern alone %.3f", broadFirst, only)
	}
}

func TestScorePathMatchNoMatch(t *testing.T) {
	if got, sig := scorePathMatch([]string{"services/orders/**"}, "services/billing/x.go"); got != 0 || sig != "" {
		t.Errorf("non-matching pattern scored %.3f (%q)", got, sig)
	}
	if got, _ := scorePathMatch(nil, "a/b.go"); got != 0 {
		t.Errorf("empty patterns scored %.3f", got)
	}
}

// softCap replaced a hard clamp that collapsed every strong match onto 1.0.
// Order preservation is the whole point.
func TestSoftCapIsMonotonicAndBounded(t *testing.T) {
	prev := -1.0
	for _, raw := range []float64{0, 0.1, 0.5, 0.8, 0.9, 1.0, 1.2, 1.5, 2.0, 2.75} {
		got := softCap(raw)
		if got <= prev {
			t.Errorf("softCap(%.2f) = %.4f is not greater than the previous %.4f", raw, got, prev)
		}
		if got >= 1.0 {
			t.Errorf("softCap(%.2f) = %.4f must stay below 1.0", raw, got)
		}
		prev = got
	}
}

// Below the knee the curve is the identity, so familiar score magnitudes and
// the existing tests that assert on them are unaffected.
func TestSoftCapIsIdentityBelowKnee(t *testing.T) {
	for _, raw := range []float64{0, 0.25, 0.6, softCapKnee} {
		if got := softCap(raw); math.Abs(got-raw) > 1e-9 {
			t.Errorf("softCap(%.3f) = %.6f, want identity below the knee", raw, got)
		}
	}
}

// The tier weight used to be applied before the clamp, so a keyword-stuffed
// wiki doc could saturate to 1.0 and erase its own penalty. A wiki doc must
// never reach a curated doc's ceiling however much signal it accumulates.
func TestWikiCannotOutrankCuratedAtSaturation(t *testing.T) {
	stuffed := &Document{
		Name: "payment-retries-faq", Title: "Payment Retries FAQ", Kind: KindWiki,
		Tags: []string{"payment", "retries", "payments", "retry"},
		Body: "payment retries payment retries payment retries",
	}
	curated := &Document{
		Name: "payment-retry", Title: "Payment Retry", Kind: KindDomain,
		Tags: []string{"payment", "retries"},
		Body: "payment retries",
	}

	got := Recommend([]*Document{stuffed, curated}, "payment retries", 5)
	if len(got) != 2 {
		t.Fatalf("expected both docs to score, got %d", len(got))
	}
	if got[0].Name != "payment-retry" {
		t.Errorf("curated doc should lead, got %q at %.3f (wiki %.3f)", got[0].Name, got[0].Score, got[1].Score)
	}
}

// Raw substring comparison could not relate "cache" and "caching" in either
// direction — they diverge at cach|e vs cach|i — so a doc was findable only by
// whichever inflection its author happened to tag it with. Stemming both sides
// fixes it, and the result must not depend on which side is which.
func TestScoreTagMatchIsSymmetric(t *testing.T) {
	forward, _ := scoreTagMatch([]string{"caching"}, []string{"cache"})
	reverse, _ := scoreTagMatch([]string{"cache"}, []string{"caching"})

	if forward <= 0 || reverse <= 0 {
		t.Fatalf("both directions should match, got %.3f forward / %.3f reverse", forward, reverse)
	}
	if math.Abs(forward-reverse) > 1e-9 {
		t.Errorf("asymmetric scoring: %.3f vs %.3f", forward, reverse)
	}
}

func TestScoreTagMatchPrefersExactOverSubstring(t *testing.T) {
	exact, _ := scoreTagMatch([]string{"index"}, []string{"index"})
	substring, _ := scoreTagMatch([]string{"indexing-pipeline"}, []string{"index"})

	if exact <= substring {
		t.Errorf("exact tag hit %.3f should beat substring brush %.3f", exact, substring)
	}
}

func TestStemProbe(t *testing.T) {
	tests := []struct {
		name string
		tok  string
		want string
	}{
		{"past tense trimmed", "declined", "declin"},
		{"plural trimmed", "retries", "retri"},
		{"gerund trimmed", "indexing", "index"},
		{"simple plural trimmed", "webhooks", "webhook"},
		{"no suffix left alone", "cache", "cache"},
		// Trimming "gas" to "ga" would match far more than it should, so a
		// suffix that leaves too little behind is not cut at all.
		{"short word protected", "gas", "gas"},
		{"short gerund protected", "ring", "ring"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stemProbe(tt.tok); got != tt.want {
				t.Errorf("stemProbe(%q) = %q, want %q", tt.tok, got, tt.want)
			}
		})
	}
}

// Every signal matches by substring, so a single-character token matched
// essentially the whole corpus and diluted coverage ratios.
func TestTokenizeDropsSingleCharacters(t *testing.T) {
	for _, tok := range tokenize("how do I reindex a catalogue") {
		if len(tok) < 2 {
			t.Errorf("single-character token %q survived tokenize", tok)
		}
	}
}

// The all-noise fallback must still yield something rather than an empty query.
func TestTokenizeFallsBackWhenEverythingFiltered(t *testing.T) {
	if got := tokenize("a I"); len(got) == 0 {
		t.Error("tokenize returned nothing for an all-filtered query")
	}
}

// Stemming is what lets a symptom description reach a doc that uses a different
// inflection of the same word.
func TestBodyMatchFindsInflectedForms(t *testing.T) {
	body := "the gateway declines an identical charge within milliseconds"

	if got, _ := scoreBodyMatch(body, []string{"declined"}); got <= 0 {
		t.Error(`"declined" should match a body containing "declines"`)
	}
	if got, _ := scoreBodyMatch(body, []string{"unrelated"}); got != 0 {
		t.Errorf("unrelated token scored %.3f", got)
	}
}

// Coverage used to count every token alike, so a broad doc sharing three
// ubiquitous words beat a specific doc sharing the one rare word that actually
// identified the subject. IDF is measured over the live corpus, so the weights
// describe this project's vocabulary rather than English in general.
func TestTokenIDFRewardsRareTokens(t *testing.T) {
	docs := []*Document{
		{Name: "a", Body: "billing billing billing"},
		{Name: "b", Body: "billing and orders"},
		{Name: "c", Body: "billing and analyzer"},
	}

	idf := tokenIDF(docs, []string{"billing", "analyzer"})

	// "billing" is in every doc; "analyzer" in one.
	if idf["analyzer"] <= idf["billing"] {
		t.Errorf("rare token should weigh more: analyzer=%.4f billing=%.4f", idf["analyzer"], idf["billing"])
	}
	// A token in every document carries essentially no information.
	if idf["billing"] < 0 {
		t.Errorf("idf should never go negative, got %.4f", idf["billing"])
	}
}

func TestTokenIDFEmptyInputs(t *testing.T) {
	if got := tokenIDF(nil, []string{"x"}); got != nil {
		t.Errorf("no docs should yield no weights, got %v", got)
	}
	if got := tokenIDF([]*Document{{Body: "x"}}, nil); got != nil {
		t.Errorf("no tokens should yield no weights, got %v", got)
	}
}

// A nil idf must reduce every weighted signal exactly to its unweighted form,
// so callers without a corpus (RecommendLearnings) are unaffected.
func TestWeightedSignalsMatchUnweightedWhenIDFIsNil(t *testing.T) {
	tokens := []string{"retry", "backoff"}

	body, _ := scoreBodyMatch("retry with backoff", tokens)
	bodyW, _ := scoreBodyMatchWeighted("retry with backoff", tokens, nil)
	if math.Abs(body-bodyW) > 1e-9 {
		t.Errorf("body: %.6f vs %.6f", body, bodyW)
	}

	tag, _ := scoreTagMatch([]string{"retry"}, tokens)
	tagW, _ := scoreTagMatchWeighted([]string{"retry"}, tokens, nil)
	if math.Abs(tag-tagW) > 1e-9 {
		t.Errorf("tag: %.6f vs %.6f", tag, tagW)
	}

	name, _ := scoreNameMatch("payment-retry", "Payment Retry", tokens)
	nameW, _ := scoreNameMatchWeighted("payment-retry", "Payment Retry", tokens, nil)
	if math.Abs(name-nameW) > 1e-9 {
		t.Errorf("name: %.6f vs %.6f", name, nameW)
	}
}

// The end-to-end effect: a specific doc owning the rare token must beat a broad
// doc that only shares the common one.
func TestRecommendPrefersDocOwningTheRareToken(t *testing.T) {
	docs := []*Document{
		{Name: "search", Kind: KindDomain, Title: "Product Search Domain",
			Tags: []string{"search", "catalog"},
			Body: "Ranks catalogue products. Does not own the index build."},
		{Name: "search-indexer", Kind: KindModule, Title: "Search Index Pipeline",
			Tags: []string{"search", "reindex"},
			Body: "Reindex the catalogue with a blue/green alias flip."},
		// Filler so "catalogue" is common across the corpus while "reindex"
		// stays rare — IDF is relative, so it needs a corpus to be relative to.
		{Name: "cache", Kind: KindModule, Tags: []string{"cache"},
			Body: "Catalogue writes are rare so staleness is cheap."},
		{Name: "orders", Kind: KindDomain, Tags: []string{"orders"},
			Body: "Checkout over the catalogue."},
	}

	got := Recommend(docs, "reindex the catalogue", 5)
	if len(got) == 0 {
		t.Fatal("expected results")
	}
	if got[0].Name != "search-indexer" {
		t.Errorf("rare-token owner should lead, got %q (%.3f); full ranking: %v",
			got[0].Name, got[0].Score, rankNames(got))
	}
}

func rankNames(recs []Recommendation) []string {
	names := make([]string, 0, len(recs))
	for _, r := range recs {
		names = append(names, r.Name)
	}
	return names
}

// Presence alone could not distinguish "the subject of a section" from "named
// once in a Boundaries section that disclaims it", so a doc got retrieved for
// the very thing its own text said belonged elsewhere.
func TestTFBoostSaturates(t *testing.T) {
	// One mention scores exactly as before, so nothing that used to work shifts.
	if got := tfBoost(1); math.Abs(got-1) > 1e-9 {
		t.Errorf("tfBoost(1) = %.6f, want exactly 1", got)
	}
	if got := tfBoost(0); math.Abs(got-1) > 1e-9 {
		t.Errorf("tfBoost(0) = %.6f, want 1", got)
	}

	prev := tfBoost(1)
	for _, n := range []int{2, 3, 5, 10, 50, 500} {
		got := tfBoost(n)
		if got <= prev {
			t.Errorf("tfBoost(%d) = %.4f should exceed the previous %.4f", n, got, prev)
		}
		// Bounded: unbounded frequency would hand the win to whatever repeats a
		// word most, which is exactly the keyword-stuffing this must resist.
		if got > tfSaturationK+1 {
			t.Errorf("tfBoost(%d) = %.4f exceeds the asymptote %.4f", n, got, tfSaturationK+1)
		}
		prev = got
	}

	// Diminishing returns: the step from 1->2 must exceed the step from 9->10.
	early := tfBoost(2) - tfBoost(1)
	late := tfBoost(10) - tfBoost(9)
	if early <= late {
		t.Errorf("gains should diminish: 1->2 = %.4f, 9->10 = %.4f", early, late)
	}
}

// The end-to-end effect: a doc that merely disclaims a subject must lose to one
// that documents it, even though both contain the word.
func TestRecommendPrefersDensityOverAPassingMention(t *testing.T) {
	docs := []*Document{
		{Name: "search", Kind: KindDomain, Title: "Search Domain", Tags: []string{"search"},
			Body: "Owns the relevance model. The pipeline that populates the index — " +
				"mapping changes, backfills, reindexes — belongs to search-indexer."},
		{Name: "search-indexer", Kind: KindModule, Title: "Search Index Pipeline", Tags: []string{"search"},
			Body: "## Reindexing\nReindex is always blue/green against an alias. " +
				"Run the reindex in batches. A full reindex takes 40 minutes."},
		{Name: "orders", Kind: KindDomain, Tags: []string{"orders"}, Body: "Checkout and carts."},
	}

	got := Recommend(docs, "reindex", 5)
	if len(got) == 0 {
		t.Fatal("expected results")
	}
	if got[0].Name != "search-indexer" {
		t.Errorf("the doc documenting reindexing should lead, got %q (%.3f); ranking: %v",
			got[0].Name, got[0].Score, rankNames(got))
	}
}

// Density must not become a keyword-stuffing exploit: repetition alone cannot
// beat a doc that is genuinely about the subject and carries the tag.
func TestBodyDensityCannotOutrankTagsByRepetition(t *testing.T) {
	docs := []*Document{
		{Name: "stuffed-faq", Kind: KindWiki,
			Body: strings.Repeat("retry retries retry ", 40)},
		{Name: "payment-retry", Kind: KindModule, Title: "Payment Retry Scheduler",
			Tags: []string{"retry"},
			Body: "The scheduler owns the retry backoff and its idempotency keys."},
	}

	got := Recommend(docs, "retry", 5)
	if got[0].Name != "payment-retry" {
		t.Errorf("keyword stuffing should not win, got %q (%.3f)", got[0].Name, got[0].Score)
	}
}

// The body signal stays bounded so density can rival a tag match without
// swamping one outright.
func TestBodyWeightIsCapped(t *testing.T) {
	body := strings.Repeat("reindex ", 200)
	got, _ := scoreBodyMatch(body, []string{"reindex"})
	if got > bodyMaxWeight+1e-9 {
		t.Errorf("body weight %.4f exceeds the cap %.4f", got, bodyMaxWeight)
	}
}
