---
tags: [search, relevance, ranking, catalog, catalogue, query, facets]
related_paths:
  - services/search/**
owner: discovery-team
last_reviewed: 2026-02-20
---

# Product Search Domain

## Purpose
Turns a shopper's text into a ranked list of catalogue products. Owns the
query pipeline and the relevance model; does not own the index build.

## Relevance model
Ranking is a linear blend of four signals, computed at query time:
- BM25 over title and description
- category affinity from the shopper's recent sessions
- in-stock boost (out-of-stock products are demoted, never filtered)
- merchandising overrides, which are hard pins and bypass the blend

Weights are configuration, not code, and are reviewed monthly. A relevance
change ships behind a flag and is judged on the offline judgement set before
it goes live.

## Query handling
Queries are lowercased, ASCII-folded, and spell-corrected against the
catalogue vocabulary. Facet selection is applied as a filter after ranking so
facet counts stay stable.

## Boundaries
The pipeline that populates the index — mapping changes, backfills, reindexes
— belongs to `search-indexer`.
