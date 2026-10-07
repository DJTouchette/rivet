package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mk is the marker keyword, assembled so this file never holds a literal
// marker: rivet scans its own source for them in CI.
const mk = "rivet:" + "intent"

const billingIntent = `---
tags: [billing]
owner: finance
last_ratified: 2099-01-01
prefix: BIL
---

# Billing — intent

## Purpose

Rules for billing. A prose mention of BIL-404 is not a rule.

` + "```markdown\n- **BIL-900** a rule shown inside a code fence is an example\n```" + `

<!--
- **BIL-901** a commented-out draft
-->

## Invariants

- **BIL-001** An issued invoice is never edited;
  corrections are credit notes.
  why: tax law requires an immutable
    audit trail
- **BIL-002**: Totals equal the sum of lines.

### Rounding

- **BIL-005** — Totals round half-even.
  why: matches the ledger
  enforced: manual — finance reconciles monthly

## Policies

* **BIL-010** Dunning starts 14 days after due.
  enforced: pending (DUN-12)
- **BIL-011** Large invoices need approval.
  enforced: by the ERP

Some trailing prose that is not part of BIL-011.

## Retired

- ~~**BIL-003**~~ Invoices are emailed as PDFs.
`

func parseBilling(t *testing.T) []Rule {
	t.Helper()
	_, body := parseFrontmatter(billingIntent)
	return ParseRules("intent/billing", "billing.md", body, bodyLineOffset(billingIntent, body))
}

func TestParseRules(t *testing.T) {
	rules := parseBilling(t)
	byID := map[string]Rule{}
	var ids []string
	for _, r := range rules {
		byID[r.ID] = r
		ids = append(ids, r.ID)
	}

	if got, want := strings.Join(ids, ","), "BIL-001,BIL-002,BIL-005,BIL-010,BIL-011,BIL-003"; got != want {
		t.Fatalf("rule IDs = %s, want %s (fenced and commented rules must be skipped)", got, want)
	}

	r := byID["BIL-001"]
	if r.Class != RuleInvariant || r.Statement != "An issued invoice is never edited; corrections are credit notes." {
		t.Errorf("BIL-001 = %+v", r)
	}
	if r.Why != "tax law requires an immutable audit trail" {
		t.Errorf("BIL-001 why = %q (continuation lines join)", r.Why)
	}
	// Line numbers are file lines, frontmatter included.
	lines := strings.Split(billingIntent, "\n")
	if !strings.Contains(lines[r.Line-1], "**BIL-001**") {
		t.Errorf("BIL-001 line %d is %q", r.Line, lines[r.Line-1])
	}

	if byID["BIL-002"].Statement != "Totals equal the sum of lines." {
		t.Errorf("colon after ID: %q", byID["BIL-002"].Statement)
	}
	if b5 := byID["BIL-005"]; b5.Class != RuleInvariant || b5.Enforcement != EnforcementManual || b5.EnforcementNote != "finance reconciles monthly" || b5.Statement != "Totals round half-even." {
		t.Errorf("BIL-005 (H3 subgroup inside Invariants) = %+v", b5)
	}
	if b10 := byID["BIL-010"]; b10.Class != RulePolicy || b10.Enforcement != EnforcementPending || b10.EnforcementNote != "DUN-12)" {
		t.Errorf("BIL-010 = %+v", b10)
	}
	if b11 := byID["BIL-011"]; b11.Enforcement != "by the ERP" || strings.Contains(b11.Statement, "trailing") {
		t.Errorf("BIL-011 = %+v (unindented prose ends a rule; unknown enforcement kept raw)", b11)
	}
	if b3 := byID["BIL-003"]; b3.Class != RuleRetired || b3.Active() {
		t.Errorf("BIL-003 = %+v", b3)
	}
	for _, r := range rules {
		if r.OutsideSection {
			t.Errorf("%s flagged outside section", r.ID)
		}
	}
}

func TestParseRulesOutsideSection(t *testing.T) {
	rules := ParseRules("d", "p", "# T\n\n## Purpose\n\n- **X-1** stray\n", 0)
	if len(rules) != 1 || !rules[0].OutsideSection || rules[0].Class != RuleInvariant {
		t.Fatalf("stray rule = %+v; want held as invariant and flagged", rules)
	}
}

func TestParseEnforcement(t *testing.T) {
	cases := []struct{ in, mode, note string }{
		{"manual — ops checks", EnforcementManual, "ops checks"},
		{"Manual: quarterly audit", EnforcementManual, "quarterly audit"},
		{"manual", EnforcementManual, ""},
		{"pending", EnforcementPending, ""},
		{"code", "", ""},
		{"tests", "", ""},
		{"somewhere", "somewhere", ""},
	}
	for _, c := range cases {
		mode, note := parseEnforcement(c.in)
		if mode != c.mode || note != c.note {
			t.Errorf("parseEnforcement(%q) = (%q, %q), want (%q, %q)", c.in, mode, note, c.mode, c.note)
		}
	}
}

func TestIsTestFile(t *testing.T) {
	tests := []string{
		"services/billing/invoice_test.go",
		"web/src/cart.test.ts", "web/src/cart.spec.tsx",
		"tools/test_reconcile.py", "tools/reconcile_test.py",
		"spec/models/invoice_spec.rb", "test/shop/cart_test.exs",
		"src/test/java/com/acme/InvoiceTest.java", "app/src/InvoiceTests.kt",
		"Acme.Billing.Tests/Invoices.cs", "Acme.Billing.UnitTests/Invoices.cs", "Acme.Billing.Test/Invoices.cs",
		"Billing/InvoiceTests.cs", "web/__tests__/cart.js", "e2e/checkout.ts",
	}
	notTests := []string{
		"services/billing/invoice.go", "web/src/cart.ts", "Billing/Latest.cs",
		"Billing/Contest.java", "testimony/notes.go", "lib/attestation.ex", "Test.cs",
	}
	for _, p := range tests {
		if !IsTestFile(p) {
			t.Errorf("IsTestFile(%q) = false, want true", p)
		}
	}
	for _, p := range notTests {
		if IsTestFile(p) {
			t.Errorf("IsTestFile(%q) = true, want false", p)
		}
	}
}

func TestGovernsPath(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"services/billing/**", "services/billing/invoice.go", true},
		{"services/billing/**", "services/billing/deep/x/y.go", true},
		{"services/billing/**", "services/billing-legacy/x.go", false},
		{"services/billing/*", "services/billing/deep/x.go", true}, // dir/* treated as dir/**
		{"services/**/*.cs", "services/orders/Acme/Checkout.cs", true},
		{"services/**/*.cs", "services/orders/Acme/Checkout.go", false},
		{"**/money.go", "lib/money/money.go", true},
		{"./lib/money/money.go", "lib/money/money.go", true},
		{"lib/*.go", "lib/money/money.go", false},
	}
	for _, c := range cases {
		if got := governsPath(c.pattern, c.path); got != c.want {
			t.Errorf("governsPath(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestRefsOnLine(t *testing.T) {
	refs := refsOnLine("a_test.go", 3, "// "+mk+" BIL-001, MON-002 — totals, also "+mk+": ORD-9")
	var ids []string
	for _, r := range refs {
		ids = append(ids, r.ID)
		if !r.Test || r.Line != 3 {
			t.Errorf("ref %+v", r)
		}
	}
	if strings.Join(ids, ",") != "BIL-001,MON-002,ORD-9" {
		t.Fatalf("ids = %v", ids)
	}
	if got := refsOnLine("a.go", 1, "// "+mk+" <ID> placeholder, and BIL-001 in prose"); len(got) != 0 {
		t.Fatalf("placeholder and bare ID must not count: %+v", got)
	}
}

func intentDoc(t *testing.T, name, raw string) *Document {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := readIntentDoc(path, name, IntentScopeDomain)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestCheckIntentCoverage(t *testing.T) {
	doc := intentDoc(t, "billing", billingIntent)
	refs := []RuleRef{
		{ID: "BIL-001", File: "invoice.go", Line: 1},
		{ID: "BIL-001", File: "invoice_test.go", Line: 1, Test: true},
		{ID: "BIL-002", File: "invoice_test.go", Line: 2, Test: true},
		{ID: "BIL-003", File: "mailer.go", Line: 4},
		{ID: "BIL-404", File: "x.go", Line: 5},
	}
	rep := CheckIntent([]*Document{doc}, refs)

	want := map[string]string{
		"BIL-001": CoverageEnforced,
		"BIL-002": CoverageTestOnly,
		"BIL-005": CoverageManual,
		"BIL-010": CoveragePending,
		"BIL-011": CoverageUnenforced,
		"BIL-003": CoverageRetired,
	}
	for _, c := range rep.Rules {
		if want[c.Rule.ID] != c.Status {
			t.Errorf("%s status %q, want %q", c.Rule.ID, c.Status, want[c.Rule.ID])
		}
	}

	got := map[string]Severity{}
	for _, w := range rep.Warnings {
		got[w.Rule] = w.Severity
	}
	wantFindings := map[string]Severity{
		"pending-enforcement":    SeverityWarning, // BIL-010
		"unenforced-policy":      SeverityWarning, // BIL-011 (raw enforcement value, nothing marks it)
		"retired-rule-reference": SeverityError,   // mailer.go
		"unknown-rule-reference": SeverityError,   // BIL-404
	}
	if len(got) != len(wantFindings) {
		t.Errorf("findings %v, want %v", got, wantFindings)
	}
	for rule, sev := range wantFindings {
		if got[rule] != sev {
			t.Errorf("finding %s = %q, want %q", rule, got[rule], sev)
		}
	}
	if !rep.HasErrors() || len(rep.Unknown) != 1 || len(rep.Retired) != 1 {
		t.Errorf("unknown=%v retired=%v", rep.Unknown, rep.Retired)
	}
}

// rivet:intent CTX-003
func TestCheckIntentDuplicateAndUntested(t *testing.T) {
	a := intentDoc(t, "a", "# A\n\n## Invariants\n\n- **X-1** one\n  why: w\n")
	b := intentDoc(t, "b", "# B\n\n## Invariants\n\n- **X-1** other\n  why: w\n- **X-2** two\n  why: w\n")
	rep := CheckIntent([]*Document{a, b}, []RuleRef{{ID: "X-1", File: "a.go"}, {ID: "X-2", File: "b.go"}})
	var dup, untested int
	for _, w := range rep.Warnings {
		switch w.Rule {
		case "duplicate-rule-id":
			dup++
			if w.Severity != SeverityError {
				t.Error("duplicate-rule-id must be an error")
			}
		case "untested-invariant":
			untested++
		}
	}
	if dup != 1 {
		t.Errorf("duplicate-rule-id reported %d times, want once", dup)
	}
	if untested != 3 { // X-1 twice (one per definition) and X-2
		t.Errorf("untested-invariant = %d, want 3", untested)
	}
}

func TestLintIntentDoc(t *testing.T) {
	stale := time.Now().AddDate(0, 0, -(StaleRatifyDays + 10)).Format("2006-01-02")
	doc := intentDoc(t, "billing", `---
last_ratified: `+stale+`
prefix: BIL
domain: nowhere
---

# Billing

## Invariants

- **BIL-1** No reason given.
- **PAY-2** Wrong prefix.
  why: w
- **BIL-3**
  why: empty statement
- **BIL-4** Manual with no how.
  why: w
  enforced: manual
- **BIL-5** Odd enforcement.
  why: w
  enforced: sometimes
`)
	res := Lint([]*Document{doc}, t.TempDir())
	got := map[string]bool{}
	for _, w := range res.Warnings {
		got[w.Rule+":"+string(w.Severity)] = true
	}
	for _, want := range []string{
		"missing-owner:warning", "stale-ratification:warning", "missing-tags:warning",
		"missing-related-paths:warning", "unknown-domain:warning",
		"missing-rationale:warning", "rule-prefix-mismatch:error", "empty-rule:error",
		"manual-without-reason:warning", "invalid-enforcement:warning",
	} {
		if !got[want] {
			t.Errorf("missing finding %s; got %v", want, got)
		}
	}

	empty := intentDoc(t, "empty", "---\nowner: x\n---\n\n# Empty\n\nJust prose, no rules.\n")
	res = Lint([]*Document{empty}, t.TempDir())
	found := false
	for _, w := range res.Warnings {
		if w.Rule == "no-rules" && w.Severity == SeverityError {
			found = true
		}
	}
	if !found {
		t.Fatal("an intent doc with no rules must be a no-rules error")
	}
}

func TestLoadIntentAndLinkDomains(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	rule := "\n## Invariants\n\n- **R-1** r\n  why: w\n"
	write(".rivet/intent/domains/billing.md", "# Billing"+rule)
	write(".rivet/intent/domains/checkout.md", "---\ndomain: orders\n---\n# Checkout"+rule)
	write(".rivet/intent/domains/own.md", "---\nrelated_paths:\n  - \"own/**\"\n---\n# Own"+rule)
	write(".rivet/intent/cross-cutting/money.md", "# Money"+rule)
	write(".rivet/intent/root-level.md", "# Root"+rule)
	write(".rivet/intent/README.md", "# How to write intent docs")
	write(".rivet/intent/proposals/amend-r-1-abc.md", "# Proposal"+rule)
	write(".rivet/intent/domains/proposals/x.md", "# Nested proposal"+rule)

	intents, err := LoadIntent(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range intents {
		names = append(names, d.Name+"/"+string(d.Scope))
	}
	if got, want := strings.Join(names, ","), "intent/billing/domain,intent/checkout/domain,intent/money/cross-cutting,intent/own/domain,intent/root-level/domain"; got != want {
		t.Fatalf("loaded %s\nwant   %s (proposals and README are never intent)", got, want)
	}

	contexts := []*Document{
		{Name: "billing", Kind: KindDomain, RelatedPaths: []string{"svc/billing/**"}},
		{Name: "orders", Kind: KindDomain, RelatedPaths: []string{"svc/orders/**"}},
		{Name: "own", Kind: KindDomain, RelatedPaths: []string{"elsewhere/**"}},
	}
	LinkIntentDomains(intents, contexts)
	by := map[string]*Document{}
	for _, d := range intents {
		by[d.Name] = d
	}
	if d := by["intent/billing"]; d.Domain != "billing" || !d.InheritedPaths || d.RelatedPaths[0] != "svc/billing/**" {
		t.Errorf("billing link = %+v", d)
	}
	if d := by["intent/checkout"]; d.Domain != "orders" || d.RelatedPaths[0] != "svc/orders/**" {
		t.Errorf("explicit domain: link = %+v", d)
	}
	if d := by["intent/own"]; d.InheritedPaths || d.RelatedPaths[0] != "own/**" {
		t.Errorf("own related_paths must win over inheritance: %+v", d)
	}
	if d := by["intent/money"]; d.Domain != "" || len(d.RelatedPaths) != 0 {
		t.Errorf("cross-cutting must not link: %+v", d)
	}

	gov := IntentsGoverning(intents, "svc/billing/x.go")
	var gnames []string
	for _, d := range gov {
		gnames = append(gnames, d.Name)
	}
	if strings.Join(gnames, ",") != "intent/billing,intent/money" {
		t.Errorf("governing svc/billing/x.go = %v", gnames)
	}
}

// rivet:intent CTX-002
func TestIntentProposals(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".rivet", "intent")
	bad := []NewIntentProposal{
		{Change: "delete", Doc: "billing", RuleID: "BIL-1", Why: "w"},
		{Change: "amend", Doc: "billing", Statement: "s", Why: "w"},                 // no rule_id
		{Change: "amend", Doc: "billing", RuleID: "bil1", Statement: "s", Why: "w"}, // bad id
		{Change: "add", Doc: "billing", Why: "w"},                                   // no statement
		{Change: "retire", Doc: "billing", RuleID: "BIL-1"},                         // no why
		{Change: "add", Statement: "s", Why: "w"},                                   // no doc
	}
	for _, p := range bad {
		if _, err := CreateIntentProposal(dir, p); err == nil {
			t.Errorf("proposal %+v accepted", p)
		}
	}

	path, err := CreateIntentProposal(dir, NewIntentProposal{Change: "Amend", Doc: "billing", RuleID: "BIL-10", Statement: "Seven days.", Why: "finance", Evidence: "dunning.ts:2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateIntentProposal(dir, NewIntentProposal{Change: "retire", Doc: "intent/billing", RuleID: "BIL-11", Why: "obsolete"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{"change: amend", "doc: intent/billing", "rule: BIL-10", "status: proposed", "- **BIL-10** Seven days.", "why: finance", "## Evidence"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("proposal missing %q:\n%s", want, data)
		}
	}

	props, err := ListIntentProposals(dir)
	if err != nil || len(props) != 2 {
		t.Fatalf("ListIntentProposals = %+v, %v", props, err)
	}

	// A proposal is shaped like a rule but must never load as one.
	intents, err := LoadIntent(filepath.Dir(filepath.Dir(dir)))
	if err != nil || len(intents) != 0 {
		t.Fatalf("proposals loaded as intent: %+v %v", intents, err)
	}
}

func TestDiffRulesAndRefsMinus(t *testing.T) {
	before := ParseRules("d", "p", "## Invariants\n- **A-1** one\n- **A-2** two\n- **A-3** three\n## Retired\n- **A-9** old\n", 0)
	after := ParseRules("d", "p", "## Invariants\n- **A-1** one\n- **A-2** two, changed\n- **A-9** back\n- **A-4** new\n## Retired\n- **A-3** three\n", 0)
	got := map[string]string{}
	for _, c := range diffRules("d", before, after) {
		got[c.ID] = c.Change
	}
	want := map[string]string{"A-2": "modified", "A-3": "retired", "A-9": "reinstated", "A-4": "added"}
	if len(got) != len(want) {
		t.Fatalf("diff = %v, want %v", got, want)
	}
	for id, ch := range want {
		if got[id] != ch {
			t.Errorf("%s = %q, want %q", id, got[id], ch)
		}
	}
	removed := diffRules("d", before, nil)
	if len(removed) != 4 || removed[0].Change != "removed" {
		t.Errorf("deleting a doc removes all its rules: %+v", removed)
	}

	a := []RuleRef{{ID: "X-1", File: "f", Line: 1}, {ID: "X-1", File: "f", Line: 9}, {ID: "X-2", File: "g", Line: 1}}
	b := []RuleRef{{ID: "X-1", File: "f", Line: 5}}
	m := refsMinus(a, b)
	if len(m) != 2 || m[0].ID != "X-1" || m[1].ID != "X-2" {
		t.Errorf("refsMinus = %+v (line numbers must not matter; counts must)", m)
	}
}

func TestSortRulesNumeric(t *testing.T) {
	rules := []Rule{{ID: "BIL-10"}, {ID: "AAA-1"}, {ID: "BIL-2"}, {ID: "BIL-001"}}
	SortRules(rules)
	var ids []string
	for _, r := range rules {
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != "AAA-1,BIL-001,BIL-2,BIL-10" {
		t.Fatalf("sorted = %v", ids)
	}
}

// rivet:intent FC-002
func TestCheckIntentInTreeFailsClosed(t *testing.T) {
	doc := intentDoc(t, "x", "# X\n\n## Invariants\n\n- **X-1** r\n  why: w\n")
	rep := CheckIntentInTree([]*Document{doc}, filepath.Join(t.TempDir(), "does-not-exist"), ScanOptions{})
	if !rep.HasErrors() || len(rep.Warnings) != 1 || rep.Warnings[0].Rule != "intent-scan-failed" {
		t.Fatalf("an unscannable tree must be an error, not an empty pass: %+v", rep.Warnings)
	}
}
