---
tags: [recon, witness, vaulty, rally, embedding, composition, capabilities, adapters, cache]
owner: djtouchette
last_reviewed: 2026-07-26
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
  from an embedded tool. (`witness run` carries the test runner's own code in an
  error type `pkg/embedded` does not export; no capability invokes it, so the
  flattening costs nothing today.)
- `SilenceErrors` means **nothing prints `cmd.Execute()`'s error** — it is not
  in the stderr buffer either, because cobra never wrote it there. The witness
  adapter appends it explicitly; `internal/recon` and `internal/vaulty` still
  drop it, so a failure from those two arrives as an exit code with no reason.
  An unexplained `exit code: 1` next to an empty stdout reads to an agent as
  "there was nothing to do".
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

- **Witness fails closed, and the capability descriptions are the only place
  the agent learns that.** `witness select --format exec` prints **one command
  per line** (a polyglot repo yields several — running only the first reports a
  pass for tests that never ran), and exits non-zero with no command rather than
  invent an invocation for a language it has no runner for. None of that is
  visible in the `Command` slice, so `internal/capabilities/builtins.go` has to
  spell it out; trimming those descriptions for brevity re-introduces the false
  green.
- **The descriptions must be true of the PINNED witness, not of `../witness`.**
  `internal/witness.PinnedVersion` names the tagged version `go.mod` embeds.
  Advertising a flag the pinned build rejects (`--fallback`, `--require-coverage`
  and `--signals` are v0.5.0) hands the agent an invocation that exits 1 as an
  unknown flag — and, worse, a rule phrased as *"an empty `tests[]` is safe when
  `summary.unmapped` is empty"* evaluates to "safe" on **every** run against a
  build with no `summary.unmapped`, inverting fail-closed advice into a
  fail-open rule. State the default instead: an empty selection is unproven.
  `internal/witness/pin_test.go` checks both — the pin against `go.mod`, and
  every flag the descriptions name against the embedded binary's own `--help`.
- **recon and witness share one cache.** Both adapters inject
  `--cache-dir <recon.CacheDir()>` before the caller's args. Witness's
  dependency-graph scoring *is* recon's index; it does not build its own. A
  project gets one `.rivet/`, not four dot-directories. The cache (`recon.db`)
  is derived and gitignored.

  `CacheDir()` returns an **absolute** `<cwd>/.rivet/recon`, and that is load
  bearing: the two tools resolve a *relative* `--cache-dir` differently — recon
  against the process's working directory, witness (v0.5.0 onward) against the
  git repository root. Run rivet from a subdirectory with a relative path and
  the one-cache guarantee silently becomes two full indexes. Anchored at the
  cwd rather than the repo root because that is where recon *analyses*; the
  practical consequence is that rivet still wants to be run from the project
  root, and running it from a subdirectory builds a second (self-consistent)
  cache there. `internal/witness/cache_test.go` asserts the adapters land in one
  directory.
- `serve.go` calls `rivetctx.LoadCodeDocs(recon.Run)` at startup, so recon is
  not only proxied — it *feeds* the context system by extracting `rivet:context`
  comments and `.context/` sidecars. This doubles as cache warming, which is why
  the first tool call is fast.
- Adding a new sibling tool is three steps: export `<sibling>/pkg/embedded`, add an
  adapter, register the runner. Do **not** also add a bespoke code path in the
  MCP server — the capability registry already handles it.
