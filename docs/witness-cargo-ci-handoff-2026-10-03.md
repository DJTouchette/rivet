# Deterministic doctor CI and Witness repair handoff

Rivet source checked: `62e4a6ee609b8a1b2463d0e3dc6c92582fd63e9d` on
`wks/astra-reliable-witness-across-projects`, worktree
`/home/djtouchette/.workspacer/worktrees/rivet/astra-reliable-witness-across-projects`.
Base: `3b607ea7cc6c665cf34871785e76348b133e9dce`. Fresh fetch confirmed
origin/master `424540d` remains an ancestor; no integration was necessary.

The two pre-existing TestSemanticIndexStates failures reproduce before this
change (`/tmp/witness-cargo-ci-repair/doctor-before.log`). Tests now use a local
httptest Ollama-shaped endpoint and actual semantic.Store fixtures, matching model,
host and dimensions. They verify the POST path, model, probe text, response vector
shape and request count. Cases retain unavailable-backend failure and the recovery
recommendation, and add missing-model, corrupt-index and incompatible-cache cases.
There are no skips, daemon/model prerequisites or production doctor changes.
Generic Run setup tests explicitly select lexical mode. t.Setenv/t.Chdir restore
all process state; subtests are sequential, requests counted atomically and servers
closed in cleanup. Full race runs pass.

The owning tool-embedding context now documents the Witness adapter's preserved
exit code (17 in its regression), exported v0.5.0 TestsFailed type and distinction
from older installed Rivet behavior. The module pin itself has not changed.

## Gates and composition

All checks used `env -u GOROOT GOTOOLCHAIN=go1.25.7`. Full tests, full race suite,
vet and builds pass both with `GOWORK=off` (Witness v0.5.0) and with the temporary
`/tmp/witness-cargo-ci-repair/go.work`, composing Witness source
`0c248bccb7130bcd99de0b40b80f5221336f02f3`. No test exclusion flags or command-level
embedding environment overrides were used. Both binaries pass strict context lint
(11 docs). Isolated actual Rivet serve MCP smoke passes with 2 pinned calls and
4 composed calls; both sentinels report runner_executed=false.

Logs and binary metadata: `/tmp/witness-cargo-ci-repair/`, especially
`rivet-{pinned,composed}-{full,race,vet}.log`,
`rivet-{pinned,composed}-lint.log`, `rivet-{pinned,composed}-metadata.txt`,
`mcp-{pinned,composed}.json`, and `doctor-{before,after}.log`.

Witness's `docs/cargo-review-handoff-2026-10-03.md` records physical identity,
actual offline Cargo regressions against old/new code, source proofs, full tests,
race/vet/build gates, 7 unchanged real-project audits, 4 prior edge cases and 2 new
reusable alias cases. All application audits are non-executing and use scratch
indexes. Only tiny scratch Cargo fixtures and local test endpoints execute.

## Review and release boundary

Ready for parent independent review, not a published release. Linux runtime only;
macOS/Windows CI/runtime checks and release cross-builds are separate work.
No push/merge/tag/release/install, global tool or service changes, live MCP calls,
shared repository/index/config mutations or credential inspection occurred.
Pre-existing untracked skills and review notes are preserved.

Rivet go.mod/go.sum and internal/witness.PinnedVersion remain v0.5.0. Pinned binary
metadata names that version; composed metadata names Witness (devel). The installed
or live process has not gained these changes. After fresh approval, the separate
release task must choose unused remote versions, publish Witness through its
existing tag workflow after green CI, update all three Rivet pin locations together,
and run the full unskipped pinned tests/vet/build/lint, metadata/help and isolated
MCP/representative gates against the released module. Publish Rivet through its
existing workflow and verify packaged checksums and embedded version. Never commit
this temporary workspace or an absolute replace directive.
