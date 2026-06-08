// Package semantic adds an optional embedding-based retrieval signal to rivet's
// context recommender. It is deliberately additive: when no embedder is
// configured (or the configured one is unavailable) callers fall back to the
// lexical engine in package context with no behaviour change.
//
// The design has three pieces:
//
//   - Embedder: turns text into vectors. Backends are pluggable — a local ONNX
//     model (CGo, behind the `onnx` build tag), or an HTTP API (OpenAI-shaped,
//     which also covers Ollama and any OpenAI-compatible endpoint).
//   - Store: a content-hash-keyed cache of vectors that round-trips to disk as a
//     text manifest plus a packed-float32 blob, so it can be committed to git and
//     regenerated incrementally.
//   - Scorer: embeds the query once, then ranks documents against cached vectors.
//
// No backend is contacted unless one is explicitly configured, and the API key
// is only ever read from configuration the caller resolves (env / human / CLI),
// never from an MCP tool argument. This keeps the umbrella's "the agent gets
// capabilities, not credentials" boundary intact.
package semantic

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// Vector is a dense embedding. All vectors in a given Store share a dimension.
type Vector []float32

// ErrUnavailable is returned by a backend that is configured but cannot run in
// this build or environment (e.g. the ONNX backend without the `onnx` build
// tag, or a model file that is absent). Callers treat it as "fall back to
// lexical" rather than a hard error.
var ErrUnavailable = errors.New("semantic: embedder unavailable")

// Embedder turns text into vectors. Implementations must be safe for
// sequential use; the recommender embeds the query once and reads cached
// document vectors, so high concurrency is not required.
type Embedder interface {
	// Embed returns one vector per input text, in order.
	Embed(ctx context.Context, texts []string) ([]Vector, error)
	// ID uniquely identifies the model+config so cached vectors from a
	// different model are never compared against this one. It is mixed into
	// every content hash.
	ID() string
	// Dim is the vector dimension, or 0 if not yet known (some backends learn
	// it from the first response).
	Dim() int
}

// Backend names accepted by New.
const (
	BackendNone   = ""       // disabled — recommender stays lexical-only
	BackendOpenAI = "openai" // OpenAI / any OpenAI-compatible /v1/embeddings
	BackendOllama = "ollama" // local Ollama /api/embeddings
	BackendONNX   = "onnx"   // bundled local ONNX model (requires `onnx` build tag)
)

// Config selects and parameterises an embedder. The zero value (BackendNone)
// disables semantic scoring.
type Config struct {
	Backend string // one of the Backend* constants
	Model   string // model name/id or path to a local model directory
	BaseURL string // HTTP backends: API base (defaults per backend)
	APIKey  string // HTTP backends: bearer token; caller-resolved, never logged
}

// New constructs an embedder for the given config. A BackendNone config returns
// (nil, nil) so callers can write `emb, _ := semantic.New(cfg)` and treat a nil
// embedder as "lexical only". An unknown backend is an error.
func New(cfg Config) (Embedder, error) {
	switch cfg.Backend {
	case BackendNone:
		return nil, nil
	case BackendOpenAI:
		return newHTTPEmbedder(httpOpenAI, cfg)
	case BackendOllama:
		return newHTTPEmbedder(httpOllama, cfg)
	case BackendONNX:
		return newONNXEmbedder(cfg) // build-tagged; stub returns ErrUnavailable
	default:
		return nil, fmt.Errorf("semantic: unknown backend %q", cfg.Backend)
	}
}

// cosine returns the cosine similarity of a and b in [-1, 1]. Mismatched or
// zero-length/zero-norm vectors return 0. For embedding models trained with a
// cosine objective the practical range on related text is roughly [0, 1].
func cosine(a, b Vector) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		av, bv := float64(a[i]), float64(b[i])
		dot += av * bv
		na += av * av
		nb += bv * bv
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
