---
tags: [onboarding, setup, dev-environment, tooling]
last_reviewed: 2026-01-05
---

# Engineering Onboarding

Welcome. Your first week, in order.

## Day 1 — access
Request GitHub, the cloud console and the observability stack in the access
portal. Your manager approves. Do not wait on these before starting day 2.

## Day 2 — local environment
Clone the monorepo, run `make bootstrap`, then `make up`. That brings up
Postgres, Redis and the local Elasticsearch node in containers. If `make up`
hangs on the Elasticsearch health check, raise your Docker memory limit to 8GB
— that is the usual cause.

## Day 3 — read the context docs
Read the domain docs first (billing, orders, auth, search), then the paradigm
docs. The module docs are worth skimming but you will retain them better once
you have a reason to open one.

## Day 4 — first change
Pick a `good-first-issue`, open a draft PR early, ask for a review before you
think it is ready. Deploys are boring here on purpose; shipping on your first
week is normal.

## Who to ask
Each doc names an owner in its frontmatter. Ask them directly.
