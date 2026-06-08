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
  compiled only with `-tags onnx`. It auto-discovers the model's input/output
  names and mean-pools + L2-normalises the output. Without the tag the backend
  reports unavailable and the recommender stays lexical. See the setup below.
- **ollama** — local Ollama daemon (`nomic-embed-text` by default). No keys, no
  egress, but a running daemon.
- **openai** — OpenAI or any OpenAI-compatible `/v1/embeddings` endpoint
  (`text-embedding-3-small` by default). Trivial cost, but sends text off-box.

## Setting up the local ONNX backend

The `onnx` backend is opt-in: the heavy `onnxruntime_go` dependency (it bundles
~70MB of test runtimes) is intentionally kept out of `go.mod`, so the default
build stays lean. To enable it (verified with ONNX Runtime 1.26.0 +
`bge-small-en-v1.5`):

```bash
# 1. Add the binding and build rivet with the onnx tag.
go get github.com/yalue/onnxruntime_go
CGO_ENABLED=1 go build -tags onnx -o rivet ./cmd/rivet

# 2. Get the ONNX Runtime shared library. Its version MUST match the binding's
#    expected API version (v1.31.0 of the binding wants ORT 1.26.0).
curl -sSL https://github.com/microsoft/onnxruntime/releases/download/v1.26.0/onnxruntime-linux-x64-1.26.0.tgz | tar xz

# 3. Get a sentence-embedding model (model.onnx + vocab.txt).
mkdir -p ~/models/bge-small-en-v1.5 && cd ~/models/bge-small-en-v1.5
curl -sSLO https://huggingface.co/Xenova/bge-small-en-v1.5/resolve/main/onnx/model.onnx
curl -sSLO https://huggingface.co/Xenova/bge-small-en-v1.5/resolve/main/vocab.txt

# 4. Point rivet at them and index.
export RIVET_EMBED_BACKEND=onnx
export RIVET_EMBED_MODEL=~/models/bge-small-en-v1.5
export RIVET_EMBED_ORT_LIB=/path/to/libonnxruntime.so.1.26.0
rivet context index
```

For the MCP server, set the same three env vars in the rivet entry of your
`.mcp.json` (`"env": { ... }`). If ORT and the binding versions disagree you'll
see "The requested API version [N] is not available" — fix by matching the ORT
release to the binding (`go list -m -versions github.com/yalue/onnxruntime_go`).

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
