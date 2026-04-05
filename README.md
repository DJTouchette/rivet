# Rivet

Your AI coding agent is smart. It's also completely clueless about your project.

It doesn't know that the billing retry logic is split across two services. It doesn't know that `ServiceRenderedInsertTrigger` fires two queries and will absolutely ruin your day if you touch it wrong. It doesn't know your team's conventions, your pain points, or that one migration script that must never run on Tuesdays.

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
  domains/         ← business areas (billing, auth, scheduling)
  modules/         ← technical subsystems (patient-search, ledger-sync)
  paradigms/       ← cross-cutting patterns (caching, event handling)
```

Each doc has frontmatter linking it to relevant files and tags:

```markdown
---
tags: [billing, invoice, payment]
related_paths:
  - backend/Handlers/PaymentGateway/**
  - clients/web/src/pages/Invoices/**
---

# Billing

## Overview
Handles invoice generation, retry logic, payment failure handling...

## Gotchas
- Retry logic is split between the scheduler and payment adapter
- Status names in the DB don't match the API. Obviously.

## Learnings
- 2026-04-01: ServiceRenderedInsertTrigger fires 2 queries per insert, watch for N+1
- 2026-04-03: Hidden dependency on ThirdPartyCheck table for payment validation
```

The `## Learnings` section is where Claude appends new findings. When it grows too long, the `/rivet-compact-context` skill promotes the important stuff to permanent sections and clears the noise.

Context docs are exposed as MCP resources, so Claude can pull the right domain knowledge before making changes. The recommendation engine scores docs by tag matches, file path globs, and keyword relevance, so Claude doesn't have to guess which context to read.

### The Feedback Loop

This is where it gets interesting.

```
You start a task
    → Claude investigates using recon tools (grep, symbols, related files)
    → After a few searches, a hook nudges: "hey, check the context docs first"
    → Claude reads the billing domain doc, finds the gotchas
    → Saves 10 minutes of blind exploration
    → Discovers something new during the task
    → Another hook nudges: "learned anything? write it down"
    → Claude appends the finding to the right domain file
    → Context doc gets too long eventually
    → Claude summarizes, promotes the good stuff, trims the noise
    → Next session starts with better knowledge
```

The context files are just markdown in `.rivet/context/`. You can read them, edit them, commit them. They're not magic. They're documentation that happens to maintain itself.

### The Nudging System

Rivet doesn't just provide tools. It shapes how Claude uses them.

**After 2+ recon searches without reading context:** "You're exploring blind. Check the context docs, someone already figured this out."

**After 5+ investigations without recording a finding:** "You've been digging for a while. Learn anything worth writing down?"

**After a doc hits 8+ learnings or 60+ lines:** "This doc is getting chunky. Time to consolidate."

These are Claude Code hooks that fire automatically. They create a natural rhythm: explore, check existing knowledge, investigate, record what you found, keep docs clean.

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
| `recon.context` | File preview, fan-in/fan-out, churn, hotspot score |
| `recon.hotspots` | Top files ranked by risk (fan-in * churn) |
| `recon.tests` | Find test files for a source file |
| `recon.overview` | Project structure, languages, frameworks, entrypoints |
| `recon.changes` | Recent git change summary |
| `recon.refresh` | Incremental cache update |

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
- Policy enforcement: this key can only talk to `api.stripe.com`
- Full audit trail of every request

Claude calls `vaulty.proxy` or `vaulty.exec` and never sees a raw secret.

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
  - name: db.patient-summary
    description: Read-only patient summary
    command: [query, patient-summary]
    output: json
    safety: safe
    params:
      - name: date_range
        type: string
        required: true
```

Claude calls `db.patient-summary` instead of writing SQL from vibes.

## Commands

```
rivet init                    Set up .rivet/, install hooks and skills
rivet serve                   Start the MCP server (auto-started by Claude Code)
rivet sync                    Regenerate CLAUDE.md from your config and context
rivet doctor                  Check that everything's wired up correctly
rivet inspect capabilities    List what's registered and its safety level
rivet run <capability>        Run a capability from the terminal
rivet context list            List context docs
rivet context show <name>     Read one
rivet context scaffold        Generate starter docs from recon analysis
rivet context recommend <q>   "What context is relevant to this task?"
rivet context lint            Check docs for quality and staleness
rivet policy status           Show active policies
rivet project register-cli    Register your project CLI
rivet project commands        List project CLI commands
```

## Claude Code Skills

Installed by `rivet init`, run them with `/` in Claude Code:

- **`/rivet-setup`** : Full onboarding. Init, scaffold, fill context, sync.
- **`/rivet-fill-context`** : Have Claude fill out placeholder context docs using recon.
- **`/rivet-compact-context`** : Consolidate learnings, prune stale info, keep docs tight.

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
