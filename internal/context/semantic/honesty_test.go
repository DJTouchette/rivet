package semantic

import (
	"context"
	"errors"
	"testing"
)

// failingEmbedder stands in for a configured backend that cannot answer: Ollama
// not running, or running without the model pulled.
type failingEmbedder struct{ err error }

func (f failingEmbedder) Embed(context.Context, []string) ([]Vector, error) { return nil, f.err }
func (f failingEmbedder) ID() string                                        { return "failing:v1" }
func (f failingEmbedder) Dim() int                                          { return 0 }

// shortEmbedder returns fewer vectors than it was given, the other way a
// backend can be useless without erroring.
type shortEmbedder struct{}

func (shortEmbedder) Embed(_ context.Context, texts []string) ([]Vector, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	return []Vector{{1, 0}}, nil // one vector regardless of how many were asked for
}
func (shortEmbedder) ID() string { return "short:v1" }
func (shortEmbedder) Dim() int   { return 2 }

// A broken backend must degrade to lexical *and* say so. Before Err existed the
// degradation was total and silent: Prepare returned false, the caller dropped
// the semantic signal, and the output was indistinguishable from a correctly
// unconfigured run — same results, same exit code, no mention of either.
func TestScorerReportsEmbedFailure(t *testing.T) {
	boom := errors.New("connection refused")
	store, _ := OpenStore(t.TempDir(), "failing:v1", 0)
	sc := NewScorer(context.Background(), failingEmbedder{err: boom}, store)

	if sc.Prepare("anything at all") {
		t.Fatal("Prepare should fail when the embedder errors")
	}
	if !errors.Is(sc.Err(), boom) {
		t.Errorf("Err() = %v, want the embedder's error", sc.Err())
	}
}

func TestScorerReportsShortResponse(t *testing.T) {
	store, _ := OpenStore(t.TempDir(), "short:v1", 0)
	sc := NewScorer(context.Background(), shortEmbedder{}, store)

	// A long enough text to chunk into more than one piece, so the embedder's
	// single-vector reply is provably short.
	long := ""
	for i := 0; i < 400; i++ {
		long += "billing invoice refund payment customer account ledger entry "
	}
	sc.Prepare(long)
	if sc.Err() == nil {
		t.Error("a short embedder response should be reported, not silently truncated")
	}
}

// The common case must stay quiet: a warning that fires when nothing is wrong
// is the same disease as a caveat that never fires.
func TestScorerNoErrorWhenHealthy(t *testing.T) {
	store, _ := OpenStore(t.TempDir(), "fake:v1", 0)
	sc := NewScorer(context.Background(), newFake(), store)

	if !sc.Prepare("payment refund invoice") {
		t.Fatal("Prepare should succeed")
	}
	if _, ok := sc.SimilarityFor("billing", "invoice billing payment"); !ok {
		t.Fatal("SimilarityFor should succeed")
	}
	if sc.Err() != nil {
		t.Errorf("healthy scorer reported Err() = %v, want nil", sc.Err())
	}
}

// A scorer with no embedder at all is "not configured", not "broken". It serves
// whatever is cached and reports no error.
func TestScorerNoEmbedderIsNotAnError(t *testing.T) {
	store, _ := OpenStore(t.TempDir(), "fake:v1", 0)
	sc := NewScorer(context.Background(), nil, store)

	sc.Prepare("payment refund invoice")
	if sc.Err() != nil {
		t.Errorf("unconfigured scorer reported Err() = %v, want nil", sc.Err())
	}
}

// Dropping an incompatible index is correct; dropping it silently is not. The
// model ID embeds the base URL, so pointing RIVET_EMBED_BASE_URL at a different
// Ollama host invalidates every cached vector — worth a word to whoever paid to
// build it.
func TestStoreReportsDiscardedIndex(t *testing.T) {
	dir := t.TempDir()
	st, _ := OpenStore(dir, "modelA", 0)
	if err := st.Put(HashText("modelA", "x"), Vector{1, 2}); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	switched, err := OpenStore(dir, "modelB", 0)
	if err != nil {
		t.Fatal(err)
	}
	was, discarded := switched.Discarded()
	if !discarded {
		t.Fatal("switching models should report the discarded index")
	}
	if was != "modelA" {
		t.Errorf("discarded model = %q, want %q", was, "modelA")
	}
	if switched.Model() != "modelB" {
		t.Errorf("Model() = %q, want %q", switched.Model(), "modelB")
	}
}

func TestStoreDiscardedQuietWhenMatching(t *testing.T) {
	dir := t.TempDir()
	st, _ := OpenStore(dir, "modelA", 0)
	st.Put(HashText("modelA", "x"), Vector{1, 2})
	st.Save()

	same, err := OpenStore(dir, "modelA", 0)
	if err != nil {
		t.Fatal(err)
	}
	if was, discarded := same.Discarded(); discarded {
		t.Errorf("matching model reported a discard of %q", was)
	}
	if same.Len() != 1 {
		t.Errorf("matching model should reuse the cache, got %d entries", same.Len())
	}
}

// An absent index is "nothing to discard", distinct from "discarded something".
func TestStoreDiscardedQuietWhenAbsent(t *testing.T) {
	st, err := OpenStore(t.TempDir(), "modelA", 0)
	if err != nil {
		t.Fatal(err)
	}
	if was, discarded := st.Discarded(); discarded {
		t.Errorf("empty dir reported a discard of %q", was)
	}
}
