package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	rivetctx "github.com/djtouchette/rivet/internal/context"
)

// Intent harness.
//
// These tests drive the real rivet binary against a real git repository, the
// way a developer, a CI job and an agent would. Unit tests in internal/context
// pin the parser and the checks in isolation; this harness proves the pieces
// agree end to end: that a marker in a C# test file is found by the scanner,
// classified as a test, counted by the check, fails or passes the exit code CI
// reads, and comes back out of the MCP server as the text an agent sees.
//
// The fixture (testdata/intent-shop) is a small polyglot shop — Go, TypeScript
// and C# — with three intent docs:
//
//	intent/billing  BIL-001 invariant  code + test     → enforced
//	                BIL-002 invariant  code only       → untested (warning)
//	                BIL-010 policy     code + test     → enforced
//	                BIL-011 policy     enforced: manual
//	                BIL-003 retired
//	intent/orders   ORD-001 invariant  C# code + C# test; no related_paths of
//	                its own, so it inherits the orders domain doc's — which
//	                include web/src/checkout/**
//	intent/money    MON-001 invariant  cross-cutting, no related_paths
//
// Every scenario gets a fresh copy committed to a fresh repository on branch
// main, so scenarios cannot leak into one another. HOME points at an empty
// directory so no developer config is read.

// mk is the marker keyword, assembled so that rivet's own source never
// contains a literal marker. Rivet dogfoods intent: its CI scans this repo for
// markers, and the fixture IDs written by these tests (BIL-099, ORD-001, ...)
// would otherwise be reported as references to rules rivet doesn't define.
const mk = "rivet:" + "intent"

var (
	harnessBinOnce sync.Once
	harnessBin     string
	harnessBinErr  error
)

// rivetBinary builds cmd/rivet once per test process.
func rivetBinary(t *testing.T) string {
	t.Helper()
	harnessBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rivet-harness-bin-")
		if err != nil {
			harnessBinErr = err
			return
		}
		harnessBin = filepath.Join(dir, "rivet")
		if runtime.GOOS == "windows" {
			harnessBin += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", harnessBin, "github.com/djtouchette/rivet/cmd/rivet")
		cmd.Dir = harnessSourceDir()
		if out, err := cmd.CombinedOutput(); err != nil {
			harnessBinErr = fmt.Errorf("building rivet: %v\n%s", err, out)
		}
	})
	if harnessBinErr != nil {
		t.Fatal(harnessBinErr)
	}
	return harnessBin
}

// harnessSourceDir is this package's source directory, found from the file
// itself rather than the working directory, which other tests in the package
// change.
func harnessSourceDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(file)
}

type shop struct {
	t    *testing.T
	dir  string
	bin  string
	home string
}

func newShop(t *testing.T) *shop {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	s := &shop{t: t, dir: t.TempDir(), bin: rivetBinary(t), home: t.TempDir()}

	src := filepath.Join(harnessSourceDir(), "testdata", "intent-shop")
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		dst := filepath.Join(s.dir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0644)
	})
	if err != nil {
		t.Fatal(err)
	}

	s.git("init", "-q", "-b", "main")
	s.git("config", "user.email", "harness@example.com")
	s.git("config", "user.name", "harness")
	s.git("config", "commit.gpgsign", "false")
	s.commitAll("baseline")
	return s
}

func (s *shop) git(args ...string) string {
	s.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = s.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func (s *shop) commitAll(msg string) {
	s.t.Helper()
	s.git("add", "-A")
	s.git("commit", "-q", "-m", msg)
}

func (s *shop) write(rel, content string) {
	s.t.Helper()
	p := filepath.Join(s.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		s.t.Fatal(err)
	}
}

func (s *shop) read(rel string) string {
	s.t.Helper()
	data, err := os.ReadFile(filepath.Join(s.dir, rel))
	if err != nil {
		s.t.Fatal(err)
	}
	return string(data)
}

// edit replaces exactly one occurrence of old in a file, failing the test if
// it is missing — a scenario whose mutation silently did nothing would pass
// for the wrong reason.
func (s *shop) edit(rel, old, new string) {
	s.t.Helper()
	content := s.read(rel)
	if strings.Count(content, old) != 1 {
		s.t.Fatalf("edit %s: want exactly one %q, found %d", rel, old, strings.Count(content, old))
	}
	s.write(rel, strings.Replace(content, old, new, 1))
}

type runResult struct {
	stdout, stderr string
	code           int
}

func (s *shop) run(args ...string) runResult {
	s.t.Helper()
	cmd := exec.Command(s.bin, args...)
	cmd.Dir = s.dir
	cmd.Env = append(os.Environ(), "HOME="+s.home, "XDG_CONFIG_HOME="+filepath.Join(s.home, ".config"), "RIVET_EMBED_BACKEND=")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		s.t.Fatalf("running rivet %v: %v", args, err)
	}
	return runResult{out.String(), errb.String(), code}
}

func (s *shop) check(args ...string) (rivetctx.IntentReport, int) {
	s.t.Helper()
	r := s.run(append([]string{"intent", "check", "--json"}, args...)...)
	var rep rivetctx.IntentReport
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		s.t.Fatalf("intent check --json: %v\nstdout:\n%s\nstderr:\n%s", err, r.stdout, r.stderr)
	}
	return rep, r.code
}

func (s *shop) affected(args ...string) rivetctx.AffectedReport {
	s.t.Helper()
	r := s.run(append([]string{"intent", "affected", "--json"}, args...)...)
	if r.code != 0 {
		s.t.Fatalf("intent affected exited %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	var rep rivetctx.AffectedReport
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		s.t.Fatalf("intent affected --json: %v\n%s", err, r.stdout)
	}
	return rep
}

// ---------------------------------------------------------------------------
// Assertions
// ---------------------------------------------------------------------------

func statuses(rep rivetctx.IntentReport) map[string]string {
	m := map[string]string{}
	for _, c := range rep.Rules {
		m[c.Rule.ID] = c.Status
	}
	return m
}

// findingRules returns "rule:severity" for every finding, for exact set checks.
func findings(rep rivetctx.IntentReport) []string {
	var out []string
	for _, w := range rep.Warnings {
		out = append(out, fmt.Sprintf("%s:%s", w.Rule, w.Severity))
	}
	return out
}

func requireFinding(t *testing.T, rep rivetctx.IntentReport, rule string, sev rivetctx.Severity, msgContains string) {
	t.Helper()
	for _, w := range rep.Warnings {
		if w.Rule == rule && w.Severity == sev && strings.Contains(w.Message+" "+w.Document, msgContains) {
			return
		}
	}
	t.Fatalf("no %s %s finding mentioning %q; findings: %v\n%+v", sev, rule, msgContains, findings(rep), rep.Warnings)
}

func requireNoErrors(t *testing.T, rep rivetctx.IntentReport) {
	t.Helper()
	for _, w := range rep.Warnings {
		if w.Severity == rivetctx.SeverityError {
			t.Fatalf("unexpected error finding %s: %s", w.Rule, w.Message)
		}
	}
}

func affectedIDs(rep rivetctx.AffectedReport) []string {
	var ids []string
	for _, a := range rep.Rules {
		ids = append(ids, a.Rule.ID)
	}
	return ids
}

func affectedRule(t *testing.T, rep rivetctx.AffectedReport, id string) rivetctx.AffectedRule {
	t.Helper()
	for _, a := range rep.Rules {
		if a.Rule.ID == id {
			return a
		}
	}
	t.Fatalf("%s not in affected rules %v", id, affectedIDs(rep))
	return rivetctx.AffectedRule{}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func requireContains(t *testing.T, label, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Fatalf("%s does not contain %q:\n%s", label, w, got)
		}
	}
}

func requireNotContains(t *testing.T, label, got string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(got, w) {
			t.Fatalf("%s unexpectedly contains %q:\n%s", label, w, got)
		}
	}
}

// ---------------------------------------------------------------------------
// Coverage check — the CI gate
// ---------------------------------------------------------------------------

func TestIntentHarness_BaselineCoverage(t *testing.T) {
	s := newShop(t)
	rep, code := s.check()
	if code != 0 {
		t.Fatalf("baseline check exited %d; findings %v", code, findings(rep))
	}

	want := map[string]string{
		"BIL-001": rivetctx.CoverageEnforced,
		"BIL-002": rivetctx.CoverageUntested,
		"BIL-010": rivetctx.CoverageEnforced,
		"BIL-011": rivetctx.CoverageManual,
		"BIL-003": rivetctx.CoverageRetired,
		"MON-001": rivetctx.CoverageEnforced,
		"ORD-001": rivetctx.CoverageEnforced,
	}
	got := statuses(rep)
	if len(got) != len(want) {
		t.Fatalf("got %d rules %v, want %d", len(got), got, len(want))
	}
	for id, st := range want {
		if got[id] != st {
			t.Errorf("%s: status %q, want %q", id, got[id], st)
		}
	}
	// The only finding is the untested invariant. The README's prose mention
	// of BIL-999 must not register as an unknown reference.
	if f := findings(rep); !equalStrings(f, []string{"untested-invariant:warning"}) {
		t.Fatalf("findings = %v, want exactly [untested-invariant:warning]", f)
	}
	if len(rep.Unknown) != 0 {
		t.Fatalf("unknown references %+v — prose in README.md must not count", rep.Unknown)
	}

	// Markers are classified as test vs code by each ecosystem's convention.
	for _, c := range rep.Rules {
		if c.Rule.ID != "ORD-001" {
			continue
		}
		if len(c.Code) != 1 || c.Code[0].File != "services/orders/Acme.Orders/Checkout.cs" || c.Code[0].Line != 7 {
			t.Errorf("ORD-001 code refs = %+v", c.Code)
		}
		if len(c.Tests) != 1 || c.Tests[0].File != "services/orders/Acme.Orders.Tests/Oversell.cs" {
			t.Errorf("ORD-001 test refs = %+v (a .NET test project must classify as tests)", c.Tests)
		}
	}

	// --strict promotes the warning to a failure.
	if _, code := s.check("--strict"); code == 0 {
		t.Fatal("--strict should fail on the untested-invariant warning")
	}

	// The existing CI step, context lint, enforces intent too.
	lint := s.run("context", "lint")
	if lint.code != 0 {
		t.Fatalf("context lint exited %d:\n%s", lint.code, lint.stdout)
	}
	requireContains(t, "context lint", lint.stdout, "untested-invariant")

	// Human-readable table.
	table := s.run("intent", "check")
	requireContains(t, "intent check", table.stdout,
		"BIL-002  invariant  untested", "7 rule(s) in 3 intent doc(s): 4 enforced, 1 untested, 1 manual, 1 retired")
}

// rivet:intent CTX-010
func TestIntentHarness_UnenforcedInvariantFailsCI(t *testing.T) {
	s := newShop(t)
	s.edit("services/orders/Acme.Orders/Checkout.cs", "        // "+mk+" ORD-001\n", "")
	s.edit("services/orders/Acme.Orders.Tests/Oversell.cs", " // "+mk+" ORD-001", "")

	rep, code := s.check()
	if code == 0 {
		t.Fatal("an invariant with no marker must fail the check")
	}
	if statuses(rep)["ORD-001"] != rivetctx.CoverageUnenforced {
		t.Fatalf("ORD-001 status = %q", statuses(rep)["ORD-001"])
	}
	requireFinding(t, rep, "unenforced-invariant", rivetctx.SeverityError, "ORD-001")

	if lint := s.run("context", "lint"); lint.code == 0 {
		t.Fatalf("context lint must fail too:\n%s", lint.stdout)
	}
}

func TestIntentHarness_PendingDowngradesToWarning(t *testing.T) {
	s := newShop(t)
	s.edit("services/orders/Acme.Orders/Checkout.cs", "        // "+mk+" ORD-001\n", "")
	s.edit("services/orders/Acme.Orders.Tests/Oversell.cs", " // "+mk+" ORD-001", "")
	s.edit(".rivet/intent/domains/orders.md",
		"  why: overselling forces cancellations, which cost more than the lost sale\n",
		"  why: overselling forces cancellations, which cost more than the lost sale\n  enforced: pending — stock service rewrite (ORD-77)\n")

	rep, code := s.check()
	if code != 0 {
		t.Fatalf("a pending gap must not fail CI; findings %v", findings(rep))
	}
	if statuses(rep)["ORD-001"] != rivetctx.CoveragePending {
		t.Fatalf("ORD-001 status = %q", statuses(rep)["ORD-001"])
	}
	requireFinding(t, rep, "pending-enforcement", rivetctx.SeverityWarning, "ORD-001")
	if _, code := s.check("--strict"); code == 0 {
		t.Fatal("--strict must still fail on a pending gap")
	}
}

func TestIntentHarness_UnenforcedPolicyIsAWarning(t *testing.T) {
	s := newShop(t)
	s.edit("services/billing/dunning.ts", "// "+mk+" BIL-010\n", "")
	s.edit("services/billing/dunning.test.ts", "// "+mk+" BIL-010\n", "")

	rep, code := s.check()
	if code != 0 {
		t.Fatalf("an unmarked policy is a warning, not a CI failure; findings %v", findings(rep))
	}
	requireFinding(t, rep, "unenforced-policy", rivetctx.SeverityWarning, "BIL-010")
}

func TestIntentHarness_BrokenReferencesFailCI(t *testing.T) {
	cases := []struct {
		name, marker, rule string
	}{
		{"unknown", "BIL-099", "unknown-rule-reference"},
		{"retired", "BIL-003", "retired-rule-reference"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newShop(t)
			s.write("services/billing/credit.go", "package billing\n\n// "+mk+" "+tc.marker+"\nfunc Credit() {}\n")
			rep, code := s.check()
			if code == 0 {
				t.Fatalf("a %s reference must fail the check", tc.name)
			}
			requireFinding(t, rep, tc.rule, rivetctx.SeverityError, "services/billing/credit.go:3")
		})
	}
}

func TestIntentHarness_MultipleIDsOnOneMarker(t *testing.T) {
	s := newShop(t)
	// One marker naming two rules, with a trailing explanation, in a Python test.
	s.write("tools/test_reconcile.py", "# "+mk+" BIL-002, MON-001 — totals reconcile in cents\ndef test_reconcile():\n    pass\n")
	rep, code := s.check()
	if code != 0 {
		t.Fatalf("check exited %d: %v", code, findings(rep))
	}
	if statuses(rep)["BIL-002"] != rivetctx.CoverageEnforced {
		t.Fatalf("BIL-002 should now be enforced (code + python test), got %q", statuses(rep)["BIL-002"])
	}
	if len(rep.Warnings) != 0 {
		t.Fatalf("expected no findings once BIL-002 has a test, got %v", findings(rep))
	}
}

func TestIntentHarness_DocDefectsFailCI(t *testing.T) {
	t.Run("duplicate-rule-id", func(t *testing.T) {
		s := newShop(t)
		s.write(".rivet/intent/cross-cutting/stock.md", "---\ntags: [stock]\nowner: ops\nlast_ratified: 2099-01-01\n---\n\n# Stock\n\n## Invariants\n\n- **ORD-001** Stock is never negative.\n  why: accounting\n")
		rep, code := s.check()
		if code == 0 {
			t.Fatal("a duplicated rule ID must fail")
		}
		requireFinding(t, rep, "duplicate-rule-id", rivetctx.SeverityError, "ORD-001 is defined 2 times")
	})

	t.Run("prefix-mismatch", func(t *testing.T) {
		s := newShop(t)
		s.edit(".rivet/intent/domains/orders.md", "## Invariants\n", "## Invariants\n\n- **ORDER-5** Orders have an owner.\n  why: support needs a contact\n")
		s.write("services/orders/Acme.Orders/Owner.cs", "// "+mk+" ORDER-5\n")
		rep, code := s.check()
		if code == 0 {
			t.Fatal("a rule ID outside the doc's prefix must fail")
		}
		requireFinding(t, rep, "rule-prefix-mismatch", rivetctx.SeverityError, "ORDER-5")
	})

	t.Run("scaffold-is-incomplete-until-filled", func(t *testing.T) {
		s := newShop(t)
		r := s.run("intent", "scaffold", "payments")
		if r.code != 0 {
			t.Fatalf("scaffold: %s%s", r.stdout, r.stderr)
		}
		doc := s.read(".rivet/intent/domains/payments.md")
		requireContains(t, "scaffolded doc", doc, "prefix: PAY", "## Invariants", "## Policies", "## Retired")
		rep, code := s.check()
		if code == 0 {
			t.Fatal("an empty scaffold must fail until someone writes rules")
		}
		requireFinding(t, rep, "no-rules", rivetctx.SeverityError, "intent/payments")

		// Refuses to clobber.
		if r := s.run("intent", "scaffold", "payments"); r.code == 0 {
			t.Fatal("scaffold must refuse to overwrite without --force")
		}
	})

	t.Run("commented-out-rule-is-not-a-rule", func(t *testing.T) {
		s := newShop(t)
		s.edit(".rivet/intent/domains/orders.md", "## Invariants\n",
			"## Invariants\n\n<!--\n- **ORD-002** Orders ship within a day.\n  why: drafting\n-->\n")
		rep, code := s.check()
		if code != 0 {
			t.Fatalf("a rule inside an HTML comment must be ignored; findings %v", findings(rep))
		}
		if _, ok := statuses(rep)["ORD-002"]; ok {
			t.Fatal("ORD-002 was parsed from inside a comment")
		}
	})

	t.Run("manual-without-reason", func(t *testing.T) {
		s := newShop(t)
		s.edit(".rivet/intent/domains/billing.md", "  enforced: manual — finance ops approve in the ERP", "  enforced: manual")
		rep, _ := s.check()
		requireFinding(t, rep, "manual-without-reason", rivetctx.SeverityWarning, "BIL-011")
	})
}

func TestIntentHarness_ScanScope(t *testing.T) {
	s := newShop(t)
	// None of these may count: config exclude, Go testdata convention,
	// vendored deps, a gitignored file, and prose.
	s.write("generated/api.go", "// "+mk+" BIL-901\n")
	s.write("services/billing/testdata/fixture.go", "// "+mk+" BIL-902\n")
	s.write("node_modules/dep/index.js", "// "+mk+" BIL-903\n")
	s.write("vendor/lib/x.go", "// "+mk+" BIL-904\n")
	s.write(".gitignore", s.read(".gitignore")+"build/\n")
	s.write("build/out.js", "// "+mk+" BIL-905\n")
	s.write("docs/notes.md", "Mark it with "+mk+" BIL-906.\n")

	rep, code := s.check()
	if code != 0 {
		t.Fatalf("excluded files leaked into the scan: %v\n%+v", findings(rep), rep.Unknown)
	}

	// But an untracked, non-ignored source file IS scanned — an agent's new
	// file must be held to the rules before it is committed.
	s.write("services/billing/new_feature.go", "// "+mk+" BIL-907\n")
	rep, code = s.check()
	if code == 0 {
		t.Fatal("a new untracked file with a bad marker must be caught")
	}
	requireFinding(t, rep, "unknown-rule-reference", rivetctx.SeverityError, "services/billing/new_feature.go:1")
}

func TestIntentHarness_WorksOutsideGit(t *testing.T) {
	s := newShop(t)
	if err := os.RemoveAll(filepath.Join(s.dir, ".git")); err != nil {
		t.Fatal(err)
	}
	s.write("generated/api.go", "// "+mk+" BIL-901\n") // config exclude still applies
	rep, code := s.check()
	if code != 0 {
		t.Fatalf("check outside git exited %d: %v", code, findings(rep))
	}
	if statuses(rep)["ORD-001"] != rivetctx.CoverageEnforced || statuses(rep)["BIL-002"] != rivetctx.CoverageUntested {
		t.Fatalf("directory-walk fallback disagrees with git scan: %v", statuses(rep))
	}
}

// ---------------------------------------------------------------------------
// Which rules does a change touch?
// ---------------------------------------------------------------------------

func TestIntentHarness_AffectedWorkingTree(t *testing.T) {
	s := newShop(t)
	// A code edit to invoice.go that leaves its markers alone.
	s.edit("services/billing/invoice.go", "\tinv.Lines = lines\n", "\tinv.Lines = append([]int64(nil), lines...)\n")

	rep := s.affected()
	if !equalStrings(rep.ChangedFiles, []string{"services/billing/invoice.go"}) {
		t.Fatalf("changed files = %v", rep.ChangedFiles)
	}
	// Markers in the file: BIL-001, BIL-002. Governed by intent/billing:
	// every active billing rule. MON-001 is cross-cutting with no
	// related_paths, so it is deliberately not listed for every change.
	if ids := affectedIDs(rep); !equalStrings(ids, []string{"BIL-001", "BIL-002", "BIL-010", "BIL-011"}) {
		t.Fatalf("affected = %v", ids)
	}
	bil1 := affectedRule(t, rep, "BIL-001")
	requireContains(t, "BIL-001 reasons", strings.Join(bil1.Reasons, "\n"),
		"enforced in changed file services/billing/invoice.go:15",
		"governs changed file services/billing/invoice.go (intent/billing related_paths)")
	if !equalStrings(rep.Tests, []string{"services/billing/dunning.test.ts", "services/billing/invoice_test.go"}) {
		t.Fatalf("tests = %v", rep.Tests)
	}
	if len(rep.RemovedRefs)+len(rep.AddedRefs) != 0 {
		t.Fatalf("no markers moved, got removed=%v added=%v", rep.RemovedRefs, rep.AddedRefs)
	}

	text := s.run("intent", "affected").stdout
	requireContains(t, "affected text", text, "This change touches 4 business rule(s).",
		"enforced manually: finance ops approve in the ERP", "Run the tests that verify these rules:")
}

func TestIntentHarness_AffectedLostEnforcement(t *testing.T) {
	s := newShop(t)
	s.edit("services/billing/invoice.go", "// "+mk+" BIL-002\n", "")

	rep := s.affected()
	a := affectedRule(t, rep, "BIL-002")
	if !a.LostEnforcement {
		t.Fatalf("BIL-002 lost its only marker; report = %+v", a)
	}
	if len(rep.RemovedRefs) != 1 || rep.RemovedRefs[0].ID != "BIL-002" {
		t.Fatalf("removed refs = %+v", rep.RemovedRefs)
	}
	requireContains(t, "reasons", strings.Join(a.Reasons, "\n"), ""+mk+" marker removed from services/billing/invoice.go")

	// Moving a marker down a few lines in the same file is not a removal.
	s.git("checkout", "--", ".")
	s.edit("services/billing/invoice.go", "import \"errors\"\n", "import \"errors\"\n\n// padding\n// padding\n")
	rep = s.affected()
	if len(rep.RemovedRefs)+len(rep.AddedRefs) != 0 {
		t.Fatalf("a marker that only changed line number was reported as moved: removed=%v added=%v", rep.RemovedRefs, rep.AddedRefs)
	}
}

func TestIntentHarness_AffectedRuleDefinitionChanges(t *testing.T) {
	s := newShop(t)
	s.edit(".rivet/intent/domains/billing.md", "start 14 days after", "start 7 days after")
	// Retire ORD-001 by moving it under a Retired heading.
	s.edit(".rivet/intent/domains/orders.md", "## Invariants\n", "## Retired\n")

	rep := s.affected()
	changes := map[string]string{}
	for _, rc := range rep.RuleChanges {
		changes[rc.ID] = rc.Change
	}
	if changes["BIL-010"] != "modified" || changes["ORD-001"] != "retired" || len(changes) != 2 {
		t.Fatalf("rule changes = %+v", rep.RuleChanges)
	}
	for _, rc := range rep.RuleChanges {
		if rc.ID == "BIL-010" && (!strings.Contains(rc.Before, "14 days") || !strings.Contains(rc.After, "7 days")) {
			t.Fatalf("BIL-010 before/after = %q / %q", rc.Before, rc.After)
		}
	}
	// The tests proving the edited rules are what must be re-run (and the
	// code they test is what must change with the rule).
	bil10 := affectedRule(t, rep, "BIL-010")
	if !equalStrings(bil10.Tests, []string{"services/billing/dunning.test.ts"}) || !equalStrings(bil10.Code, []string{"services/billing/dunning.ts"}) {
		t.Fatalf("BIL-010 code=%v tests=%v", bil10.Code, bil10.Tests)
	}

	// And the check now flags the C# markers still enforcing a retired rule.
	check, code := s.check()
	if code == 0 {
		t.Fatal("retiring a rule while code still enforces it must fail the check")
	}
	requireFinding(t, check, "retired-rule-reference", rivetctx.SeverityError, "Checkout.cs")
}

func TestIntentHarness_AffectedSinceBranchPoint(t *testing.T) {
	s := newShop(t)
	s.git("checkout", "-q", "-b", "feature")
	s.edit("services/orders/Acme.Orders/Checkout.cs", "throw new InvalidOperationException(\"insufficient stock\");", "throw new InvalidOperationException(\"out of stock\");")
	s.commitAll("feature work")
	// main moves on independently; --since must use the merge-base, so
	// main's own change is not attributed to this branch.
	s.git("checkout", "-q", "main")
	s.edit("lib/money/money.go", "type Cents int64", "type Cents int64 // minor units")
	s.commitAll("main work")
	s.git("checkout", "-q", "feature")

	rep := s.affected("--since", "main")
	if !equalStrings(rep.ChangedFiles, []string{"services/orders/Acme.Orders/Checkout.cs"}) {
		t.Fatalf("changed files = %v (main's commit must not leak in)", rep.ChangedFiles)
	}
	if !equalStrings(affectedIDs(rep), []string{"ORD-001"}) {
		t.Fatalf("affected = %v", affectedIDs(rep))
	}
	if !equalStrings(rep.Tests, []string{"services/orders/Acme.Orders.Tests/Oversell.cs"}) {
		t.Fatalf("tests = %v", rep.Tests)
	}
}

func TestIntentHarness_AffectedStagedOnly(t *testing.T) {
	s := newShop(t)
	s.edit("lib/money/money.go", "type Cents int64", "type Cents int64 // minor units")
	s.git("add", "lib/money/money.go")
	s.edit("services/billing/invoice.go", "\tinv.Lines = lines\n", "\tinv.Lines = append([]int64(nil), lines...)\n") // unstaged

	rep := s.affected("--staged")
	if !equalStrings(rep.ChangedFiles, []string{"lib/money/money.go"}) {
		t.Fatalf("staged changed files = %v", rep.ChangedFiles)
	}
	if !equalStrings(affectedIDs(rep), []string{"MON-001"}) {
		t.Fatalf("staged affected = %v (unstaged billing edit leaked in)", affectedIDs(rep))
	}
}

func TestIntentHarness_InheritedRelatedPaths(t *testing.T) {
	s := newShop(t)
	// intent/orders declares no related_paths; it inherits the orders domain
	// doc's, which cover the browser checkout. Nothing in cart.ts is marked.
	rep := s.affected("web/src/checkout/cart.ts")
	if !equalStrings(affectedIDs(rep), []string{"ORD-001"}) {
		t.Fatalf("affected = %v", affectedIDs(rep))
	}

	out := s.run("intent", "for", "web/src/checkout/cart.ts").stdout
	requireContains(t, "intent for", out, "intent/orders", "ORD-001", "intent/money", "cross-cutting, applies everywhere")
	requireNotContains(t, "intent for", out, "intent/billing")

	// Segment-safe matching: a sibling directory sharing a name prefix is
	// not governed.
	out = s.run("intent", "for", "services/billing-legacy/old.go").stdout
	requireNotContains(t, "intent for billing-legacy", out, "intent/billing")
}

// ---------------------------------------------------------------------------
// The agent's view: rivet serve over MCP stdio
// ---------------------------------------------------------------------------

type mcpReply struct {
	ID     int `json:"id"`
	Result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError   bool              `json:"isError"`
		Tools     []json.RawMessage `json:"tools"`
		Resources []struct {
			URI string `json:"uri"`
		} `json:"resources"`
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (r mcpReply) text() string {
	var parts []string
	for _, c := range r.Result.Content {
		parts = append(parts, c.Text)
	}
	return strings.Join(parts, "\n<<content-block>>\n")
}

// mcp runs one `rivet serve` session: initialize, then each request in order.
// Replies are returned by request index.
func (s *shop) mcp(requests ...map[string]interface{}) []mcpReply {
	s.t.Helper()
	var in bytes.Buffer
	enc := json.NewEncoder(&in)
	_ = enc.Encode(map[string]interface{}{"jsonrpc": "2.0", "id": 0, "method": "initialize", "params": map[string]interface{}{}})
	for i, req := range requests {
		req["jsonrpc"] = "2.0"
		req["id"] = i + 1
		_ = enc.Encode(req)
	}

	cmd := exec.Command(s.bin, "serve")
	cmd.Dir = s.dir
	cmd.Env = append(os.Environ(), "HOME="+s.home, "XDG_CONFIG_HOME="+filepath.Join(s.home, ".config"), "RIVET_EMBED_BACKEND=")
	cmd.Stdin = &in
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		s.t.Fatalf("rivet serve: %v\n%s", err, errb.String())
	}

	replies := make([]mcpReply, len(requests))
	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		var r mcpReply
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			s.t.Fatalf("bad JSON-RPC line %q: %v", sc.Text(), err)
		}
		if r.ID >= 1 && r.ID <= len(requests) {
			replies[r.ID-1] = r
		}
	}
	return replies
}

func toolCall(name string, args map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"method": "tools/call", "params": map[string]interface{}{"name": name, "arguments": args}}
}

func TestIntentHarness_MCPAgentView(t *testing.T) {
	s := newShop(t)
	r := s.mcp(
		map[string]interface{}{"method": "tools/list", "params": map[string]interface{}{}},
		toolCall("rivet.intent", map[string]interface{}{"id": "BIL-001"}),
		toolCall("rivet.intent", map[string]interface{}{"path": "services/orders/Acme.Orders/Checkout.cs"}),
		toolCall("rivet.intent", map[string]interface{}{"query": "refund an issued invoice"}),
		toolCall("rivet.context-recommend", map[string]interface{}{"query": "edit an issued invoice"}),
		toolCall("rivet.context-show", map[string]interface{}{"name": "intent/billing"}),
		map[string]interface{}{"method": "resources/list", "params": map[string]interface{}{}},
		map[string]interface{}{"method": "resources/read", "params": map[string]interface{}{"uri": "rivet://intent/orders"}},
		toolCall("rivet.intent", map[string]interface{}{}),
		toolCall("rivet.intent", map[string]interface{}{"id": "NOPE-1"}),
		toolCall("rivet.intent", map[string]interface{}{"id": "BIL-001", "path": "x.go"}),
		toolCall("rivet.context-list", map[string]interface{}{}),
	)

	var names []string
	for _, raw := range r[0].Result.Tools {
		var tl struct{ Name string }
		_ = json.Unmarshal(raw, &tl)
		names = append(names, tl.Name)
	}
	requireContains(t, "tools/list", strings.Join(names, ","), "rivet.intent,rivet.intent-propose")

	requireContains(t, "rivet.intent id", r[1].text(),
		"BIL-001 [invariant]", "why: tax law requires", "coverage: enforced",
		"services/billing/invoice.go:15", "services/billing/invoice_test.go:5")

	requireContains(t, "rivet.intent path", r[2].text(),
		"These are business rules", "intent/orders", "ORD-001 [invariant]", "line 7: ORD-001", "intent/money")
	requireNotContains(t, "rivet.intent path", r[2].text(), "BIL-001")

	requireContains(t, "rivet.intent query", r[3].text(), "[intent] intent/billing", "BIL-001")

	rec := r[4].text()
	requireContains(t, "context-recommend", rec, "Business rules (intent) that apply:", "BIL-001", "Recommended context for")
	if strings.Index(rec, "Business rules (intent)") > strings.Index(rec, "Recommended context for") {
		t.Fatalf("rules must come before descriptive context:\n%s", rec)
	}

	requireContains(t, "context-show intent/billing", r[5].text(), "## Invariants", "**BIL-001**")

	var uris []string
	for _, res := range r[6].Result.Resources {
		uris = append(uris, res.URI)
	}
	requireContains(t, "resources/list", strings.Join(uris, " "), "rivet://intent/billing", "rivet://intent/orders", "rivet://intent/money")
	if len(r[7].Result.Contents) != 1 || !strings.Contains(r[7].Result.Contents[0].Text, "ORD-001") {
		t.Fatalf("resources/read rivet://intent/orders = %+v (error %+v)", r[7].Result.Contents, r[7].Error)
	}

	requireContains(t, "rivet.intent list", r[8].text(), "Intent documents (3)", "intent/billing", "intent/money", "intent/orders")
	requireNotContains(t, "rivet.intent list", r[8].text(), "BIL-003") // retired rules aren't listed as current

	if !r[9].Result.IsError {
		t.Fatalf("an unknown rule ID must be an error: %s", r[9].text())
	}
	if !r[10].Result.IsError {
		t.Fatalf("two modes at once must be an error: %s", r[10].text())
	}
	requireContains(t, "context-list", r[11].text(), "intent/billing", "intent")
}

// rivet:intent CTX-002, CTX-003
func TestIntentHarness_MCPProposalsNeverChangeRules(t *testing.T) {
	s := newShop(t)
	before := s.read(".rivet/intent/domains/billing.md")

	r := s.mcp(
		toolCall("rivet.intent-propose", map[string]interface{}{
			"change": "amend", "rule_id": "BIL-010",
			"statement": "Dunning reminders start 7 days after the due date.",
			"why":       "Finance shortened the collection window (user request).",
			"evidence":  "services/billing/dunning.ts:2",
		}),
		toolCall("rivet.intent-propose", map[string]interface{}{"change": "add", "rule_id": "BIL-001", "statement": "x", "why": "y"}),
		toolCall("rivet.intent-propose", map[string]interface{}{"change": "amend", "rule_id": "BIL-777", "statement": "x", "why": "y"}),
		toolCall("rivet.intent-propose", map[string]interface{}{"change": "amend", "rule_id": "BIL-010", "statement": "x"}),
		toolCall("rivet.intent-propose", map[string]interface{}{"change": "retire", "rule_id": "BIL-003", "why": "y"}),
		toolCall("rivet.intent-propose", map[string]interface{}{"change": "add", "doc": "billing", "statement": "Credit notes reference the invoice they correct.", "why": "audit"}),
	)

	if r[0].Result.IsError {
		t.Fatalf("valid proposal refused: %s", r[0].text())
	}
	requireContains(t, "propose reply", r[0].text(), ".rivet/intent/proposals/", "changes NOTHING")
	for i, why := range map[int]string{1: "reusing an ID", 2: "amending an unknown rule", 3: "omitting why", 4: "retiring a retired rule"} {
		if !r[i].Result.IsError {
			t.Errorf("proposal %s must be refused, got: %s", why, r[i].text())
		}
	}
	if r[5].Result.IsError {
		t.Fatalf("add without an ID refused: %s", r[5].text())
	}

	// The ratified doc is untouched, and the proposal is not loaded as intent.
	if s.read(".rivet/intent/domains/billing.md") != before {
		t.Fatal("a proposal modified the ratified intent doc")
	}
	list := s.run("intent", "list").stdout
	requireContains(t, "intent list", list, "BIL-010 [policy] Dunning reminders start 14 days")
	requireNotContains(t, "intent list", list, "7 days", "proposal")
	if rep, code := s.check(); code != 0 {
		t.Fatalf("a filed proposal must not affect the check: %v", findings(rep))
	}

	props := s.run("intent", "proposals", "--json")
	var listed []rivetctx.IntentProposal
	if err := json.Unmarshal([]byte(props.stdout), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("want 2 proposals, got %+v", listed)
	}
	found := false
	for _, p := range listed {
		if p.Change == "amend" && p.RuleID == "BIL-010" && p.Doc == "intent/billing" {
			found = true
			requireContains(t, "proposal file", s.read(p.Path), "- **BIL-010** Dunning reminders start 7 days", "why: Finance shortened", "services/billing/dunning.ts:2")
		}
	}
	if !found {
		t.Fatalf("amend BIL-010 proposal missing from %+v", listed)
	}
}

func TestIntentHarness_MCPChangeAwareness(t *testing.T) {
	s := newShop(t)
	s.git("checkout", "-q", "-b", "feature")
	s.edit("services/orders/Acme.Orders/Checkout.cs", "        // "+mk+" ORD-001\n", "")
	s.commitAll("drop the stock check marker")
	s.edit("services/billing/invoice.go", "\tinv.Lines = lines\n", "\tinv.Lines = append([]int64(nil), lines...)\n")

	r := s.mcp(
		toolCall("rivet.intent", map[string]interface{}{"changes": true}),
		toolCall("rivet.intent", map[string]interface{}{"since": "main"}),
		toolCall("witness.since", map[string]interface{}{"ref": "main"}),
		toolCall("witness.select", map[string]interface{}{"args": []string{"web/src/checkout/cart.ts"}}),
		toolCall("witness.select", map[string]interface{}{"args": []string{"README.md"}}),
	)

	// Working tree vs HEAD: only the uncommitted invoice.go edit.
	changes := r[0].text()
	requireContains(t, "intent changes", changes, "BIL-001", "invoice_test.go")
	requireNotContains(t, "intent changes", changes, "ORD-001")

	// Since main: also the committed marker removal, which leaves ORD-001
	// enforced only by its test.
	since := r[1].text()
	requireContains(t, "intent since", since, "ORD-001", ""+mk+" marker removed from services/orders/Acme.Orders/Checkout.cs", "BIL-001")

	// Witness selections carry the rule-aware tests in a second content block,
	// leaving witness's own output as the first block untouched.
	for i, label := range map[int]string{2: "witness.since", 3: "witness.select path"} {
		if len(r[i].Result.Content) < 2 {
			t.Fatalf("%s: want witness output plus an intent block, got %d block(s): %s", label, len(r[i].Result.Content), r[i].text())
		}
		note := r[i].Result.Content[len(r[i].Result.Content)-1].Text
		requireContains(t, label+" intent block", note, "[rivet] This change touches business rules", "ORD-001 (invariant)",
			"services/orders/Acme.Orders.Tests/Oversell.cs")
		if strings.Contains(r[i].Result.Content[0].Text, "[rivet]") {
			t.Fatalf("%s: intent note leaked into witness's own output block", label)
		}
	}
	if len(r[4].Result.Content) != 1 {
		t.Fatalf("a change touching no rules must not add an intent block: %s", r[4].text())
	}
}

func TestIntentHarness_NoIntentDocsIsQuiet(t *testing.T) {
	s := newShop(t)
	if err := os.RemoveAll(filepath.Join(s.dir, ".rivet", "intent")); err != nil {
		t.Fatal(err)
	}
	r := s.run("intent", "check")
	if r.code != 0 {
		t.Fatalf("no intent docs must not fail: %s", r.stdout)
	}
	requireContains(t, "intent check", r.stdout, "No intent documents found")

	// Markers with no docs are not checked: a project that hasn't adopted
	// intent is never failed by it.
	if lint := s.run("context", "lint"); lint.code != 0 {
		t.Fatalf("context lint without intent docs exited %d:\n%s", lint.code, lint.stdout)
	}

	m := s.mcp(
		toolCall("rivet.intent", map[string]interface{}{"path": "services/billing/invoice.go"}),
		toolCall("witness.select", map[string]interface{}{"args": []string{"services/billing/invoice.go"}}),
	)
	requireContains(t, "rivet.intent", m[0].text(), "No intent documents found")
	if len(m[1].Result.Content) != 1 {
		t.Fatal("no intent docs must mean no intent block on witness output")
	}
}

func TestIntentHarness_SyncListsIntentDocs(t *testing.T) {
	s := newShop(t)
	if r := s.run("sync", "--provider", "claude"); r.code != 0 {
		t.Fatalf("sync: %s%s", r.stdout, r.stderr)
	}
	requireContains(t, "CLAUDE.md", s.read("CLAUDE.md"),
		"**Business rules (intent):** intent/billing, intent/money, intent/orders",
		"### Business rules (intent)", "`rivet.intent`")
}
