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
- **The root command sets `SilenceErrors`, so don't print errors yourself.**
  `main` prints the returned error and sets the exit code. Cobra printed it too,
  so every failure appeared twice; that is fixed once on the root and inherited
  by every subcommand. `SilenceUsage` stays per-command — usage is still the
  right thing to show for a genuine argument error, just not for a runtime one
  whose message already says what to do.
- **`rivet run` and `rivet project run` are unrelated commands.** `run` has
  `DisableFlagParsing` and forwards argv verbatim to `project_cli.command`.
  `project run` looks up a registered capability, checks policy, and executes it.
- **Every executor comes from `newExecutor`, and it must stay that way.**
  `serve` and `project run` once built their own, and the second one registered
  only two of the four in-process runners — so `rivet project run witness.select`
  fell through to `os/exec` and hunted for a `witness` binary on PATH. There is
  now one construction site, guarded by a test that fails if a second appears
  and another that requires every builtin to have a runner. Register a new
  sibling tool there, not in a caller. See [[tool-embedding]].
- **The CLI subtrees rewrite `--cache-dir`'s default rather than prepending an
  argument.** `useRivetCacheDir` in `internal/cli/recon.go` walks the sibling's
  command tree and repoints the flag at `.rivet/recon`, so an explicit
  `--cache-dir` passed by the user still wins (pflag parses afterwards). It has
  to check both `PersistentFlags()` and `Flags()` at every level, because recon
  binds the flag persistently on its root while witness binds it per subcommand.
  Before this, `rivet recon` maintained a second `<root>/.recon/` index that the
  MCP server never read.
- **`register-cli` is the only command that writes `.rivet/config.yaml`, and
  writing config has two traps that both used to bite.** `Config.Write` now
  merges into the existing document via `yaml.Node`, updating only the keys the
  struct models. It previously marshalled the struct over the whole file, which
  destroyed every comment and every unmodelled key — including the entire
  `schema:` section, since `internal/config.Config` has no `Schema` field. Once
  schema tooling became gated on that section, the same bug also made twelve MCP
  tools vanish. If you add a writer, merge; never marshal over the file.
- **Use `config.LoadProject()` to write, never `LoadOrDefault("")`.**
  `LoadOrDefault` falls back to `~/.config/rivet/config.yaml` when a project has
  no config, and `cfg.path` follows whatever it loaded — so writing through it
  edited the user's *global* config on one project's behalf. `LoadProject` never
  leaves the project. `LoadOrDefault` is still correct for read-only paths, where
  a user-level fallback is a feature.
