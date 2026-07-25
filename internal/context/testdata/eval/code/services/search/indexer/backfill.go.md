---
related_paths:
  - services/search/indexer/backfill.go
---

# Reindex backfill batching

`Backfill` walks the catalogue by primary key in batches of 500 and bulk
writes into the target index named by the caller, never into the alias.

The inter-batch pause is applied by the caller's rate limiter, not here. A
retry wrapper placed around `Backfill` therefore bypasses the pause entirely
and will drive the cluster into bulk rejections — wrap the batch, not the
walk.

`resumeFrom` makes the walk restartable: it is the last primary key written,
persisted after every batch.
