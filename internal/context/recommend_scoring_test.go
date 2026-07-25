package context

import (
	"math"
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
