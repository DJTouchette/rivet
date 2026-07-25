---
tags: [mcp, nudging, tools, resources, jsonrpc, claude]
owner: djtouchette
last_reviewed: 2026-07-24
related_paths:
  - "internal/mcp/**"
  - "internal/cli/serve.go"
  - "internal/cli/hooks.go"
---

# Mcp

Source: `internal/mcp/` (3 files)

## High-risk files

- `internal/mcp/server.go` — fan-in: 1, churn: 6, score: 0.11

## Overview

The JSON-RPC 2.0 stdio server Claude Code talks to. It exposes two surfaces:
**tools** (built-in `rivet.*` / `rally.*` handlers plus every registered
capability, routed through the executor) and **resources** (context docs, wiki,
runbooks, code-extracted docs, and pinned tickets).

`server.go` is one large file with a flat `handleToolsCall` switch: built-in
rivet tools are matched by name first and return early; everything else falls
through to the capability registry and executor.

## Key modules

- `server.go` — the whole server: routing, tool list, resource list/read, nudging
- `handleToolsCall` — the switch; built-ins first, then registry dispatch
- `handleLearn` — writes learning entries and emits the promotion nudge
- `allDocs` — merges the four document tiers for resource listing

## Failure modes

- Policy violations and executor errors both return `IsError: true` results
  rather than JSON-RPC errors — Claude sees them as tool output, not protocol
  failures. That's intentional; don't "fix" it into an RPC error.
- Loader failures in `serve.go` (context, wiki, runbooks, code docs, semantic
  scorer) all degrade to a warning on stderr and a nil slice. The server starts
  with fewer tiers rather than refusing to start. Never write loader warnings to
  stdout — stdout is the JSON-RPC channel and any stray byte corrupts the stream.

## Gotchas

- **Nudging lives here, not in hooks.** Session state (`reconCallsSinceLearn`,
  `contextShown`) drives three nudges appended to tool response text: a
  context-first nudge at exactly 2 recon investigation calls (suppressed once
  `rivet.context-show` is called), a learn nudge at `learnNudgeThreshold` (5),
  reset to 0 by `rivet.learn`, and a promotion nudge in `handleLearn` when
  `CountActive` reaches `promoteLearningsThreshold` (10). All of it is unit
  tested in `server_test.go`.
- Rivet used to *also* ship bash hooks that inferred the same state by grepping
  Claude Code's transcript. They were redundant and broken, and are now deleted;
  `removeLegacyHooks` in `internal/cli/hooks.go` cleans them out of existing
  projects. **Do not reintroduce transcript-grepping.** The server observes
  every tool call directly, and logic in a Go string literal cannot be tested.
- **MCP tool names are sanitized by the client.** Rivet registers
  `recon.grep` and `rivet.learn`; Claude Code exposes them as
  `mcp__rivet__recon_grep` and `mcp__rivet__rivet_learn` — dots become
  underscores. Anything that matches on tool names externally (hook matchers,
  settings, docs) must use the sanitized form. Matching the dotted form silently
  never fires; this exact bug is what killed the old hooks.
- Only tools in `reconInvestigationTools` count toward nudging — `recon.overview`
  and `recon.refresh` are deliberately excluded because they aren't investigation.
- `promoteMessage` is a format string taking (count, threshold, learningsDir).
  It once referenced a `rivet.context-learnings-list` tool that does not exist;
  when editing nudge copy, only name tools that appear in `handleToolsList`.
