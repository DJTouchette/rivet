# semantic — optional embedding-based context retrieval

This package adds a **semantic-match** signal to rivet's context recommender.
It is purely additive: with nothing configured, `rivet context recommend` and
the `rivet.context-recommend` MCP tool behave exactly as before (lexical only).

## How it fits

`context.Recommend` scores documents on four lexical signals (tag, path, name,
body). When an embedder is configured, a fifth signal is added:

    score += 0.45 * cosine(query, doc)

The query is embedded once per call; document vectors come from a precomputed,
git-committable cache. If the embedder is unavailable or a vector is missing,
that document is simply scored lexically — there is no hard failure mode.

## Backends

Set via environment (the API key is an env var / human concern, never an MCP
tool argument — the agent gets retrieval, not the credential):

| Variable | Meaning |
|---|---|
| `RIVET_EMBED_BACKEND` | `onnx` \| `ollama` \| `openai` (unset = disabled) |
| `RIVET_EMBED_MODEL`   | model name, or path to a local ONNX model dir |
| `RIVET_EMBED_BASE_URL`| override API/daemon base URL |
| `RIVET_EMBED_API_KEY` | bearer token for an HTTP API |

- **onnx** — bundled local model, fully offline. Real code in `onnx.go`,
  compiled only with `-tags onnx` (needs `github.com/yalue/onnxruntime_go`, the
  ONNX Runtime C library, and a model dir with `model.onnx` + `vocab.txt`, e.g.
  `bge-small-en-v1.5`). Without the tag the backend reports unavailable and the
  recommender stays lexical.
- **ollama** — local Ollama daemon (`nomic-embed-text` by default). No keys, no
  egress, but a running daemon.
- **openai** — OpenAI or any OpenAI-compatible `/v1/embeddings` endpoint
  (`text-embedding-3-small` by default). Trivial cost, but sends text off-box.

## The cache (committable to git)

`rivet context index` embeds every context document into `.rivet/embeddings/`:

- `manifest.json` — sorted `contentHash -> slot`, plus model id and dimension.
- `vectors.bin` — packed little-endian float32, `dim` per vector.

The layout is a deterministic function of content, so two machines indexing the
same corpus produce byte-identical files — safe to commit and diff. Re-running
only embeds new or changed chunks. Switching models invalidates the cache
automatically (the model id is mixed into every hash and checked on load).

Long documents (a wiki) are split into overlapping ~320-word chunks; a doc's
score is the best cosine over its chunks.

## Quick start

    export RIVET_EMBED_BACKEND=ollama        # or onnx / openai
    rivet context index                      # writes .rivet/embeddings/, commit it
    rivet context recommend "customer refund dispute"
