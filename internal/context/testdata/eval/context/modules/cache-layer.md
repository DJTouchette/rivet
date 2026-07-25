---
tags: [cache, caching, redis, invalidation, ttl, read-through, memoize]
related_paths:
  - internal/cache/**
owner: platform-team
last_reviewed: 2026-01-18
---

# Read-Through Cache Layer

## What it does
The generic read-through cache used by every service. Callers supply a key
builder and a loader; the layer handles TTL, negative caching, and single
flight so a cold key does not stampede the database.

## Invalidation strategy
Invalidation is write-through by key prefix, not broadcast. When a product
changes, the writer deletes `product:{id}:*`. There is deliberately no
cross-service invalidation bus: a service that caches another service's data
must accept staleness bounded by its own TTL.

Never invalidate by scanning. `SCAN` over a hot keyspace has taken production
down twice; use a known prefix or accept the TTL.

## TTL guidance
- Product listings: 300s. Catalogue writes are rare and staleness is cheap.
- Pricing: 30s. Staleness is visible to the shopper at checkout.
- Anything user-specific: do not put it here, use a request-scoped cache.

## Negative caching
Misses are cached for 10s to protect against enumeration scans. Negative
entries are dropped immediately on a successful write to the same key.
