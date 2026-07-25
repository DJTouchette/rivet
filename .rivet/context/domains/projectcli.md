---
tags: [projectcli]
owner: djtouchette
last_reviewed: 2026-07-24
related_paths:
  - "internal/projectcli/**"
---

# Projectcli

Source: `internal/projectcli/` (6 files)

## Overview

How a project's own CLI becomes rivet capabilities. Two halves that meet at
`.rivet/capabilities.yaml`:

1. **Scaffolding** — `rivet project init-cli` writes a starter CLI. Go projects
   get a standalone cobra module under `tools/<name>`; Elixir projects get Mix
   tasks written straight into the host project's lib/mix/tasks/ tree.
2. **Registration** — `rivet project register-cli <binary>` sets
   `project_cli.command` and, if the binary answers the **rivet-discover
   protocol** (a hidden subcommand printing `{"capabilities": [...]}`), merges
   what it reports into the manifest.

The manifest is the real contract. `Manifest.ToCapabilities` prepends the
top-level `cli:` value to each entry's `command:`, so `command:` holds
subcommand args *only*. From there a project command is an ordinary capability —
see [[capabilities]] and [[tool-embedding]].

## Key modules

- `internal/projectcli/discover.go` — `RunDiscover`, the protocol client
- `internal/projectcli/scaffold.go` — Go scaffold templates + `Scaffold`
- `internal/projectcli/scaffold_elixir.go` — Mix task templates + `ScaffoldElixir`
- `internal/cli/project.go` — `initGoCLI`, `initElixirCLI`, `detectProjectLanguage`, the register-cli merge
- `internal/capabilities/manifest.go` — `ToCapabilities`, `StarterManifest`, `StarterManifestElixir`

## Failure modes

- `RunDiscover` maps **every** `cmd.Run` error to `(nil, nil)` — "this binary
  doesn't support discovery". A discover command that panics, a binary that
  isn't executable, and a CLI that genuinely has no discover subcommand all
  produce the same "no rivet-discover support — edit ... manually" message.
  Only malformed JSON on a zero-exit run surfaces as an error.
- Both scaffolders are per-file skip-if-exists, so a half-written scaffold is
  repaired by re-running rather than clobbered.
- `initGoCLI`/`initElixirCLI` treat a failed manifest write as a stderr warning
  and still report success for the scaffold.

## Gotchas

- **Discovered capabilities default to `safe`.** `RunDiscover` back-fills
  `kind: project_command`, `output: json` and `safety: safe` on any entry that
  omits them. A project CLI that reports a destructive command without a
  `safety` field gets it registered as auto-runnable.
- **`register-cli` merges by name and never updates.** Existing manifest entries
  are skipped outright, so re-running it after changing a description or safety
  level in your discover output does nothing. Only `cli:` is overwritten.
- **The absolute-path strip is exact string equality.** The generated discover
  command emits `os.Executable()` (which resolves symlinks), while `register-cli`
  strips using `filepath.Abs` of the path *you typed* (which does not). When
  those differ, the strip silently doesn't happen and the absolute binary path
  survives as `Command[0]` in the manifest — then `ToCapabilities` prepends
  `cli:` on top, so the tool runs as `<cli> /abs/path/<cli> query status`.
- **`--lang` auto-detection has five outcomes but only two code paths.**
  `detectProjectLanguage` returns elixir/go/node/rust/python/ruby;
  `newProjectInitCLICmd` switches on `"elixir"` and sends *everything else* to
  the Go scaffold. A Node or Python repo gets a Go cobra module and a `go.mod`.
- **The Elixir discover task is unreachable.** The scaffold generates
  `mix <ns>.rivet_discover`, but `RunDiscover` always execs the fixed argv
  `["rivet-discover"]` against a binary path. Elixir capabilities come from
  `StarterManifestElixir` (`cli: mix`) at scaffold time and are edited by hand
  from then on.
- `initElixirCLI` rewrites the default name `projectcli` to `project`, because
  the name becomes a Mix task namespace. Your Go and Elixir scaffolds of the
  "same" project therefore produce differently-named capabilities.
- The scaffolded Go module is a **separate module** with its own pinned
  toolchain (`go 1.23`, cobra v1.9.1) — it is not built by rivet's `go build`
  and does not share rivet's dependency versions. See [[stack]].
- `DiscoverCapabilities` and `DiscoverElixirCapabilities` are dead. The former's
  comment claims register-cli uses it "when the binary hasn't been built yet";
  register-cli only ever calls `RunDiscover`. Don't edit them expecting an effect.
- `init-cli` refuses to run without `.rivet/`; `register-cli` has no such check,
  which is how it ends up writing to the global config (see [[cli]]).
