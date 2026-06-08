package context

import "testing"

// stubSemantic is a controllable Semantic for testing the additive signal in
// isolation from any real embedder. similarity maps a doc name to a cosine.
type stubSemantic struct {
	prepared   bool
	prepareOK  bool
	similarity map[string]float64
}

func (s *stubSemantic) Prepare(string) bool { s.prepared = true; return s.prepareOK }
func (s *stubSemantic) SimilarityFor(id, _ string) (float64, bool) {
	sim, ok := s.similarity[id]
	return sim, ok
}

func docFixture(name, title, body string, tags ...string) *Document {
	return &Document{Name: name, Kind: KindDomain, Title: title, Body: body, Tags: tags}
}

func TestRecommendSemanticAddsSignal(t *testing.T) {
	docs := []*Document{
		docFixture("billing", "Billing", "charges and money"),
		docFixture("scheduler", "Scheduler", "cron and jobs"),
	}
	sem := &stubSemantic{prepareOK: true, similarity: map[string]float64{
		"billing": 0.9, // strong semantic hit, no lexical overlap with the query
	}}

	// Query shares no tokens with either doc, so lexical score is 0 for both;
	// only the semantic signal can surface a result.
	recs := Recommend(docs, "customer payment dispute", 5, WithSemantic(sem))
	if !sem.prepared {
		t.Error("Prepare should have been called once")
	}
	if len(recs) != 1 || recs[0].Name != "billing" {
		t.Fatalf("expected only billing via semantic, got %+v", recs)
	}
	if !contains(recs[0].Signals, "semantic-match") {
		t.Errorf("signals = %v, want semantic-match", recs[0].Signals)
	}
	// 0.45 weight * 0.9 cosine = 0.405.
	if recs[0].Score < 0.40 || recs[0].Score > 0.41 {
		t.Errorf("semantic score = %.3f, want ~0.405", recs[0].Score)
	}
}

func TestRecommendSemanticAugmentsLexical(t *testing.T) {
	docs := []*Document{
		docFixture("billing", "Billing", "invoice handling", "invoice"),
	}
	lexicalOnly := Recommend(docs, "invoice", 5)
	if len(lexicalOnly) != 1 {
		t.Fatal("expected a lexical hit")
	}

	sem := &stubSemantic{prepareOK: true, similarity: map[string]float64{"billing": 0.8}}
	withSem := Recommend(docs, "invoice", 5, WithSemantic(sem))
	if len(withSem) != 1 {
		t.Fatal("expected a hit with semantic too")
	}
	// Semantic is additive: the combined score must exceed lexical-only (until
	// the 1.0 cap), and carry both signals.
	if withSem[0].Score <= lexicalOnly[0].Score {
		t.Errorf("semantic %.3f should augment lexical %.3f", withSem[0].Score, lexicalOnly[0].Score)
	}
	if !contains(withSem[0].Signals, "semantic-match") || len(withSem[0].Signals) < 2 {
		t.Errorf("signals = %v, want lexical + semantic", withSem[0].Signals)
	}
}

func TestRecommendSemanticFallbackWhenPrepareFails(t *testing.T) {
	docs := []*Document{docFixture("billing", "Billing", "invoice handling", "invoice")}
	sem := &stubSemantic{prepareOK: false} // embedder unavailable
	recs := Recommend(docs, "invoice", 5, WithSemantic(sem))
	// Behaves exactly like lexical-only.
	if len(recs) != 1 || recs[0].Name != "billing" {
		t.Fatalf("fallback failed: %+v", recs)
	}
	for _, s := range recs[0].Signals {
		if s == "semantic-match" {
			t.Error("semantic-match must not appear when Prepare returned false")
		}
	}
}

func TestRecommendNilSemanticUnchanged(t *testing.T) {
	docs := []*Document{docFixture("billing", "Billing", "invoice handling", "invoice")}
	a := Recommend(docs, "invoice", 5)
	b := Recommend(docs, "invoice", 5, WithSemantic(nil))
	if len(a) != len(b) || a[0].Score != b[0].Score {
		t.Error("WithSemantic(nil) should match plain Recommend")
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
