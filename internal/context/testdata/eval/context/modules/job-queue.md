---
tags: [jobs, job, queue, worker, background, scheduler, retry, retries]
related_paths:
  - internal/jobs/**
owner: platform-team
last_reviewed: 2026-02-28
---

# Background Job Queue

## What it does
A Postgres-backed at-least-once work queue. Producers insert a row; workers
lease rows with `FOR UPDATE SKIP LOCKED`, renew the lease every 30s, and
delete on success.

## Adding a new background job
1. Register a handler in `internal/jobs/registry.go` under a stable job name.
   The name is persisted, so renaming it orphans in-flight rows.
2. Declare `MaxAttempts` and whether the job is idempotent. Non-idempotent
   handlers must take their own lock; at-least-once means your handler *will*
   run twice eventually.
3. Add the job name to the queue dashboard config so it is not invisible.

## Retries and the dead letter queue
A failed job is retried with exponential backoff (30s base, doubling, 6h cap)
until `MaxAttempts`, then moved to the dead letter table. Dead letters are
never retried automatically.

## Stuck jobs
The usual cause of a queue that stops draining is a worker that died holding a
lease: the row stays leased until expiry, and if the handler deadlocks the
lease is renewed forever. Look for jobs whose `leased_until` keeps advancing
while `attempts` never increments. The reaper only reclaims expired leases, so
a live-but-wedged worker is invisible to it.

## Not in scope
Domain-specific retry policy. Billing's charge schedule, for example, is owned
by `payment-retry`; the queue only executes what it is given.
