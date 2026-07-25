---
tags: [session, sessions, cache, redis, ttl, expiry, auth]
related_paths:
  - internal/cache/session/**
owner: platform-team
last_reviewed: 2026-03-05
---

# Session Store and Cache

## What it does
Stores and reads back the server-side session records described by the `auth`
domain. Redis is the system of record for sessions; Postgres holds only an
audit trail.

## Expiry
Two clocks, and confusing them is the classic bug:
- Absolute expiry: 30 days from creation. Enforced as the Redis key TTL.
- Idle expiry: 12 hours since last use. Enforced by a `last_seen` field that
  is rewritten on read, which also refreshes the TTL.

A session that looks like it expired early has almost always hit idle expiry
while the absolute clock still had weeks left.

## Read path
Reads are single-key GETs and must stay that way; the session lookup is on
every authenticated request and the p99 budget is 2ms.

## Invalidation
Revocation deletes the key outright rather than marking it. There is no
tombstone, so a delete that races an in-flight refresh can resurrect a
session for up to one request. Accepted, documented, tolerated.
