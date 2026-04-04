# Rivet

A project capability layer for Claude Code. One CLI that packages safe, explicit project operations and exposes them via MCP — so Claude Code calls real project tools instead of improvising shell commands.

**The problem:** Claude Code is powerful but blind. It improvises SQL queries, guesses at project structure, and cobbles together shell commands without knowing your codebase's conventions, safety rules, or operational surface.

**Rivet's approach:** Define your project's capabilities once. Rivet exposes them as MCP tools that Claude Code can call directly — project CLI commands, repo intelligence, structured context, and safety policies.

## Quick Start

```bash
# Install
go install github.com/djtouchette/rivet/cmd/rivet@latest

# Initialize in your project
cd your-project
rivet init

# This creates:
#   .rivet/config.yaml          — capability manifest
#   .rivet/context/             — structured context docs
#   .mcp.json                   — MCP server config for Claude Code
#   .claude/skills/             — Claude Code skill files
#   .claude/settings.json       — hooks for context learning

# Scaffold context docs from your codebase
rivet context scaffold

# Fill them out with AI assistance (run in Claude Code)
/rivet-fill-context

# Sync capabilities to CLAUDE.md
rivet sync
```

Open Claude Code and the rivet MCP server starts automatically. Claude Code gets project-aware tools instead of raw shell.

## What Rivet Gives Claude Code

### Project CLI Commands

Register your project's operational CLI and expose commands as MCP tools:

```yaml
# .rivet/config.yaml
project_cli:
  command: "./bin/projectcli"

capabilities:
  - name: "db.patient-summary"
    description: "Read-only patient summary report"
    command: ["./bin/projectcli", "db", "patient-summary"]
    output: "json"
    safety: "safe"

  - name: "billing.failures"
    description: "Billing failure summary by date range"
    command: ["./bin/projectcli", "billing", "failures"]
    output: "json"
    safety: "safe"
```

Claude Code calls `db.patient-summary` instead of guessing at SQL. Every command has a safety level (`safe`, `guarded`, `dangerous`) and structured JSON output.

### Repo Intelligence (Recon)

Built-in integration with [recon](https://github.com/DJTouchette/recon) for fast, deterministic codebase understanding:

- **`recon.overview`** — project structure, languages, frameworks, entrypoints
- **`recon.related`** — find files related to a given path (imports, co-change, naming)
- **`recon.tests`** — discover test files for a source file
- **`recon.symbols`** — search functions, types, and classes
- **`recon.search`** — unified search across symbols, paths, and content
- **`recon.grep`** — enriched grep with classification (definition/reference/test/comment)
- **`recon.hotspots`** — high fan-in, high churn files (risky to change)
- **`recon.context`** — file preview, owners, metrics, nearby configs

### Structured Context

Encode domain knowledge that survives across sessions:

```
.rivet/context/
  domains/billing.md       — what billing does, its invariants, common traps
  domains/auth.md          — auth boundaries, session rules
  modules/patient-search.md — how search works, what it depends on
  paradigms/stack.md       — framework conventions, testing approach
  paradigms/hotspots.md    — high-risk files with metrics
```

Context docs are exposed as MCP resources. Claude Code reads the relevant context before making changes, instead of guessing at intent.

The `rivet.learn` tool lets Claude Code append findings to context docs during investigation — building institutional knowledge over time.

### Secrets via Vaulty

Built-in integration with [vaulty](https://github.com/DJTouchette/vaulty) for secret-aware operations. Claude Code gets authenticated API calls and secret-injected commands without ever seeing raw credentials.

### Safety Policies

Every capability has a safety level:

| Level | Behavior | Examples |
|-------|----------|----------|
| `safe` | Auto-allowed | Summaries, list, search, diagnostics |
| `guarded` | Environment checks | Cache refresh, seed, codegen |
| `dangerous` | Requires approval | Migrations, deploys, backfills |

## Commands

```
rivet init                    Initialize .rivet/ in your project
rivet serve                   Start the MCP server (auto-started by Claude Code)
rivet sync                    Update CLAUDE.md from .rivet/ config and context
rivet doctor                  Check environment and dependencies
rivet inspect capabilities    List registered capabilities with safety levels
rivet run <capability>        Run a registered capability directly
rivet context list            List context documents
rivet context show <name>     Show a context document
rivet context scaffold        Generate starter context docs from recon analysis
rivet context recommend <q>   Find context docs relevant to a query
rivet policy status           Show current policy configuration
rivet project register-cli    Register a project CLI
rivet project commands        List project CLI commands
```

## Claude Code Skills

`rivet init` installs these skills (run them with `/` in Claude Code):

- **`/rivet-setup`** — Full initialization: init, scaffold, fill context, sync
- **`/rivet-fill-context`** — Fill out scaffolded context docs using recon analysis
- **`/rivet-compact-context`** — Deduplicate learnings, prune stale info, keep docs concise

## Architecture

```
Claude Code
    │
    ▼
Rivet MCP Server (rivet serve)
    │
    ├── Project CLI commands     — named operations, not ad-hoc shell
    ├── Recon                    — deterministic repo intelligence
    ├── Context system           — domain knowledge as MCP resources
    ├── Vaulty                   — secret-aware execution
    └── Policy layer             — safety levels and approval gates
    │
    ▼
Project Codebase
```

### Core Philosophy

> Prefer explicit project capabilities over ad hoc shell use.

> Deterministic facts first, AI judgment second.

> Context is a built-in primitive, not an afterthought.

## Building

```bash
make build       # build to bin/rivet
make test        # run tests
make vet         # static analysis
make install     # go install
```

Requires Go 1.25+.

## License

MIT
