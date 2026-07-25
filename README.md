# Rivet

Your AI coding agent is smart. It's also completely clueless about your project.

It doesn't know that a critical retry workflow is split across two services. It doesn't know that a database trigger fires two queries and will absolutely ruin your day if you touch it wrong. It doesn't know your team's conventions, your pain points, or that one migration script that must never run on Tuesdays.

So it improvises. It guesses at SQL. It greps around hoping to stumble on the right file. It reinvents your deploy script from scratch every single time.

Rivet fixes this.

## What It Actually Does

Rivet is an MCP server that sits between Claude Code and your project. It gives the AI three things it desperately needs:

1. **Domain knowledge** that persists across sessions and improves over time
2. **Real tools** instead of shell improvisation
3. **Guardrails** so it doesn't do something profoundly stupid at 2am

The domain knowledge is the core of the whole thing. It's not static docs you write once and forget. It's a living system. Claude learns things while working, writes them down in the right place, and the next session starts smarter. The docs grow, get pruned when they're too long, and self-organize over time.

You seed it once. After that, it maintains itself.

## Quick Start

```bash
# Install rivet and its friends
go install github.com/djtouchette/rivet/cmd/rivet@latest
go install github.com/djtouchette/recon/cmd/recon@latest
go install github.com/djtouchette/witness/cmd/witness@latest
go install github.com/djtouchette/vaulty/cmd/vaulty@latest
go install github.com/djtouchette/rally/cmd/rally@latest

# Initialize in your project
cd your-project
rivet init

# Let Claude scaffold context docs from your codebase
# (run this inside Claude Code)
/rivet-setup
```

That's it. Open Claude Code and the MCP server starts automatically. Claude now has project-aware tools, your domain knowledge, and a feedback loop that gets smarter with use.

## The Context System

This is the heart of Rivet. Everything else exists to feed into this or act on it.

Context docs live in `.rivet/context/` and come in three flavors:

```
.rivet/context/
  domains/         ← business areas (orders, auth, scheduling)
  modules/         ← technical subsystems (search, data-sync)
  paradigms/       ← cross-cutting patterns (caching, event handling)
```

Each doc has frontmatter linking it to relevant files and tags:

```markdown
---
tags: [orders, invoicing, retries]
related_paths:
  - backend/Handlers/Workflow/**
  - clients/web/src/pages/Invoices/**
---

# Orders & Invoicing

## Overview
Handles invoice generation, retry logic, and failure handling...

## Failure modes
- Retries stop silently after the third attempt. Nothing alerts.

## Gotchas
- Retry logic is split between the scheduler and the adapter
- Status names in the DB don't match the API. Obviously.
```

Context docs are the **curated** tier: deliberately edited, kept short, reviewed. Claude doesn't scribble in them mid-task — raw findings land in a separate capture layer and get promoted in later, on purpose. That's [the feedback loop](#the-feedback-loop).

Context docs are exposed as MCP resources, so Claude can pull the right domain knowledge before making changes. The recommendation engine scores docs by tag matches, file path globs, and keyword relevance, so Claude doesn't have to guess which context to read.

### Context Docs in the Code Itself

Some context belongs next to the line it's about. Two in-repo forms feed the same retrieval engine, extracted deterministically by recon:

**`rivet:context` comments** — in any of 25+ languages. A marked comment directly above a declaration attaches to that symbol; `rivet:context(SymbolName)` attaches explicitly; otherwise it's file-level.

```go
// rivet:context
// Never call this inside a transaction — the retry
// scheduler owns rollback.
func ProcessPayment(o Order) error {
```

**`.context/` sidecar markdown** — one doc per source file, named after it:

```
src/orders/
  handler.go
  .context/
    handler.md      ← context doc for handler.go
```

These show up in `rivet.context-recommend` (weighted just below curated context docs, above wiki), as `rivet://code/...` resources, and through the dedicated `recon.docs` tool (`file:path` or `symbol:Name` to filter).

### Semantic Retrieval (optional)

By default, recommendation is **lexical** — it matches on shared words, tags, and paths. That misses conceptually-related docs that don't share vocabulary ("how do I authenticate" vs. a doc titled "OAuth setup"). Turn on **semantic retrieval** to add an embedding-based signal on top of the lexical ones. It's purely additive: with nothing configured, behavior is unchanged.

**Pick a backend** (set via environment — the API key is a human/CI concern, never an MCP tool argument, so the agent gets retrieval, not the credential):

| Variable | Meaning |
|---|---|
| `RIVET_EMBED_BACKEND` | `onnx` \| `ollama` \| `openai` (unset = disabled) |
| `RIVET_EMBED_MODEL` | model name, or path to a local ONNX model dir |
| `RIVET_EMBED_BASE_URL` | override API/daemon base URL |
| `RIVET_EMBED_API_KEY` | bearer token for an HTTP API |

- **`onnx`** — bundled local model, fully offline (no network, no keys). Built with `-tags onnx`; needs the ONNX Runtime library and a model dir (`model.onnx` + `vocab.txt`, e.g. `bge-small-en-v1.5`). The default build falls back to lexical if it's absent.
- **`ollama`** — local [Ollama](https://ollama.com) daemon (`nomic-embed-text`). No keys, no egress, but a running daemon.
- **`openai`** — OpenAI or any OpenAI-compatible `/v1/embeddings` endpoint (`text-embedding-3-small`). Embedding a corpus costs pennies, but text leaves the box.

**Precompute and commit the index:**

```bash
export RIVET_EMBED_BACKEND=ollama        # or onnx / openai
rivet context index                      # embeds docs into .rivet/embeddings/
rivet context recommend "customer refund dispute"
```

`rivet context index` writes a deterministic, **git-committable** cache (`.rivet/embeddings/manifest.json` + `vectors.bin`) — commit it so retrieval works offline without re-embedding. Re-running only embeds new or changed content; switching models invalidates the cache automatically. Long docs (a wiki) are chunked into overlapping passages, and a doc scores by its best-matching chunk. At query time only the query is embedded (one tiny call); matched docs gain a `semantic-match` signal in the output. If the embedder is unavailable, recommendation silently stays lexical.

### Wiki & Runbooks

Context docs are the *curated, code-adjacent* tier — "what must I know to change this code safely?" Two more tiers cover the rest of what devs and agents need, and both feed the same retrieval engine (and semantic index):

**Wiki** (`.rivet/wiki/`) — free-form reference and narrative: onboarding, architecture, the "why" behind decisions. Point rivet at docs you already have instead of re-authoring them — set `wiki_paths` in `.rivet/config.yaml` to a `docs/**` tree or a checked-out Azure DevOps wiki (which is just a git repo of markdown):

```yaml
context:
  wiki_paths:
    - "../project.wiki/**"   # your team pulls the latest; rivet just reads it
    - "docs/**"
```

Wiki docs show up in `rivet.context-recommend`, ranked *below* curated context so they augment rather than outrank it.

**Runbooks** (`.rivet/runbooks/`) — actionable, trigger-keyed procedures: "what do I DO when X happens?" They have their own tool because they're reached deliberately, by symptom:

```bash
rivet runbook find payments are failing      # → the matching procedure, by trigger
rivet runbook list                            # what's covered
```

```markdown
---
title: Payment webhook backlog recovery
triggers: [payments failing, webhook queue backlog]   # ← how it's found
severity: high
owner: payments-team
last_tested: 2026-05-01                                # runbooks rot dangerously
---
## Steps  ## Verification  ## Rollback  ## Escalation
```

Agents get a matching `rivet.runbook` tool (find-by-symptom / list) — runbooks are **guidance**, so any commands in them run through the agent's normal, overseen tools, never auto-executed. An agent that works through a novel operational problem can draft a runbook (`rivet.runbook-draft` → `.rivet/runbooks/drafts/`), but a draft is **never retrievable until a human reviews and promotes it** (`rivet runbook promote`) — a wrong runbook followed under pressure is worse than none. `rivet context lint` flags runbooks missing `triggers`/`owner` or with a stale/absent `last_tested`.

`rivet init` ships a couple of starter runbooks for operating rivet itself — including **Enable semantic search**, a step-by-step ONNX setup an agent can find (`rivet.runbook find "set up embeddings"`) and follow on a fresh, lexical-only install to turn on the embeddings above.

### The Feedback Loop

This is where it gets interesting.

```
You start a task
    → Claude investigates using recon tools (grep, symbols, related files)
    → A nudge fires: "hey, check the context docs first"
    → Claude reads the orders domain doc, finds the gotchas
    → Saves 10 minutes of blind exploration
    → Discovers something new during the task
    → Another nudge: "learned anything? write it down"
    → Claude calls rivet.learn → one small file in .rivet/learnings/
    → The log accumulates entries
    → /rivet-promote-learnings reviews them, merges the good ones
      into context docs, archives the rest
    → Next session starts with better knowledge
```

Capture and curation are deliberately **two separate layers**, because they want opposite things. Capture should be cheap and always-available; curation should be picky.

**Capture — `.rivet/learnings/`.** One file per entry, named `YYYY-MM-DD-<slug>-<id>.md` — so two agents recording findings at the same moment never collide on a shared section. Claude writes them with `rivet.learn` (title and observation required, everything else optional); you write them with `rivet learnings add`:

```markdown
---
title: ServiceRenderedInsertTrigger fires 5 queries per insert
date: 2026-04-01
confidence: high
suggested_doc: orders            # a hint, not a decision
related_paths:
  - backend/Handlers/Workflow/**
promoted: false                  # ← the whole state machine
---

# ServiceRenderedInsertTrigger fires 5 queries per insert

## Observation
## Impact
## Recommendation
```

**Curation — `/rivet-promote-learnings`.** A separate, deliberate pass. It walks the active entries and decides, one by one: promote, merge with a sibling entry, or archive. Promoting means the content gets merged into the right context doc as a concise bullet and the entry is stamped `promoted: true` / `promoted_to: <doc>` (`rivet learnings promote <name> --to <doc> --archive`, which can also move the file into `.rivet/learnings/archive/`).

Not every learning earns permanence. Most don't. `promoted: true` is what keeps a reviewed entry from being re-litigated every session, and it's what the "your log is getting long" nudge counts against.

The context files are just markdown in `.rivet/context/`, and so is the learning log. You can read them, edit them, commit them. They're not magic. They're documentation that happens to maintain itself.

### The Nudging System

Rivet doesn't just provide tools. It shapes how Claude uses them.

**Exploring with recon before reading any context doc:** "You're exploring blind. Check the context docs, someone already figured this out."

**A sustained investigation with nothing recorded:** "You've been digging for a while. Learn anything worth writing down? Call `rivet.learn`."

**A learning log with a real backlog of un-promoted entries:** "Time to review. Run `/rivet-promote-learnings`."

They come from the MCP server itself — it already sees every tool call — and they create a natural rhythm: check existing knowledge, investigate, record what you found, promote what mattered.

## Recon: Codebase Intelligence

Deterministic repo intelligence. No AI, just fast static analysis.

- Dependency graphs, import resolution, reverse lookups
- Symbol search across 10+ languages
- Co-change history (files that always change together)
- Hotspot detection (high fan-in + high churn = scary)
- Enriched grep that classifies results as definitions, references, tests, or comments
- File context with fan-in/fan-out metrics, ownership, nearby configs

Claude calls these instead of running raw `grep` and hoping for the best. Recon is what feeds the context system with real facts. It's the reason the feedback loop actually works: Claude can discover things deterministically instead of guessing, and then record those discoveries back into context.

**Tools exposed via MCP:**

| Tool | What it does |
|------|-------------|
| `recon.search` | Unified search across symbols, paths, and content. Start here. |
| `recon.grep` | Enriched grep with definition/reference/test/comment classification |
| `recon.related` | Files related to a path (imports, co-change, naming, test pairs) |
| `recon.symbols` | Search or list functions, types, classes |
| `recon.callers` | Where a symbol is defined, and every call site referencing it |
| `recon.context` | File preview, fan-in/fan-out, churn, hotspot score |
| `recon.hotspots` | Top files ranked by risk (fan-in * churn) |
| `recon.tests` | Find test files for a source file |
| `recon.docs` | Context docs living in the code: `rivet:context` comments + `.context/` sidecar markdown |
| `recon.overview` | Project structure, languages, frameworks, entrypoints |
| `recon.changes` | Recent git change summary |
| `recon.refresh` | Incremental cache update |

## Schema: Database Intelligence

Read-only database awareness for the live DB plus your migrations and application code.

- Connects over `database/sql` (pgx for Postgres, go-mssqldb for MSSQL) — only ever reads system catalogs and stats views
- Reconstructs a static schema from on-disk migrations when no DB is reachable
- Extracts SQL queries from C# (Dapper), Go (`database/sql`/sqlx/pgx), Python (psycopg/SQLAlchemy) and Node (pg/knex/prisma/tagged-template) sources
- Detects **unused** indexes from `pg_stat_user_indexes` / `sys.dm_db_index_usage_stats`
- Detects **redundant** indexes (column prefix of another index on the same table)
- Suggests **missing** indexes by combining engine hints (`sys.dm_db_missing_index_details`, pg_qualstats) with WHERE/JOIN/ORDER BY columns found in code
- Reports **coverage** — which queries hit which tables and whether their predicates land on an index

Configure connections in `.rivet/config.yaml`:

```yaml
schema:
  databases:
    - name: prod
      engine: postgres          # postgres | mssql
      host: db.example.com
      user: readonly
      password: ${SCHEMA_PW}
      database: production
      default: true
  migrations:
    dir: ./db/migrations
    dialect: postgres
  code_scan:
    roots: [./src, ./backend]
    languages: [csharp, go]      # optional filter
```

**Tools exposed via MCP:**

| Tool | What it does |
|------|-------------|
| `schema.overview` | Summary of configured databases + migration status |
| `schema.tables` | Tables with row estimates and sizes |
| `schema.describe` | Columns, indexes, FKs for one table |
| `schema.indexes-list` | Every index in a database |
| `schema.indexes-unused` | Indexes with zero reads (drop candidates) |
| `schema.indexes-redundant` | Indexes covered by another on the same table |
| `schema.indexes-missing` | Engine + code-analysis candidates to add |
| `schema.queries-extracted` | SQL statements found in app source |
| `schema.queries-slow` | Top expensive queries from the engine log |
| `schema.coverage` | Which predicates land on an index |
| `schema.migrations` | Static schema from SQL migration files |
| `schema.refresh` | Re-pull catalog snapshots from all databases |

## Witness: Test Selection

Smart test selection based on what actually changed.

- Maps changed files to relevant tests via the dependency graph
- Scores by distance: direct test > 1-hop import > 2-hop > co-change pattern
- Knows about Go, Elixir, Python, Ruby, Node, Rust test frameworks
- Stops traversing at high-fan-out boundaries so it doesn't suggest your entire test suite

**Tools exposed via MCP:**

| Tool | What it does |
|------|-------------|
| `witness.select` | Select tests for changed files (or auto-detect from git diff) |
| `witness.run` | Same as select but returns the executable test command |
| `witness.staged` | Select tests for staged changes (pre-commit) |
| `witness.since` | Select tests since a git ref (PR review) |

## Vaulty: Secrets Proxy

Secrets proxy for AI agents. The agent gets capabilities, not credentials.

- Injects auth headers, env vars, bearer tokens without exposing values
- Redacts secrets from all output (raw, base64, URL-encoded)
- Policy enforcement: this key can only talk to `api.vendor.com`
- Full audit trail of every request

Claude calls `vaulty.proxy` or `vaulty.exec` and never sees a raw secret.

## Rally: Ticket Sync

Your assigned tickets, synced into local markdown so Claude works from a real backlog instead of guessing what to do next.

- Pulls assigned work items from Jira, Linear, GitHub, and Asana into `.rally/tickets/*.md`
- Normalizes provider-specific statuses and priorities into one vocabulary
- Pin tickets to surface them as working context for the agent
- `start`/`done` push status back to the source system
- Tokens are brokered through Vaulty — never written to disk

```bash
rally connect jira            Authorize a provider (tokens go into Vaulty)
rally sync                    Pull assigned tickets to .rally/tickets/
rally next                    Highest-priority actionable ticket
rally start <id>              Mark in-progress, pin it, push status upstream
rally done <id>               Mark done and unpin
rally pinned                  List pinned tickets (working context)
```

## Which Tools You Actually Get

Not all of them, and that's deliberate. Every tool definition is context window spent in every session, before any work happens. Twelve database tools in a project with no database aren't just noise — every call would fail anyway.

So `recon.*` and `witness.*` are always on: they need no configuration and work in any git repo. `schema.*` and `vaulty.*` are registered only when rivet can see you're using them. A bare project gets 16 built-in tools instead of 34; rivet's own `rivet.*` context tools are there either way.

- **`schema.*` (12 tools)** appears once the `schema:` section of `.rivet/config.yaml` names something to read — a database, a migrations dir, or a code-scan root.
- **`vaulty.*` (6 tools)** appears once a vault exists: `./vaulty.{toml,yaml,yml}`, `.vaulty/`, or `~/.config/vaulty/`.

Detection is a default, not a verdict. The `tools:` section overrides it in either direction:

```yaml
tools:
  schema: true     # force on
  vaulty: false    # force off — omit a key entirely to auto-detect
```

Forcing `vaulty: true` is the common case: creating the first vault is a human step (`rivet vaulty init`), so on a fresh project the tools would otherwise stay hidden until after you've bootstrapped it.

## Safety Levels

Every capability has a classification:

| Level | What happens | Examples |
|-------|-------------|----------|
| `safe` | Runs automatically | Queries, searches, diagnostics |
| `guarded` | Environment checks first | Cache refresh, seed data, codegen |
| `dangerous` | Explicit approval required | Migrations, deploys, backfills |

Policies can also block dangerous operations in specific environments:

```yaml
policies:
  - name: no-dangerous-in-ci
    match:
      safety: dangerous
    deny_env: [CI]
```

## Project CLI Integration

If your project has its own CLI (or you want to build one), Rivet can expose its commands as MCP tools:

```yaml
# .rivet/capabilities.yaml
cli: ./bin/projectcli

capabilities:
  - name: db.metrics-summary
    description: Read-only metrics summary
    command: [query, metrics-summary]
    output: json
    safety: safe
    params:
      - name: date_range
        type: string
        required: true
```

Claude calls `db.revenue-summary` instead of writing SQL from vibes.

## Commands

```
rivet init                    Set up .rivet/, install skills and subagents
rivet update                  Add any missing Rivet files without overwriting config.yaml
rivet serve                   Start the MCP server (auto-started by Claude Code)
rivet sync                    Regenerate CLAUDE.md from your config and context
rivet doctor                  Check that everything's wired up correctly
rivet inspect capabilities    List what's registered and its safety level
rivet project run <cap>       Run a capability from the terminal
rivet run <args...>           Pass through to your registered project CLI
rivet context list            List context docs
rivet context show <name>     Read one
rivet context scaffold        Generate starter docs from recon analysis
rivet context recommend <q>   "What context is relevant to this task?"
rivet context index           Precompute embeddings for semantic recommend (optional)
rivet runbook find <symptom>  Find the operational runbook for a symptom
rivet runbook list            List runbooks and their triggers
rivet runbook draft <title>   Draft a runbook for human review (--steps, --trigger)
rivet runbook promote <name>  Promote a reviewed draft into an active runbook
rivet context lint            Check docs for quality and staleness
rivet learnings add <title>   Record a learning (--observation required)
rivet learnings list          Active (un-promoted) entries (--all, --json)
rivet learnings show <name>   Read one entry
rivet learnings promote <n>   Mark promoted into a doc (--to, --archive)
rivet policy status           Show active policies
rivet policy check <cap>      Evaluate policies against one capability
rivet schema overview         Configured databases + migration summary
rivet schema tables           List tables
rivet schema describe <tbl>   Columns, indexes, FKs for a table
rivet schema indexes unused   Indexes with zero reads
rivet schema indexes missing  Missing-index candidates (engine + code)
rivet schema refresh          Re-pull catalog snapshots
rivet project init-cli        Scaffold a starter project CLI
rivet project register-cli    Register your project CLI
rivet project commands        List project CLI commands
```

## Claude Code Skills

Installed by `rivet init`, run them with `/` in Claude Code:

- **`/rivet-setup`** : Full onboarding. `rivet init`, scaffold, fill the placeholder docs, `rivet sync`.
- **`/rivet-fill-context`** : Fill out placeholder context docs using recon. Overview, key modules, failure modes, gotchas — written for an AI reader, 20-40 lines a doc.
- **`/rivet-promote-learnings`** : Work the learning log. Promote, merge, or archive each entry; update the target context docs; mark what was promoted.
- **`/rivet-compact-context`** : Curated layer only. Run `rivet context lint`, trim docs back under ~50 lines, delete gotchas that got fixed, bump `last_reviewed`. It'll point you at `/rivet-promote-learnings` if what you actually wanted was the log.

## Claude Code Agents

Installed by `rivet init` into `.claude/agents/`:

- **`rivet-explorer`**: A strictly read-only investigation subagent. Tool access is limited to Claude read tools plus read-only Rivet MCP tools.
- **`rivet-investigator`**: The same investigation workflow, but also allowed to call `rivet.learn` to record durable non-obvious findings to the learning log.

## Building

```bash
make build       # build to bin/rivet
make test        # run tests
make vet         # static analysis
make install     # go install
```

Requires Go 1.25+.

## The Philosophy

Three rules, in order:

> Prefer explicit project capabilities over ad hoc shell use.

> Deterministic facts first, AI judgment second.

> Context is a built-in primitive, not an afterthought.

The goal isn't to make the AI more autonomous. It's to make it less ignorant.

## License

MIT
