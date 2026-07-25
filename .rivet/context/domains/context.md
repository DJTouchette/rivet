---
tags: [context, retrieval, recommend, learnings, wiki, runbooks, embeddings, curated, weighting, scoring]
owner: djtouchette
last_reviewed: 2026-07-24
related_paths:
  - "internal/context/**"
---

# Context

Source: `internal/context/` (37 files)

## High-risk files

- `internal/context/recommend.go` — fan-in: 12, churn: 4, score: 0.89
- `internal/context/context.go` — fan-in: 12, churn: 4, score: 0.89
- `internal/context/lint.go` — fan-in: 12, churn: 3, score: 0.67
- `internal/context/runbook.go` — fan-in: 12, churn: 1, score: 0.22
- `internal/context/learnings.go` — fan-in: 12, churn: 1, score: 0.22
- `internal/context/codedocs.go` — fan-in: 12, churn: 1, score: 0.22

## Overview

The knowledge layer — the reason rivet exists. Four document tiers feed one
retrieval engine, all reduced to the same `Document` type distinguished by
`Kind`:

1. **Curated context** (`.rivet/context/{domains,modules,paradigms}`) — "what must
   I know to change this code safely?" Highest weight.
2. **Code-extracted** — `rivet:context` comments and `.context/` sidecars, pulled
   from recon's index at server startup. Just below curated.
3. **Wiki** (`.rivet/wiki/` or `wiki_paths`) — free-form narrative and reference.
   Down-weighted so it augments rather than outranks code-adjacent context.
4. **Runbooks** (`.rivet/runbooks/`) — trigger-keyed procedures, reached
   deliberately by symptom through their own tool rather than by ranking.

Separately, the **learning log** (`.rivet/learnings/*.md`) is capture, not
retrieval: one file per entry, `promoted: false` until a human-reviewed
promotion pass folds it into a curated doc.

## Key modules

- `recommend.go` — the scorer; lexical signals plus an optional semantic one
- `context.go` — `Document`, `Kind`, frontmatter loading
- `learnings.go` — `CreateLearning`, `CountActive`, `MarkPromoted`, `ArchiveLearning`
- `wiki.go`, `runbook.go`, `codedocs.go` — the other three tiers
- `semantic/` — embedding backends (onnx/ollama/openai) and the committable vector cache
- `lint.go` — staleness and quality checks, incl. runbook `last_tested`
- `links.go` — `[[wikilink]]` extraction, resolution and rendering

## Failure modes

- Every loader degrades to nil on error; the server starts with fewer tiers
  rather than failing. A malformed doc silently disappears from retrieval.
- If the embedder is unavailable, recommendation silently stays lexical. Silent
  is deliberate, but it means "semantic search isn't working" looks identical to
  "semantic search is off".
- Runbook drafts are **not retrievable** until promoted. A wrong runbook followed
  under pressure is worse than none, so the gate is intentional.

## Gotchas

- **Scoring is additive and clamped at 1.0.** Signals (tag 0.5/0.6, name
  0.4/0.5/0.6, path, body, `semanticWeight` 0.45) sum, then multiply by
  `kindWeight`, then clamp. The clamp means docs that saturate at 1.0 stop being
  distinguishable from each other — discrimination is worst exactly among the
  top results, where it matters most. Know this before "just tuning a weight".
- The constants are hand-tuned with no derivation. `eval_test.go` is the
  regression ratchet — run it before and after any scoring change; do not adjust
  a weight on intuition alone.
- `kindWeight` is what keeps a wiki page from outranking a curated domain doc
  that shares vocabulary. Adding a new `Kind` without adding it to `kindWeight`
  gives it full curated weight by default.
- Path matching only runs when the query looks like a path, so `related_paths`
  globs are dead weight for prose queries.
- The embedding cache (`.rivet/embeddings/`) is deterministic and **meant to be
  committed**; the recon cache (`.rivet/recon/`) is derived and gitignored. Don't
  conflate them.
- `CountActive` counts files *without* `promoted: true` — that is what the
  promotion nudge in [[mcp]] thresholds against.
- **Lint is corpus-wide, not per-doc.** `broken-wikilink` and `duplicate-name`
  need every name up front, so `Lint` builds the index and hands it to
  `lintDoc`. Passing nil skips link checking rather than reporting every link as
  broken — relevant if you ever lint a single doc in isolation.
- **`looksLikeFilePath` is deliberately conservative**, and the interesting part
  is why it *rejects* things. Backticks in prose hold flags, arity notation,
  module refs and MIME types; a false positive invents a "stale reference" for
  something that was never a path. A candidate qualifies via a conventional
  source prefix (which fires whether or not the dir exists — a reference into a
  missing lib directory *is* stale) or a first segment that's a real directory.
  Dot-directories are excluded: docs legitimately mention `.rivet/embeddings/`
  before anything creates it.
- Lint exits non-zero on errors, or on anything with `--strict`. CI runs
  `--strict` against this repo's own docs, so a broken link fails the build.
