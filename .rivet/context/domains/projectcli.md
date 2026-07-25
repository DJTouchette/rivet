---
tags: [projectcli, discover, scaffold, elixir, manifest, capabilities, registration, safety, subcommand]
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

- **Discovered capabilities default to `dangerous`, on purpose.** `applyDefaults`
  back-fills `kind: project_command` and `output: json` quietly, but an omitted
  `safety` becomes `dangerous` and warns on stderr. It used to default to `safe`,
  which meant a project CLI reporting a destructive command without a `safety`
  field got it registered as auto-runnable — fail-open on the one axis that has
  to fail closed. Note `guarded` would not have been enough: nothing enforces it.
  `RunCapability` gates only `dangerous`, and the MCP server offers the `approve`
  argument only for `dangerous`. Explicitly declared levels are left alone, so
  labelling your commands is how you get out of the approval prompt.
- **Re-registering refreshes, but not in the loosening direction.**
  `MergeManifest` refreshes `description`, `command` and `output` — mechanical
  facts only the CLI knows, where a stale value preserves a bug rather than a
  preference. `safety` refreshes only when discovery is *stricter*; a discovered
  level that would relax the manifest is reported and skipped unless `--force`,
  because silently accepting a loosening would reopen the fail-open hole that
  defaulting unlabelled capabilities to `dangerous` closed. `params` are never
  touched: they can't appear in discovery output, so rewriting them can only
  destroy work. It previously skipped existing entries entirely, making the
  discover contract write-once.
- **The manifest merge edits the YAML node tree, like config does.** It used to
  `yaml.Marshal` over the file, erasing every comment — including the starter
  block documenting param types — and re-rendering params with `default: ""`
  noise. Same class of bug as `Config.Write`; see [[cli]]. An unparseable
  manifest is now an error rather than something to overwrite.
- **Path comparison resolves symlinks on both sides.** `StripBinaryPrefix` uses
  `filepath.EvalSymlinks`, falling back to `filepath.Abs` when resolution fails,
  and requires one side to exist on disk so a subcommand token can never be
  eaten. Exact string equality used to no-op whenever the discover command's
  `os.Executable()` (symlinks resolved) differed from the `filepath.Abs` of the
  path you typed (not resolved), leaving an absolute path as `Command[0]` that
  `ToCapabilities` then prefixed with `cli:` again.
- **`--lang` detects six languages but only two have scaffolds, and the other
  four are now refused rather than silently given the Go one.** A Node or Python
  repo used to acquire a cobra module and a `go.mod` it never asked for, because
  the switch sent everything that wasn't `elixir` to `initGoCLI`.
  `detectProjectLanguage` also returns `""` now instead of falling back to
  `"go"` — a repo it can't identify was otherwise indistinguishable from a real
  Go repo. `--lang go` remains available deliberately: the Go scaffold is its own
  module, so it is usable from any project.
- **The discover command is argv, not a single token.** `project_cli.discover`
  holds the arguments appended to `command`, defaulting to `["rivet-discover"]`.
  It exists because not every CLI can host a top-level subcommand: the Elixir
  scaffold's task lives in the project's Mix namespace (`mix <ns>.rivet_discover`),
  so the old fixed argv meant discovery reported "no rivet-discover support" for
  every Elixir project. Register with
  `register-cli mix --discover project.rivet_discover`; the flag persists.
- **Mix prints "Compiling…" to stdout**, so the first discovery after any edit
  arrives as chatter followed by JSON. `parseDiscoverOutput` retries from the
  first `{`. Without that, Elixir registration fails on the first run and
  succeeds on the second, which is a maddening thing to debug.
- **`register-cli` accepts a bare command name**, resolved on PATH, and records
  the bare name — `command: mix`, not one machine's toolchain path. A file on
  disk still wins, so existing usage is unchanged.
- `initElixirCLI` rewrites the default name `projectcli` to `project`, because
  the name becomes a Mix task namespace. Your Go and Elixir scaffolds of the
  "same" project therefore produce differently-named capabilities.
- The scaffolded Go module is a **separate module** with its own pinned
  toolchain (`go 1.23`, cobra v1.9.1) — it is not built by rivet's `go build`
  and does not share rivet's dependency versions. See [[stack]].
- `DiscoverCapabilities` is dead. Its comment claims register-cli uses it "when
  the binary hasn't been built yet"; register-cli only ever calls `RunDiscover`.
  Don't edit it expecting an effect. Its Elixir twin was deleted for being a
  third hardcoded copy of the same capability list — after the discover-task
  template and `StarterManifestElixir` — which is exactly where safety levels
  drift apart unnoticed.
- `init-cli` refuses to run without `.rivet/`; `register-cli` has no such check.
  That used to mean it wrote to the user's global config; `LoadProject` now keeps
  writes inside the project regardless, so the missing check is harmless. See
  [[cli]].
