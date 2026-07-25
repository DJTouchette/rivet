---
tags: [recon, witness, vaulty, rally, embedding, composition, capabilities, adapters, cache]
owner: djtouchette
last_reviewed: 2026-07-24
related_paths:
  - "internal/recon/**"
  - "internal/witness/**"
  - "internal/vaulty/**"
  - "internal/rally/**"
  - "internal/capabilities/executor.go"
  - "internal/cli/serve.go"
---

# Sibling Tool Embedding

How rivet composes recon, witness, vaulty and rally. Read this before changing
anything about how a sibling tool is invoked — the two integration mechanisms
look similar from the outside and are not interchangeable.

## Overview

Four sibling tools live in their own repos under the same umbrella. Rivet
integrates them **two different ways**, deliberately.

**In-process (recon, witness, vaulty).** Each exports its cobra root command
from `<sibling>/pkg/embedded`. Rivet wraps that in a ~35-line adapter
(`internal/{recon,witness,vaulty}/*.go`) that swaps stdout/stderr for buffers,
sets `SilenceErrors`/`SilenceUsage`, and returns
`(stdout, stderr, exitCode, err)`. The adapters register as `InProcessRunner`s
in `internal/cli/serve.go`, keyed by the **first element of a capability's
`Command` slice**:

    exec.RegisterInProcess("recon", recon.Run)

`internal/capabilities/builtins.go` then declares capabilities as plain data
(`Command: []string{"recon", "grep"}`). `Executor.RunCapability` checks
`cap.Builtin`, looks up `Command[0]` in the runner map, and calls in-process;
anything unmatched falls through to `os/exec`. A user's project-CLI capability
from `.rivet/capabilities.yaml` and a built-in recon tool therefore travel the
identical code path — the only difference is whether a runner is registered for
that prefix.

**On-disk contract (rally).** Rally is the exception. It *has* a `<sibling>/pkg/embedded`,
but rivet never imports it. `internal/rally/pins.go` reads `.rally/pins.json`
and `.rally/tickets/*.md` directly and implements the generic `pins.Provider`
interface. The file format is the contract; there is no Go dependency on rally
at all.

## Key modules

- `internal/{recon,witness,vaulty}/*.go` — the buffering adapters
- `internal/capabilities/executor.go` — `Command[0]` dispatch to in-process vs `os/exec`
- `internal/capabilities/builtins.go` — capability declarations (data, not code)
- `internal/cli/serve.go` — where runners are registered
- `internal/rally/pins.go` — the on-disk provider, not an embed
- `internal/cli/{recon,witness,vaulty}.go` — same commands re-parented as `rivet <tool>` CLI subtrees

## Failure modes

- The adapters **flatten every failure to exit code 1**. `cmd.Execute()`
  returning any error yields `exitCode: 1`, so a sibling's meaningful exit codes
  never reach the caller. Don't build logic on top of a specific non-zero code
  from an embedded tool.
- Sibling tools are consumed as **tagged module versions**, not `replace`
  directives (dropped in commit `9f908f5`). A change in `../recon` does nothing
  here until it is tagged and `go.mod` is bumped. Editing the sibling repo and
  expecting rivet to pick it up is the most common wasted hour.
- Rally's coupling is invisible to the compiler. Changing the shape of
  `.rally/pins.json` in the rally repo breaks rivet at runtime with nothing
  failing at build time. `internal/rally/pins.go` also deliberately mirrors
  rally's file/dir permissions (0644/0755) so pinning from rivet doesn't change
  them out from under rally.

## Gotchas

- **recon and witness share one cache.** Both adapters inject
  `--cache-dir .rivet/recon` before the caller's args. Witness's dependency-graph
  scoring *is* recon's index; it does not build its own. A project gets one
  `.rivet/`, not four dot-directories. The cache (`recon.db`) is derived and
  gitignored.
- `serve.go` calls `rivetctx.LoadCodeDocs(recon.Run)` at startup, so recon is
  not only proxied — it *feeds* the context system by extracting `rivet:context`
  comments and `.context/` sidecars. This doubles as cache warming, which is why
  the first tool call is fast.
- Adding a new sibling tool is three steps: export `<sibling>/pkg/embedded`, add an
  adapter, register the runner. Do **not** also add a bespoke code path in the
  MCP server — the capability registry already handles it.
