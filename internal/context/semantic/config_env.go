package semantic

import (
	"context"
	"os"
)

// Environment variables that configure the optional embedder. They are read by
// the CLI and MCP server. The API key is intentionally an environment variable
// (human/CI provided) and never an MCP tool argument — the agent gets the
// retrieval capability, not the credential.
const (
	EnvBackend = "RIVET_EMBED_BACKEND" // "", "openai", "ollama", "onnx"
	EnvModel   = "RIVET_EMBED_MODEL"   // model name, or path to a local ONNX model dir
	EnvBaseURL = "RIVET_EMBED_BASE_URL"
	EnvAPIKey  = "RIVET_EMBED_API_KEY"
)

// DefaultStoreDir is where precomputed embeddings live, relative to the repo
// root. It is meant to be committed to git.
const DefaultStoreDir = ".rivet/embeddings"

// ConfigFromEnv builds a Config from the RIVET_EMBED_* environment. A config
// with an empty Backend (the default) disables semantic scoring.
func ConfigFromEnv() Config {
	return Config{
		Backend: os.Getenv(EnvBackend),
		Model:   os.Getenv(EnvModel),
		BaseURL: os.Getenv(EnvBaseURL),
		APIKey:  os.Getenv(EnvAPIKey),
	}
}

// OpenScorer builds a query-time Scorer from a config and a store directory, or
// returns (nil, nil) when no backend is configured — the signal that callers
// should stay lexical-only. The store is opened (created empty if absent) so a
// committed embedding cache is reused and query-time misses can be filled.
//
// A nil Scorer is safe to pass to context.WithSemantic.
func OpenScorer(ctx context.Context, cfg Config, storeDir string) (*Scorer, error) {
	emb, err := New(cfg)
	if err != nil {
		return nil, err
	}
	if emb == nil {
		return nil, nil // disabled
	}
	store, err := OpenStore(storeDir, emb.ID(), emb.Dim())
	if err != nil {
		return nil, err
	}
	return NewScorer(ctx, emb, store), nil
}
