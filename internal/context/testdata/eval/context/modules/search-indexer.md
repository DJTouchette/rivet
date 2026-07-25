---
tags: [search, index, indexer, reindex, backfill, elasticsearch, mapping]
related_paths:
  - services/search/indexer/**
owner: discovery-team
last_reviewed: 2026-03-09
---

# Search Index Pipeline

## What it does
Keeps the Elasticsearch product index in step with the catalogue. Two paths
feed it: a live change stream for incremental updates, and a batch backfill
used for reindexes.

## Reindexing the catalogue
Reindex is always blue/green against an alias. Never write a mapping change
into the live index.
1. Create `products-{date}` with the new mapping.
2. Run the backfill in batches of 500 with a 200ms pause; a full catalogue is
   roughly 40 minutes.
3. Let the live change stream double-write to both indices for the duration.
4. Flip the `products` alias, then delete the old index after 24h.

## Mapping changes
Any mapping change is breaking. Analyzer changes silently alter relevance
without erroring, which is worse than a hard failure, so every analyzer change
must be scored against the offline judgement set before the alias flip.

## Failure modes
A stalled backfill is usually the batch pause being swallowed by a retry loop,
which hammers the cluster into rejecting bulk writes. Watch bulk rejection
count, not indexing throughput.
