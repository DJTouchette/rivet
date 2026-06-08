package semantic

import "context"

// Indexable is anything that can produce text to embed and a stable id. It is
// satisfied by *context.Document (via EmbeddingText) without this package
// importing package context.
type Indexable interface {
	EmbeddingText() string
}

// IndexDocs chunks and embeds each document into the store, skipping chunks
// already cached (keyed by content hash, so unchanged text is never re-embedded
// on a re-run). It returns the number of newly embedded chunks. The caller is
// responsible for store.Save().
func IndexDocs[T Indexable](ctx context.Context, emb Embedder, store *Store, docs []T) (int, error) {
	model := emb.ID()

	// Collect all uncached chunks across all docs, then embed in one batch per
	// call to amortise HTTP round-trips.
	var pending []string
	seen := map[string]bool{}
	for _, d := range docs {
		for _, c := range chunk(d.EmbeddingText()) {
			h := HashText(model, c)
			if _, ok := store.Get(h); ok || seen[h] {
				continue
			}
			seen[h] = true
			pending = append(pending, c)
		}
	}
	if len(pending) == 0 {
		return 0, nil
	}

	vecs, err := emb.Embed(ctx, pending)
	if err != nil {
		return 0, err
	}
	for i, c := range pending {
		if i >= len(vecs) {
			break
		}
		if err := store.Put(HashText(model, c), vecs[i]); err != nil {
			return 0, err
		}
	}
	return len(pending), nil
}
