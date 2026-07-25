---
title: Enable semantic search (embeddings)
triggers:
  - enable semantic search
  - set up embeddings
  - semantic search not working
  - set up ollama
  - set up onnx
  - context recommend only lexical
  - turn on embeddings
  - no semantic-match signal
severity: low
owner: rivet
last_tested: 2026-07-25
---

# Enable semantic search (embeddings)

By default `rivet.context-recommend` matches lexically — shared words, tags,
paths. That misses the questions people actually ask before they know the
vocabulary of the code. Adding an embedding signal makes it match on *meaning*
as well. It is purely additive: every lexical signal still applies.

Measured on a 13k-file C# repo, lexical score → with embeddings:

| Query | Lexical | Semantic |
|---|---|---|
| "what happens when a lab result comes back" | 0.21 | 0.46 |
| "where does the nightly billing run start" | 0.21 | 0.45 |
| "how do we stop a certificate being issued twice" | 0.16 | 0.41 |

The third one also changed its *answer* — lexical picked a wiki page, semantic
picked the domain doc that actually documents idempotency.

## Steps (Ollama — start here)

This works with the stock `rivet` binary. No build, no keys, nothing leaves the
box. Two commands.

1. **Run the daemon and pull the model.** The daemon being up is not enough —
   `ollama list` must show the model, or every embed call returns a 404.
   ```bash
   ollama serve &                    # or: systemctl --user start ollama
   ollama pull nomic-embed-text      # ~274 MB
   ollama list                       # confirm it is there
   ```

2. **Point rivet at it and index.**
   ```bash
   export RIVET_EMBED_BACKEND=ollama
   rivet context index               # embeds context + wiki + runbooks
   ```

   For the MCP server (Claude Code), add `RIVET_EMBED_BACKEND=ollama` to the
   `rivet` entry's `"env": {}` block in `.mcp.json` — the CLI and the MCP server
   read the environment separately, and exporting it in your shell does **not**
   reach the server Claude Code spawns. This is the most common way the index
   ends up built but never read.

   Commit `.rivet/embeddings/` — it is deterministic and keyed by model, so
   teammates reuse the vectors and only need the daemon running.

## Verification

```bash
rivet doctor | grep semantic
# OK  semantic search  ollama reachable, 411 cached vectors match http:...:nomic-embed-text
```

`rivet doctor` contacts the backend rather than trusting the env var, so a FAIL
here is a real failure with the real error attached. Then check the signal:

```bash
rivet context recommend "how do we stop unpaid accounts from logging in"
# each result's "signals:" line should include semantic-match
```

If the ranking is degraded, retrieval still works — it falls back to lexical —
but it now tells you so on stderr and in the MCP response, rather than
returning a quietly weaker answer.

## Common failures

| Symptom | Cause |
|---|---|
| doctor FAIL, `model "..." not found` | daemon up, model not pulled — `ollama pull nomic-embed-text` |
| doctor FAIL, `connection refused` | daemon not running |
| doctor WARN, `built by "..." but configured backend is "..."` | the index and the backend disagree; the model ID embeds the base URL, so changing `RIVET_EMBED_BASE_URL` alone invalidates the cache. Re-run `rivet context index` or point the URL back. |
| doctor WARN, backend unset but index present | set in your shell but not in `.mcp.json`, or vice versa |
| no `semantic-match` on any result | index never built — `rivet context index` |

## Rollback

Unset `RIVET_EMBED_BACKEND` (or remove it from `.mcp.json`). Retrieval falls back
to lexical; the committed `.rivet/embeddings/` is harmless when unused.

## Alternatives

- **OpenAI/compatible:** `RIVET_EMBED_BACKEND=openai` + `RIVET_EMBED_API_KEY`
  (`text-embedding-3-small`). Costs pennies to embed a corpus, but text leaves
  the box. Also works with the stock binary.

- **ONNX (fully offline, no daemon):** the only option with no background
  process and no network at all, at the cost of a source build. Steps below.

### ONNX setup

1. **Build rivet with the ONNX tag.** The runtime dependency is opt-in:
   ```bash
   go get github.com/yalue/onnxruntime_go
   CGO_ENABLED=1 go build -tags onnx -o "$(go env GOPATH)/bin/rivet" ./cmd/rivet
   ```

2. **Install the ONNX Runtime shared library.** Its version MUST match the
   binding's expected API version — binding v1.31.0 requires ONNX Runtime
   **1.26.0**. A mismatch shows "The requested API version [N] is not available".
   ```bash
   mkdir -p ~/.local/share/rivet/onnxruntime
   # Linux x64:
   curl -sSL https://github.com/microsoft/onnxruntime/releases/download/v1.26.0/onnxruntime-linux-x64-1.26.0.tgz \
     | tar xz -C /tmp && cp /tmp/onnxruntime-linux-x64-1.26.0/lib/libonnxruntime.so* ~/.local/share/rivet/onnxruntime/
   # macOS arm64: onnxruntime-osx-arm64-1.26.0.tgz  (lib is libonnxruntime.1.26.0.dylib)
   # macOS x64:   onnxruntime-osx-x86_64-1.26.0.tgz
   ```

3. **Download a sentence-embedding model** (model.onnx + vocab.txt). bge-small is
   a good default — small, fast, fully offline:
   ```bash
   M=~/.local/share/rivet/models/bge-small-en-v1.5; mkdir -p "$M"
   curl -sSL https://huggingface.co/Xenova/bge-small-en-v1.5/resolve/main/onnx/model.onnx -o "$M/model.onnx"
   curl -sSL https://huggingface.co/Xenova/bge-small-en-v1.5/resolve/main/vocab.txt   -o "$M/vocab.txt"
   ```

4. **Point rivet at them**, then `rivet context index` as above:
   ```bash
   export RIVET_EMBED_BACKEND=onnx
   export RIVET_EMBED_MODEL=~/.local/share/rivet/models/bge-small-en-v1.5
   export RIVET_EMBED_ORT_LIB=~/.local/share/rivet/onnxruntime/libonnxruntime.so.1.26.0
   ```
   Don't commit machine-specific absolute paths to a shared `.mcp.json` — set
   them per-developer.
