---
tags: [postmortem, incident, outage, checkout, orders, cache]
last_reviewed: 2025-11-14
---

# Postmortem: Checkout Outage, 2025-11-08

**Impact** — checkout returned 500 for 41 minutes; roughly 12k carts affected.

## Timeline
- 14:02 A merchandising job deleted a product price key and its negative
  cache entry in the wrong order.
- 14:03 Every checkout began loading pricing from the database directly.
- 14:07 The pricing database hit connection exhaustion; checkout 500s.
- 14:31 Cause identified from the cache miss rate graph.
- 14:43 Job disabled, cache warmed, error rate back to baseline.

## Root cause
The negative cache entry was written after the positive key was deleted, so
every read stampeded the loader. Single flight should have absorbed this, but
it keys on the cache key and the loader was called with a different key shape.

## What we changed
- Single flight now keys on the loader's arguments.
- Merchandising writes go through the cache layer's invalidate helper.

## What we did not change
The 30s pricing TTL. Shorter TTLs were discussed and rejected; the incident
was a stampede, not a staleness problem.
