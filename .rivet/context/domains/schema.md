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

- `pullSnapshot` hard-fails on `Ping`, `LoadSchema` or `IndexUsage`, but treats
  the optional sections — `MissingIndexHints`, `SlowQueries` — as recoverable,
  recording a `cache.Gap` rather than failing the whole snapshot. That split is
  deliberate: pg_qualstats and pg_stat_statements are absent on plenty of
  healthy databases, so hard-failing would take `tables` and `describe` down
  with them. Gaps persist into the snapshot file, so a cached read tomorrow sees
  the same hole instead of a zero.
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
- **An empty result and an uncaptured one are different answers.** A gap on
  `slow_queries` or `missing_index_hints` means "unknown", not "none", and the
  commands whose entire output *is* that section say so explicitly. Gaps carry a
  `Kind`: `unavailable` (the driver reported `catalog.ErrNotSupported` — a fact
  about the database, no action) versus `failed` (permission denied, broken
  stats view, timeout — somebody's bug). The Postgres driver used to return
  `(nil, nil)` for both a missing extension and a failed query, so the CLI could
  not have distinguished them even if it had tried.
- **Snapshots expire, and the age is always on screen.** `loadOrFetch` checks
  `FetchedAt` against `schema.cache.max_age` (default 24h; `0s` means never read
  from cache). A missing `FetchedAt` counts as stale — an unknown age is not a
  safe age — and a future timestamp clamps to zero rather than going negative on
  clock skew. Every cached-data command prints the snapshot's age, inline on
  stdout in human mode and on stderr in JSON mode so stdout stays one parseable
  document. Don't add a command that reads a snapshot without reporting
  freshness; that was the original bug, where weeks-old index counters looked
  exactly like live ones.
- **Unreachable DB plus an existing snapshot serves the snapshot, loudly.** A
  deliberate choice: schema questions are mostly still answerable from old data,
  so labelled-stale beats a hard error. No snapshot and no DB *is* an error.
- **`schema overview` pings; everything else doesn't.** `Connected` used to be
  set purely because a snapshot file loaded, claiming a connection never made.
  It now reflects a real ping, on a shorter timeout than the query path because
  overview dials every configured database.
- **Migrations are not a fallback.** `schema migrations` reconstructs a schema
  with no DB, but `tables`, `describe`, `indexes *`, `coverage` and
  `queries slow` all go through `loadOrFetch` and fail without a snapshot. They
  never fall back to the migration-derived schema.
- **Multiple migration roots merge; they do not interleave.** `ParseAll` replays
  roots in configured order (`dir`, then `dirs`), lexical within a root, sharing
  one parser so a later root can `ALTER` an earlier root's table. Filenames are
  deliberately *not* sorted globally across roots — two roots are two
  independent numbering spaces, and a global sort would invent an order nobody
  wrote. A filename appearing in two roots is reported in `Summary.Warnings`
  rather than silently resolved, and a root that fails to read is reported
  per-root while the rest still merge.
- **`--limit` is a capture-time parameter, not a display one.** `pullSnapshot`
  once hard-coded `cat.SlowQueries(ctx, 25)`, so the flag could only slice the
  cached 25 down and asking for 50 got you 25. The requested limit now travels
  with the request and is recorded on the entry, so a snapshot captured with a
  smaller limit is *insufficient* for a larger one and re-dials — a second gate
  entirely separate from freshness. A capture never takes fewer than 25 rows, so
  an incidental `--limit 1` can't poison the shared snapshot.
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
