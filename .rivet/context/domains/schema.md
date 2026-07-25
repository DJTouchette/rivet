---
tags: [schema]
owner: djtouchette
last_reviewed: 2026-07-24
related_paths:
  - "internal/schema/**"
---

# Schema

Source: `internal/schema/` (36 files)

## High-risk files

- `internal/schema/types/types.go` — fan-in: 27, churn: 1, score: 0.50
- `internal/schema/config/config.go` — fan-in: 8, churn: 1, score: 0.15
- `internal/schema/catalog/catalog.go` — fan-in: 5, churn: 1, score: 0.09

## Overview

Database intelligence: which indexes are unused, redundant, or missing, and
which application queries hit which tables. It is **strictly read-only** — every
driver query targets system catalogs and stats views, never application data.

Four independent inputs feed one set of pure analyzers: a live catalog snapshot
(Postgres/MSSQL), on-disk SQL migrations, SQL extracted from application source,
and the engine's own missing-index hints. The subsystem is deliberately shaped
like a separate repo — its own cobra tree, its own config package — so it can be
extracted later.

## Key modules

- `internal/schema/schema.go` — the rivet shim: `NewCommand` and the in-process `Run`
- `internal/schema/cli/runtime.go` — `loadOrFetch`, `pullSnapshot`, the 30s per-call timeout
- `internal/schema/catalog/catalog.go` — the driver-neutral `Catalog` interface and `init()`-time registry
- `internal/schema/cache/cache.go` — one JSON snapshot per database under `.rivet/schema/`
- `internal/schema/migrations/migrations.go` — regex-based static schema reconstruction
- `internal/schema/queryextract/` — SQL extraction for C#/Dapper, Go, Python, Node
- `internal/schema/analyze/analyze.go` — pure functions over the above; no I/O

## Failure modes

- `pullSnapshot` hard-fails on `Ping`, `LoadSchema` or `IndexUsage`, but
  **swallows** errors from `MissingIndexHints` and `SlowQueries` and stores nil.
  A missing `pg_stat_statements` therefore looks exactly like a quiet server.
- `migrations.Parse` records a filename in `Unparsed` and continues when it
  meets SQL it doesn't understand; only a filesystem read error aborts. A
  half-parsed schema is reported as success with a count you have to look at.
- Every catalog call is capped at 30s (`ctxTimeout`), so a forgotten VPN fails
  rather than hangs.

## Gotchas

- **The `schema:` YAML is parsed by a different package.**
  `internal/config.Config` has **no** `Schema` field at all; the section is read
  separately by `Load` in `internal/schema/config/config.go`. Concluding schema settings
  don't exist because they're absent from the main config struct is the single
  easiest wrong turn here — and it is why `rivet project register-cli` silently
  deletes the section (see [[cli]]).
- **The snapshot cache has no TTL.** `loadOrFetch` returns the cached entry if
  the file merely *exists*. `FetchedAt` is written and never read anywhere in
  the repo. Once `.rivet/schema/<name>.json` exists, nothing re-dials the
  database until someone runs `schema refresh`, so stale index-usage counters
  are the default state, not an edge case.
- **`schema overview` never connects.** It reports `Connected: true` purely
  because a snapshot file loaded. "connected" there means "we have a snapshot".
- **Migrations are not a fallback.** `schema migrations` reconstructs a schema
  with no DB, but `tables`, `describe`, `indexes *`, `coverage` and
  `queries slow` all go through `loadOrFetch` and fail without a snapshot. They
  never fall back to the migration-derived schema. `schema migrations` also uses
  only `AllDirs()[0]`, and `schema overview` stops at the first directory that
  parses — extra migration dirs are silently ignored.
- **`schema queries slow --limit N` can only shrink the result.**
  `pullSnapshot` hard-codes `cat.SlowQueries(ctx, 25)`, and the flag just slices
  the cached 25 down. Asking for 50 gets you 25.
- Every `schema.*` capability is `SafetyLevelSafe`, including `schema.refresh` —
  the one command that always opens a connection. That is deliberate (read-only
  catalog queries), but it does mean an agent can dial your configured database
  without an approval step. See [[capabilities]].
- Read-only is a **convention enforced by the drivers**, not a session setting.
  Nothing issues `SET default_transaction_read_only`; the guarantee is that
  `internal/schema/catalog/postgres/postgres.go` and its MSSQL sibling only ever
  issue `SELECT`s against catalogs. Adding a mutating query would compile fine.
- Migration files are applied in `sort.Strings` order over the **full recursive
  path**, so non-zero-padded prefixes replay out of order (`10_x.sql` before
  `9_x.sql`).
- `analyze.Run`/`analyze.Inputs` exist but nothing calls them; the CLI wires
  `DetectUnused`/`DetectRedundant`/`DetectMissing`/`BuildCoverage` directly.
  Changing `Run` changes nothing that ships.
