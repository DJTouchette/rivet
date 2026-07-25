package semantic

import (
	"context"
	"fmt"
)

// Scorer ranks documents against a query using cached vectors. It implements
// the context.Semantic interface (Prepare / SimilarityFor) without importing
// package context, so there is no import cycle: the recommender defines the
// interface, this type satisfies it, and the wiring layer adapts the two.
//
// The intended flow keeps query-time cost to a single embed call:
//
//	store is precomputed offline by `rivet context index` and committed.
//	Prepare(query) embeds the query once.
//	SimilarityFor(id, text) reads the document's cached chunk vectors and
//	  returns the best cosine. If a chunk is missing and an embedder is
//	  available, it is embedded on the fly and cached; otherwise that chunk is
//	  skipped (graceful degradation toward lexical-only).
type Scorer struct {
	emb   Embedder
	store *Store
	ctx   context.Context

	qChunks []Vector // query chunk vectors, set by Prepare
	lastErr error    // most recent embed failure, surfaced by Err
}

// Err returns the most recent embedding failure, or nil if every embed call
// succeeded (or none was needed). A Scorer whose Prepare returned false and
// whose Err is non-nil means semantic scoring was configured and broken, as
// opposed to configured and merely unnecessary — callers should say so rather
// than presenting lexical-only results as if nothing were wrong.
func (s *Scorer) Err() error { return s.lastErr }

// NewScorer builds a scorer over a store. emb may be nil, in which case the
// scorer serves only what is already cached and never makes a network/model
// call. store must be non-nil.
func NewScorer(ctx context.Context, emb Embedder, store *Store) *Scorer {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Scorer{emb: emb, store: store, ctx: ctx}
}

// modelID is the identity used for cache keys. Falls back to a sentinel when no
// embedder is attached so cached-only lookups still resolve against whatever
// model wrote the store (the store's manifest carries the authoritative model).
func (s *Scorer) modelID() string {
	if s.emb != nil {
		return s.emb.ID()
	}
	return s.store.model
}

// Prepare embeds the query and returns true if semantic scoring is possible
// (i.e. a query vector was obtained). A false result tells the recommender to
// skip the semantic signal entirely for this query.
func (s *Scorer) Prepare(query string) bool {
	s.qChunks = nil
	vecs := s.vectorsFor(query)
	if len(vecs) == 0 {
		return false
	}
	s.qChunks = vecs
	return true
}

// SimilarityFor returns the best cosine similarity between the prepared query
// and the document's chunks, in [0, 1] (negatives clamped to 0). The bool is
// false when no comparable vector was available, so the caller omits the
// signal rather than scoring it as zero.
func (s *Scorer) SimilarityFor(id, text string) (float64, bool) {
	if len(s.qChunks) == 0 {
		return 0, false
	}
	docVecs := s.vectorsFor(text)
	if len(docVecs) == 0 {
		return 0, false
	}
	best := -1.0
	for _, q := range s.qChunks {
		for _, d := range docVecs {
			if c := cosine(q, d); c > best {
				best = c
			}
		}
	}
	if best < 0 {
		best = 0
	}
	return best, true
}

// vectorsFor returns chunk vectors for text, reading from the store first and
// embedding+caching misses only when an embedder is attached.
func (s *Scorer) vectorsFor(text string) []Vector {
	chunks := chunk(text)
	if len(chunks) == 0 {
		return nil
	}
	model := s.modelID()

	var vecs []Vector
	var missing []string
	var missingIdx []int
	for _, c := range chunks {
		if v, ok := s.store.Get(HashText(model, c)); ok {
			vecs = append(vecs, v)
		} else {
			missingIdx = append(missingIdx, len(vecs))
			vecs = append(vecs, nil) // placeholder
			missing = append(missing, c)
		}
	}

	if len(missing) > 0 && s.emb != nil {
		embedded, err := s.emb.Embed(s.ctx, missing)
		switch {
		case err != nil:
			// Retained, not returned: falling back to lexical is the right
			// behaviour, but the caller needs to be able to say that it
			// happened. Dropping this made a broken backend indistinguishable
			// from an unconfigured one — same results, same exit code, no
			// mention of either.
			s.lastErr = err
		case len(embedded) != len(missing):
			s.lastErr = fmt.Errorf("embedder returned %d vectors for %d inputs", len(embedded), len(missing))
		default:
			for i, slot := range missingIdx {
				vecs[slot] = embedded[i]
				_ = s.store.Put(HashText(model, missing[i]), embedded[i])
			}
		}
	}

	// Drop any placeholders we could not fill.
	out := vecs[:0]
	for _, v := range vecs {
		if len(v) > 0 {
			out = append(out, v)
		}
	}
	return out
}

// Store exposes the underlying store so callers can Save() after on-the-fly
// embedding has populated new entries.
func (s *Scorer) Store() *Store { return s.store }
