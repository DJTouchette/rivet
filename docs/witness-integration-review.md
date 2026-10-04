# Witness consistency review — 2026-10-03

Historical review. Current Cargo/doctor repairs and unskipped green gates are in
[witness-cargo-ci-handoff-2026-10-03.md](witness-cargo-ci-handoff-2026-10-03.md).

## Baselines and ownership

Both changed repositories were fetched and pulled fast-forward-only **before
editing**. Rivet baseline: `424540db1d29df0d8ab3bf94773866a44cfece15`.
Witness baseline: `c1abc74e40facd7956444e79109330cc60a67139`.
Their shared master worktrees remain untouched (pre-existing untracked
`.workspacer/` retained). Recon needed no source changes.

The installed mise Rivet binary is v0.19.1, revision
`31d959d562e9ce4b1a37b826121f7e578ab13ebf`, with Witness v0.4.2.
This Rivet source pins Witness v0.5.0. `internal/witness` is an adapter;
the selector/runner owner is the standalone Witness module. A sibling edit
cannot change the embedded module or an already-running MCP process.

Rivet changes preserve explicit CLI test-runner exit codes, show the actual
pinned Witness version on `rivet witness --version`, and make an empty Witness
MCP payload an error with an unproven-coverage explanation. Capability names,
legacy text/selection shapes and command-only `witness.run` remain compatible.
The new Witness planner is deliberately not advertised as present in v0.5.0.

## Observed project cases

Representative checkouts were read only; analysis used absolute temporary cache
directories. No tests from these real applications were executed. Source identities
at the audit were Workspacer `bbe684281c42ad8d41b054d341b0b3305d136b7e`,
Leroy `696363b07dbad425c0e8d752ea515bfb842dda7d`, and Cassadol
`0ea0cb29fc7899625aaad8d871dbc7c0ca77e493` (working-tree content is what Recon reads).

| Change | Baseline behavior | New behavior |
|---|---|---|
| Workspacer `apps/native/src/ui.rs` | Zero tests; fallback `npx jest` at repo root | Inline Rust test candidate; `cargo test --manifest-path ./apps/native/Cargo.toml` |
| Workspacer renderer `webBackend.ts` | Jest command at repo root; capped selected list | Renderer Vitest at its config directory; main Vitest and Playwright suites for related tests; truncation visible |
| Workspacer Rust + renderer | JS-only command could omit the unmapped native change | Four independent commands, including native Cargo; Rust is never sent to Jest |
| Leroy `LocalBillingService.cs` | 50 of 71 tests; project directories inferred by naming | Real ProjectReference closure, 10 actual `.csproj` targets; 21 omitted ranked tests reported |
| Cassadol `navigationHistoryUtil.ts` | 23 relevant tests, but `npx jest` | `yarn run vitest run --environment=jsdom`, cwd `clients/web` |
| Cassadol C# DataLayer `App.cs` | 23 selected tests; unresolved/capped graph edges hidden | 19 actual test-project suites, including `backend/Handlers/DataLayer/test/DataLayer.Tests/DataLayer.Tests.csproj`; unresolved import and fan-out caps reported |
| Cassadol C# + web | Guessed .NET directories plus wrong JS runner | 19 independent .NET project commands plus web Vitest, retaining all coverage diagnostics |

All seven recurring acceptance cases pass. Required related tests are asserted
where named in the audit, alongside required commands/cwd and plan status.
The objective is valid ownership and explained coverage, not a larger test list.
Whole suites intentionally trade precision for coverage. For example, desktop
main has both Vitest and Playwright configs; project mappings can assign narrower
source ownership instead of running both. Application integration suites still
need their usual runtime prerequisites when a person chooses to execute them.

Warm planning measured **380–792 ms** in the final seven-case audit; cold indexing
and planning **3.69–10.27 s**. These are observations on this machine, not an SLA.
Manifest reads are capped at 2 MiB and each manifest walk at 200,000 entries;
dependency/build/hidden trees and symlinked directories are skipped. Exceeding a
limit is an explained incomplete plan.

Witness contains the repeatable command and compact recorded results:

```
witness audit audits/representative.json --root /path/to/Work
# audits/verified-2026-10-03.json
```

## Verification

- Witness: full `make test`, `make vet`, `make build` pass, including golden
  regressions and actual tiny Cargo fixture execution (passing and failing tests).
- Scratch .NET manifests were evaluated by installed .NET 10 MSBuild without
  restore/build: the planned target is a test project with the expected reference.
- The generated npm argv/cwd was executed offline against a local fake Vitest bin;
  no package was downloaded or installed.
- New tests cover inline Rust, fresh/new/deleted/renamed paths through a reused
  index, real nested project ownership, reverse Cargo dependencies, config
  validation/precedence/exclusions, path quoting/cwd, and no-execution sentinels.
- Rivet: vet/build pass. Full Go tests expose two existing cases inside
  `TestSemanticIndexStates` that configure Ollama and expect it to answer on
  localhost. No doctor source changed, and no model/service was started.
  With `RIVET_EMBED_BACKEND=` and `-skip '^TestSemanticIndexStates$'`, all other
  Go tests pass against both the tagged pin and the composed Witness tree.
- `scripts/verify-witness-mcp.py` starts a **real isolated `rivet serve` process**
  in a scratch mixed Rust/.NET/JS repo. It verifies stdout JSON-RPC capture,
  selection text, all three plan commands/cwd, nonzero/incomplete JSON surfaced
  with MCP `isError`, and PATH sentinels proving no runner/package-manager
  execution. The tagged-pin smoke and composed `--require-plan` smoke pass.

The inherited shell points GOROOT at a different Go installation; checks used
`env -u GOROOT GOTOOLCHAIN=go1.25.7` with the installed Go launcher on PATH.
No global tool installation or model/database/network service was needed.
Windows argv/cwd is a documented interface, but these runtime checks ran on Linux;
POSIX text command rendering is not advertised as PowerShell syntax.

## Review the composed changes without changing a module pin

From an isolated Rivet checkout, with Go 1.25.7 or later selected:

```sh
RIVET_TREE="$PWD"
WITNESS_TREE=/path/to/isolated/witness
PROOF_DIR=$(mktemp -d)
(cd "$PROOF_DIR" && go work init "$RIVET_TREE" "$WITNESS_TREE")
GOWORK="$PROOF_DIR/go.work" go build -o "$PROOF_DIR/rivet" ./cmd/rivet
python3 scripts/verify-witness-mcp.py --binary "$PROOF_DIR/rivet" --require-plan
GOWORK="$PROOF_DIR/go.work" go test ./... -count=1
```

No `replace` directive or absolute local dependency path is committed. The
project's normal tag/release-and-pin contract remains intact. After independent
review/local landing, a separately authorized Witness release and Rivet pin bump
are needed to make the planner available in distributed Rivet builds. Keep the
current pin constant and capability claims synchronized when doing that.
Replacing installed binaries or restarting live MCP/fleet processes was outside
this task and was not done.

## Optional project configuration

Witness now owns optional Git-root `.witness.json`, schema version 1. It supports
named suites, source/test globs, rule exclusions, explicit argv/cwd, and per-suite
`suite`/`fail` fallback. Explicit matching rules win; overlapping rules include
all matching suites. Invalid/unsupported schemas, malformed rules and escaping
cwd are errors. Exclusions never make a changed file disappear. Custom commands
are labelled as configured; detecting them does not execute them or authorize
an agent to execute them.

See Witness `schemas/witness-config-v1.json`, `docs/project-configuration.md`, and
README for Workspacer/Rust+TS, C# backend+Vitest, and unusual-layout examples.
Witness previously had only flags; Rivet's existing config owns MCP capabilities
and context, not test-suite mappings, so these rules are not duplicated there.
No config was added to the representative shared repositories.
