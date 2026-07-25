---
tags: [deploy, deployment, release, ci, cd, rollback, canary, pipeline]
related_paths:
  - .github/workflows/**
  - deploy/**
owner: platform-team
last_reviewed: 2026-03-01
---

# Deploy and Release Process

## Pipeline
Merge to `main` builds an immutable image, runs the full test suite, and
deploys to staging automatically. Production is a manual promotion of an
already-built image — production never builds from source.

## Canary
Production rollout is 5% for 10 minutes, then 50% for 10 minutes, then 100%.
The canary is judged on error rate and p99 latency against the baseline
fleet, automatically, and a breach halts the rollout in place.

## Rollback procedure
Rollback is a promotion of the previous image tag, not a revert commit.
1. `deploy/rollback.sh <service> <previous-tag>` — this is the fast path and
   takes about 90 seconds.
2. Only then open the revert PR, so `main` matches what is running.
3. Rolling back never rolls back a database migration. Migrations are
   expand-contract precisely so the previous image runs against the new
   schema.

## Migrations
Expand, deploy, backfill, contract — four separate deploys, never fewer. A
migration that is not backward compatible with the currently deployed image
will fail the pipeline's schema check.
