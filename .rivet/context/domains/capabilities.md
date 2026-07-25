---
tags: [capabilities, registry, executor, safety, gating, tools]
owner: djtouchette
last_reviewed: 2026-07-24
related_paths:
  - "internal/capabilities/**"
  - "internal/cli/project.go"
  - "internal/config/config.go"
---

# Capabilities

Source: `internal/capabilities/` (8 files)

## High-risk files

- `internal/capabilities/builtins.go` — fan-in: 9, churn: 6, score: 1.00
- `internal/capabilities/manifest.go` — fan-in: 9, churn: 1, score: 0.17

## Overview

The registry of everything rivet can *do*, and the machinery that runs it. A
capability is data: name, description, `Command` slice, output format, safety
level, optional typed params. Three sources feed the registry, in increasing
precedence: built-ins, `.rivet/capabilities.yaml` (manifest), then `capabilities:`
in `.rivet/config.yaml`. Later sources `Override` earlier ones by name.

## Key modules

- `builtins.go` — the built-in capability declarations plus `BuiltinGroups`/`BuiltinsFor`
- `registry.go` — registration, lookup, `Override`
- `executor.go` — dispatch to in-process runner or `os/exec`, safety enforcement
- `manifest.go` — `.rivet/capabilities.yaml` parsing into capabilities
- `internal/cli/project.go` — `buildRegistry`, `builtinGroupsFor`, `schemaInUse`, `vaultyInUse`

## Failure modes

- `SafetyLevelDangerous` capabilities return `ErrDangerousNoApprove` unless
  `approved` is true. Approval comes from `buildArgsFromParams` in the MCP layer
  or `--approve` on the CLI. Policy rules are checked *before* execution, in the
  MCP server, not in the executor — a capability run from the CLI does not go
  through policy.
- Manifest and config capability errors warn on stderr and skip the offending
  entry rather than failing startup. A typo'd capability silently doesn't exist.

## Gotchas

- **Built-ins are gated on config.** `buildRegistry` calls
  `BuiltinsFor(builtinGroupsFor(cfg))`, which filters by name prefix. A bare
  project registers 16 tools (12 `recon.*` + 4 `witness.*`); the 12 `schema.*`
  and 6 `vaulty.*` only appear when in use. The reason is context economy —
  every tool definition costs window in *every* session, so dead Postgres tools
  in a project with no database are pure overhead. `Builtins()` still returns
  the unfiltered set and is the source of truth for tests.
- `recon.*` and `witness.*` are structurally ungated — `BuiltinGroups` has no
  field for them. They need no configuration and are the core of the product.
- **`tools:` in config is tri-state.** `*bool` where nil = auto-detect (the
  default), true = force on, false = force off. The force-on case matters for
  bootstrapping a first vault, since `vaulty init` is interactive and must run
  before any vault exists for auto-detection to see.
- **Schema config is parsed by a different package.** `internal/config.Config`
  has **no** `Schema` field. The `schema:` section of the same YAML file is read
  separately by `internal/schema/config.Load()`. Looking for schema settings on
  the main config struct and concluding they don't exist is an easy wrong turn.
- `buildRegistry` is shared by `serve`, `inspect`, `policy` and `sync`, so
  gating affects all of them. That's intended: `rivet inspect capabilities`
  should report what Claude actually gets, not a superset.
- Dispatch is keyed on `Command[0]`, so a capability named `recon.anything` with
  `Command: ["recon", ...]` routes in-process automatically. See
  [[tool-embedding]].
