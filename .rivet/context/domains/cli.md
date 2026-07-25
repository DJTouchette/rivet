---
tags: [cli]
owner: djtouchette
last_reviewed: 2026-07-24
related_paths:
  - "internal/cli/**"
---

# Cli

Source: `internal/cli/` (26 files)

## High-risk files

- `internal/cli/serve.go` — fan-in: 1, churn: 7, score: 0.13
- `internal/cli/root.go` — fan-in: 1, churn: 5, score: 0.09
- `internal/cli/context.go` — fan-in: 1, churn: 5, score: 0.09

## Overview

Every user-facing entrypoint. `cmd/rivet/main.go` only builds the tree from
`NewRootCmd(version)` and exits 1 on error — all behaviour lives here. Sixteen
subcommands in four groups:

- **Setup** — `init`, `update`, `doctor`, `sync`
- **Knowledge** — `context`, `runbook`, `learnings` (see [[context]])
- **Capabilities** — `project`, `inspect`, `policy`, `serve` (see [[capabilities]])
- **Re-parented sibling trees** — `recon`, `witness`, `vaulty`, `schema`, plus
  `run`, a raw argv passthrough to the configured project CLI binary

## Key modules

- `internal/cli/root.go` — the entire tree, one `AddCommand` call
- `internal/cli/init.go` — `ensureProjectSetup`, the shared setup path
- `internal/cli/project.go` — `buildRegistry`, `buildPolicies`, the gating helpers, `project run` / `register-cli`
- `internal/cli/serve.go` — registry + executor + four document tiers → MCP server
- `internal/cli/hooks.go` — `removeLegacyHooks`
- `internal/cli/scaffold.go` — turns `recon overview` + `recon hotspots` into starter docs
- `internal/cli/skills.go`, `agents.go`, `runbooks_default.go` — `.claude/` and `.rivet/` assets shipped as Go string constants

## Failure modes

- Setup is uniformly skip-if-exists. Every `ensure*` helper reports
  "already exists, skipped" rather than overwriting, which is the only reason
  `rivet update` is safe to run on a live project. `--force` on `init` widens
  exactly one thing: rewriting `.rivet/config.yaml`.
- `buildRegistry` writes warnings to stderr and skips the offending entry, so a
  typo'd capability silently doesn't exist. `serve.go` degrades every document
  loader to nil for the same reason — fewer tiers beats refusing to start.
- `register-cli` treats **any** non-zero exit from `<binary> rivet-discover` as
  "this binary has no discovery support". A crashing discover command and a
  missing one are indistinguishable. See [[projectcli]].

## Gotchas

- **`ensureProjectSetup` is the whole of both `init` and `update`.** `init`
  adds only a pre-flight check that `.rivet/` doesn't already exist; `update`
  calls it with `force=false`. Anything you add there runs on both paths, on
  every existing project, forever. That is the intended contract, not an
  accident — but it means "a small addition to init" is really a migration.
- `removeLegacyHooks` runs inside that shared path, so `rivet update` is what
  retires the old bash nudge hooks. It sweeps **every** hook event in
  `.claude/settings.json`, not just `PostToolUse`, because an even older version
  registered them under `Stop`. It deletes `.rivet/hooks/` only when the
  directory is left empty. Nudging lives in the MCP server now — see [[mcp]].
- **`rivet run` and `rivet project run` are unrelated commands.** `run` has
  `DisableFlagParsing` and forwards argv verbatim to `project_cli.command`.
  `project run` looks up a registered capability, checks policy, and executes it.
- **`rivet project run` cannot run `witness.*` or `schema.*`.** `serve.go`
  registers four in-process runners (vaulty, recon, witness, schema);
  `newProjectRunCmd` registers only vaulty and recon. A builtin whose
  `Command[0]` has no runner falls through to `os/exec` and goes looking for a
  `witness` binary on PATH. Use `rivet witness ...` / `rivet schema ...` or the
  MCP tools instead. See [[tool-embedding]].
- **The sibling CLI subtrees use a different recon cache than the MCP tools.**
  `internal/recon/recon.go` and `internal/witness/witness.go` prepend
  `--cache-dir .rivet/recon` before the caller's args; `internal/cli/recon.go`
  and `internal/cli/witness.go` attach the sibling's root command untouched, so
  they fall back to recon's own default `<root>/.recon/`. `rivet recon refresh`
  from a shell does not refresh the index the MCP server reads.
- **`register-cli` is the only command that writes `.rivet/config.yaml`, and it
  rewrites the whole file.** `Config.Write` marshals the Go struct, so every
  comment and every key the struct doesn't model is dropped — including the
  entire `schema:` section, since `internal/config.Config` has no `Schema`
  field. Worse, `config.LoadOrDefault("")` falls back to
  `~/.config/rivet/config.yaml` when the project has none, and `cfg.path`
  follows the file it loaded, so running `register-cli` before `rivet init`
  edits the user's global config.
