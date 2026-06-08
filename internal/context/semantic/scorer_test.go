package semantic

import (
	"context"
	"testing"
)

func TestScorerRanksBySharedVocabulary(t *testing.T) {
	store, _ := OpenStore(t.TempDir(), "fake:v1", 0)
	sc := NewScorer(context.Background(), newFake(), store)

	if !sc.Prepare("payment refund invoice billing") {
		t.Fatal("Prepare should succeed with an embedder")
	}

	// A doc sharing vocabulary with the query should outscore an unrelated one.
	related, ok1 := sc.SimilarityFor("billing", "invoice billing payment refund customer")
	unrelated, ok2 := sc.SimilarityFor("auth", "kubernetes pod scheduler node taint")
	if !ok1 || !ok2 {
		t.Fatalf("similarity ok flags = %v/%v", ok1, ok2)
	}
	if related <= unrelated {
		t.Errorf("related %.3f should exceed unrelated %.3f", related, unrelated)
	}
	if related < 0 || related > 1 {
		t.Errorf("similarity %.3f out of [0,1]", related)
	}
}

func TestScorerCachesAndPersists(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(dir, "fake:v1", 0)
	sc := NewScorer(context.Background(), newFake(), store)
	sc.Prepare("alpha beta")
	sc.SimilarityFor("doc", "alpha beta gamma")

	if store.Len() == 0 {
		t.Error("on-the-fly embedding should have populated the store")
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	// A second scorer with NO embedder must still serve the cached vectors.
	reStore, _ := OpenStore(dir, "fake:v1", 0)
	offline := NewScorer(context.Background(), nil, reStore)
	if !offline.Prepare("alpha beta") {
		// Prepare needs the query vector; the query text was cached above.
		t.Fatal("offline Prepare failed despite cached query vector")
	}
	if _, ok := offline.SimilarityFor("doc", "alpha beta gamma"); !ok {
		t.Error("offline scorer should serve cached doc vector")
	}
}

func TestScorerNoEmbedderNoCacheDegrades(t *testing.T) {
	store, _ := OpenStore(t.TempDir(), "fake:v1", 0)
	// No embedder and an empty store: Prepare can't get a query vector.
	sc := NewScorer(context.Background(), nil, store)
	if sc.Prepare("anything") {
		t.Error("Prepare should fail with no embedder and empty cache")
	}
	if _, ok := sc.SimilarityFor("doc", "text"); ok {
		t.Error("SimilarityFor should report not-ok when unprepared")
	}
}
