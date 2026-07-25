---
tags: [go, stack, dependencies, modules, cgo, tree-sitter, build, tests, toolchain]
owner: djtouchette
last_reviewed: 2026-07-24
related_paths:
  - "go.mod"
  - "Makefile"
  - ".goreleaser.yml"
  - ".github/workflows/**"
  - "cmd/rivet/**"
---

# Stack & Conventions

**Language:** go
**Frameworks:** github.com/DATA-DOG/go-sqlmock, github.com/djtouchette/recon, github.com/djtouchette/vaulty, github.com/djtouchette/witness, github.com/jackc/pgx/v5, github.com/microsoft/go-mssqldb, github.com/spf13/cobra, github.com/yalue/onnxruntime_go, gopkg.in/yaml.v3

## Project conventions

Go 1.25.7 (CI resolves the toolchain from `go.mod`, so bumping the file bumps
the build). One binary, `cmd/rivet/main.go`, which does nothing but build the
cobra tree; everything else is under `internal/`, so nothing here is importable
by another repo. `make build` writes `bin/rivet` with the version injected via
`-ldflags -X main.version`.

- **Cobra commands are constructors, never package-level vars.** Every command
  is a `newXCmd() *cobra.Command` with its flags bound to closure locals. The
  one exception is `internal/schema/cli`, whose flags live in package-level
  vars re-bound by `NewRoot`; that is safe only because pflag resets the target
  to the default on each registration, and it is not safe across goroutines.
- **Anything that must land in a user's repo is a Go string constant.** Skills,
  subagents, default runbooks, the starter config and both project-CLI
  scaffolds are all `const` templates — there is no `go:embed` in this repo.
  Editing one changes what `rivet init` *and* `rivet update` write; see [[cli]].
- **The design rationale lives in package and file header comments**, not in a
  docs tree. `internal/cli/hooks.go`, `internal/context/semantic/onnx_stub.go`
  and `.goreleaser.yml` each open with the "why" before the "what". Read the top
  of a file before changing it.
- Config, manifest and frontmatter parsing is all `gopkg.in/yaml.v3` with
  struct tags, and unknown keys are silently dropped on unmarshal — which is
  what makes rewriting a config file lossy (see [[schema]]).

## Testing approach

Stdlib `testing` only — no assertion or mocking library, table-driven with
anonymous struct slices. 43 `_test.go` files, colocated with their package.
CI runs `go build ./cmd/rivet`, `go test ./... -count=1` and `go vet ./...` on
ubuntu and macos; there is no linter config and no coverage gate, so `vet` is
the entire static bar.

- `go-sqlmock` is used only by the Postgres/MSSQL driver tests, to assert the
  exact catalog SQL without a server.
- Real-database tests are **env-gated, not build-tagged**
  (`RIVET_TEST_PG_DSN`, `RIVET_TEST_MSSQL_DSN`), so `go test ./...` runs them
  and they skip. Nobody sees them fail; nobody sees them run either.
- The `internal/cli` setup helpers are all cwd-relative, so their tests do
  `t.Chdir(t.TempDir())`. That makes them non-parallelizable by construction.
  `internal/cli/update_test.go` still uses the older `os.Chdir` + `defer` form.
- `internal/context/eval_test.go` is a retrieval quality *ratchet* rather than a
  correctness test — see [[context]] before touching scoring constants.

## Common patterns

- **Sibling tools are consumed as tagged module versions with no `replace`
  directives.** `go.mod` pins recon v0.10.0, witness v0.4.2, vaulty v0.4.0.
  Editing a sibling repo changes nothing here until it is tagged and bumped —
  the most common wasted hour in this codebase. Details in [[tool-embedding]].
- **CGo is mandatory.** All eighteen tree-sitter modules in `go.mod` are marked
  indirect: they arrive through recon (and witness) rather than being used
  here, but they still link C. A `CGO_ENABLED=0` build will not work,
  which is why `.goreleaser.yml` forces `CGO_ENABLED=1`, builds darwin natively
  on a macOS runner and cross-compiles linux/windows with `zig cc`.
- **`onnxruntime_go` is a direct require that the default build never
  compiles.** The real embedder is behind `//go:build onnx` and additionally
  needs the ONNX Runtime shared library plus a model directory on disk; without
  the tag `onnx_stub.go` reports unavailable and retrieval stays lexical.
- Database drivers register themselves through blank side-effect imports in
  `internal/schema/schema.go`; adding an engine means a new `catalog.Register`
  in an `init()` plus an import there, not a switch statement.
- The in-process integration shape is one signature repeated four times:
  `func Run(args []string) (stdout, stderr string, exitCode int, err error)`.
  Matching it is what makes a tool pluggable into the capability executor.
