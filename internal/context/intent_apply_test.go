package context

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var approvalDay = time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)

// applyRoot builds a project with one intent doc and returns its root.
func applyRoot(t *testing.T, docs map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range docs {
		p := filepath.Join(root, IntentDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func loadAll(t *testing.T, root string) []*Document {
	t.Helper()
	intents, err := LoadIntent(root)
	if err != nil {
		t.Fatal(err)
	}
	return intents
}

const ordersIntent = `---
owner: orders
last_ratified: 2025-01-01
prefix: ORD
---

# Orders — intent

## Invariants

- **ORD-001** Never oversell.
  why: cancellations cost more than lost sales
- **ORD-002** Orders have an owner.
  why: support needs a contact

## Policies

- **ORD-010** Carts expire after 30 minutes.
  why: stock is held while a cart lives

## Non-goals

- Orders do not price anything.

## Retired

- ~~**ORD-003**~~ Orders are faxed.
`

// propose files a proposal the way the MCP tool does and returns it loaded.
func propose(t *testing.T, root string, p NewIntentProposal) *IntentProposal {
	t.Helper()
	intents := loadAll(t, root)
	if p.Change == ProposeAdd && p.RuleID == "" {
		var doc *Document
		for _, d := range intents {
			if d.Name == p.Doc {
				doc = d
			}
		}
		p.RuleID = NextRuleID(intents, doc, p.Doc)
	}
	if p.Change != ProposeAdd {
		r, d := FindRule(intents, p.RuleID)
		if r != nil {
			cur := *r
			p.Current = &cur
			if p.Doc == "" {
				p.Doc = d.Name
			}
		}
	}
	path, err := CreateIntentProposal(filepath.Join(root, IntentDir), p)
	if err != nil {
		t.Fatal(err)
	}
	prop, err := LoadIntentProposal(path)
	if err != nil {
		t.Fatal(err)
	}
	return prop
}

func approve(t *testing.T, root string, prop *IntentProposal) *ProposalPlan {
	t.Helper()
	plan, err := PlanProposal(root, loadAll(t, root), prop, approvalDay)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyProposalPlan(plan, "Pat Person", approvalDay); err != nil {
		t.Fatal(err)
	}
	return plan
}

func ruleIn(t *testing.T, root, id string) (*Rule, *Document) {
	t.Helper()
	r, d := FindRule(loadAll(t, root), id)
	if r == nil {
		t.Fatalf("%s not found after approval", id)
	}
	return r, d
}

func TestNextRuleID(t *testing.T) {
	root := applyRoot(t, map[string]string{
		"domains/orders.md": ordersIntent,
		"domains/wide.md":   "---\nprefix: WID\n---\n# W\n\n## Invariants\n\n- **WID-0042** w\n\n## Retired\n\n- ~~**WID-0050**~~ old\n",
	})
	intents := loadAll(t, root)
	by := map[string]*Document{}
	for _, d := range intents {
		by[d.Name] = d
	}
	cases := []struct {
		doc  *Document
		name string
		want string
	}{
		{by["intent/orders"], "intent/orders", "ORD-011"}, // past the policy range, retired included
		{by["intent/wide"], "intent/wide", "WID-0051"},    // past a retired ID, keeping the padding
		{nil, "intent/shipping", "SHI-001"},               // new doc: prefix from the name
	}
	for _, c := range cases {
		if got := NextRuleID(intents, c.doc, c.name); got != c.want {
			t.Errorf("NextRuleID(%s) = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestApproveAddInvariant(t *testing.T) {
	root := applyRoot(t, map[string]string{"domains/orders.md": ordersIntent})
	prop := propose(t, root, NewIntentProposal{
		Change: "add", Doc: "intent/orders", Statement: "Orders over 50 units need a sales rep.",
		Why: "bulk orders get negotiated pricing", Author: "claude",
	})
	if prop.RuleID != "ORD-011" || prop.Rule == nil || prop.Rule.Class != RuleInvariant {
		t.Fatalf("proposal = %+v", prop)
	}

	plan := approve(t, root, prop)
	r, d := ruleIn(t, root, "ORD-011")
	if r.Class != RuleInvariant || r.Statement != "Orders over 50 units need a sales rep." || r.Why != "bulk orders get negotiated pricing" {
		t.Fatalf("added rule = %+v", r)
	}
	// Filed at the end of Invariants, before Policies.
	ids := []string{}
	for _, x := range d.Rules {
		ids = append(ids, x.ID)
	}
	if strings.Join(ids, ",") != "ORD-001,ORD-002,ORD-011,ORD-010,ORD-003" {
		t.Errorf("rule order = %v", ids)
	}
	if d.LastRatified.Format("2006-01-02") != "2026-10-07" {
		t.Errorf("last_ratified = %v", d.LastRatified)
	}

	// The proposal is archived with who approved it, and no longer pending.
	if _, err := os.Stat(prop.Path); !os.IsNotExist(err) {
		t.Error("proposal still pending after approval")
	}
	archived, err := os.ReadFile(filepath.Join(filepath.Dir(prop.Path), "archive", filepath.Base(prop.Path)))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"status: approved", "approved_by: Pat Person", "approved_on: 2026-10-07", "applied_as: ORD-011"} {
		if !strings.Contains(string(archived), want) {
			t.Errorf("archive missing %q:\n%s", want, archived)
		}
	}
	if pending, _ := ListIntentProposals(filepath.Join(root, IntentDir)); len(pending) != 0 {
		t.Errorf("archived proposal still listed: %+v", pending)
	}
	if !strings.Contains(LineDiff(plan.Before, plan.After), "+ - **ORD-011** Orders over 50 units need a sales rep.") {
		t.Errorf("diff:\n%s", LineDiff(plan.Before, plan.After))
	}
}

func TestApproveAmendInPlaceAndAcrossSections(t *testing.T) {
	root := applyRoot(t, map[string]string{"domains/orders.md": ordersIntent})

	// Same class: replaced where it stands.
	approve(t, root, propose(t, root, NewIntentProposal{
		Change: "amend", RuleID: "ORD-010", Statement: "Carts expire after 15 minutes.", Why: "stock turns faster now",
	}))
	r, d := ruleIn(t, root, "ORD-010")
	if r.Statement != "Carts expire after 15 minutes." || r.Class != RulePolicy || r.Why != "stock turns faster now" {
		t.Fatalf("amended = %+v", r)
	}
	if strings.Contains(d.RawBody, "30 minutes") {
		t.Fatal("old text left behind")
	}

	// Class change: moved from Policies to Invariants, with enforcement.
	approve(t, root, propose(t, root, NewIntentProposal{
		Change: "amend", RuleID: "ORD-010", Class: RuleInvariant, Statement: "Carts expire after 15 minutes.",
		Why: "legal now requires it", Enforcement: "manual — ops sweep hourly",
	}))
	r, d = ruleIn(t, root, "ORD-010")
	if r.Class != RuleInvariant || r.Enforcement != EnforcementManual || r.EnforcementNote != "ops sweep hourly" {
		t.Fatalf("reclassified = %+v", r)
	}
	if n := strings.Count(d.RawBody, "**ORD-010**"); n != 1 {
		t.Fatalf("ORD-010 appears %d times", n)
	}
	// The emptied Policies section is left in place; Non-goals are untouched.
	if !strings.Contains(d.RawBody, "## Policies") || !strings.Contains(d.RawBody, "- Orders do not price anything.") {
		t.Fatalf("doc mangled:\n%s", d.RawBody)
	}
}

func TestApproveRetire(t *testing.T) {
	root := applyRoot(t, map[string]string{"domains/orders.md": ordersIntent})
	prop := propose(t, root, NewIntentProposal{Change: "retire", RuleID: "ORD-002", Why: "accounts replaced owners"})
	if prop.Reason != "accounts replaced owners" || prop.Rule != nil {
		t.Fatalf("retire proposal = %+v", prop)
	}
	approve(t, root, prop)
	r, d := ruleIn(t, root, "ORD-002")
	if r.Active() || r.Why != "accounts replaced owners" || r.Statement != "Orders have an owner." {
		t.Fatalf("retired = %+v", r)
	}
	retired := d.RawBody[strings.Index(d.RawBody, "## Retired"):]
	if !strings.Contains(retired, "~~**ORD-002**~~") {
		t.Fatalf("not under Retired:\n%s", d.RawBody)
	}
}

func TestApproveCreatesNewDoc(t *testing.T) {
	root := applyRoot(t, map[string]string{"domains/orders.md": ordersIntent})
	prop := propose(t, root, NewIntentProposal{
		Change: "add", Doc: "shipping", Class: RulePolicy, Scope: IntentScopeCrossCutting,
		Statement: "Parcels ship within two business days.", Why: "the SLA we sell",
	})
	plan := approve(t, root, prop)
	if !plan.Created || !strings.HasSuffix(filepath.ToSlash(plan.DocPath), ".rivet/intent/cross-cutting/shipping.md") {
		t.Fatalf("plan = %+v", plan)
	}
	r, d := ruleIn(t, root, "SHI-001")
	if r.Class != RulePolicy || d.Scope != IntentScopeCrossCutting || d.Prefix != "SHI" {
		t.Fatalf("new doc rule = %+v doc = %+v", r, d)
	}
	res := Lint([]*Document{d}, root)
	for _, w := range res.Warnings {
		if w.Severity == SeverityError {
			t.Errorf("created doc has lint error %s: %s", w.Rule, w.Message)
		}
	}
}

func TestApproveFillsScaffoldPlaceholder(t *testing.T) {
	scaffold := "---\nprefix: PAY\n---\n\n# Payments — intent\n\n## Invariants\n\n<!-- Rules that must always hold -->\n\n## Policies\n\n<!-- Business decisions -->\n"
	root := applyRoot(t, map[string]string{"domains/payments.md": scaffold})
	approve(t, root, propose(t, root, NewIntentProposal{Change: "add", Doc: "payments", Statement: "Refunds go to the original method.", Why: "fraud"}))
	_, d := ruleIn(t, root, "PAY-001")
	if strings.Contains(d.RawBody, "Rules that must always hold") {
		t.Errorf("placeholder left in the section that now has a rule:\n%s", d.RawBody)
	}
	if !strings.Contains(d.RawBody, "<!-- Business decisions -->") {
		t.Errorf("placeholder removed from a section that's still empty:\n%s", d.RawBody)
	}
}

// rivet:intent CTX-004
func TestApproveRefusesStaleProposals(t *testing.T) {
	root := applyRoot(t, map[string]string{"domains/orders.md": ordersIntent})
	prop := propose(t, root, NewIntentProposal{Change: "amend", RuleID: "ORD-001", Statement: "Never oversell, ever.", Why: "w"})

	// A person changes ORD-001 after the draft was written.
	path := filepath.Join(root, IntentDir, "domains", "orders.md")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "Never oversell.", "Never oversell stock we hold.", 1)), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanProposal(root, loadAll(t, root), prop, approvalDay); !errors.Is(err, ErrProposalStale) {
		t.Fatalf("stale proposal planned: %v", err)
	}

	// And a doc edited between review and approval is refused too.
	prop2 := propose(t, root, NewIntentProposal{Change: "amend", RuleID: "ORD-002", Statement: "x", Why: "w"})
	plan, err := PlanProposal(root, loadAll(t, root), prop2, approvalDay)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	_ = os.WriteFile(path, append(data, []byte("\n")...), 0644)
	if err := ApplyProposalPlan(plan, "Pat", approvalDay); !errors.Is(err, ErrProposalStale) {
		t.Fatalf("doc changed during review but approval went through: %v", err)
	}
}

func TestApproveRenumbersTakenID(t *testing.T) {
	root := applyRoot(t, map[string]string{"domains/orders.md": ordersIntent})
	first := propose(t, root, NewIntentProposal{Change: "add", Doc: "orders", Statement: "First.", Why: "w"})
	second := propose(t, root, NewIntentProposal{Change: "add", Doc: "orders", Statement: "Second.", Why: "w"})
	if first.RuleID != second.RuleID {
		t.Fatalf("both drafts should have taken the same free ID, got %s and %s", first.RuleID, second.RuleID)
	}
	approve(t, root, first)
	plan := approve(t, root, second)
	if !plan.Renumbered || plan.RuleID != "ORD-012" {
		t.Fatalf("second approval = %s renumbered=%v", plan.RuleID, plan.Renumbered)
	}
	if r, _ := ruleIn(t, root, "ORD-012"); r.Statement != "Second." {
		t.Fatalf("ORD-012 = %+v", r)
	}
}

func TestApproveHonoursHumanEditsToTheProposal(t *testing.T) {
	root := applyRoot(t, map[string]string{"domains/orders.md": ordersIntent})
	prop := propose(t, root, NewIntentProposal{Change: "add", Doc: "orders", Statement: "Agent wording.", Why: "agent reason"})
	data, _ := os.ReadFile(prop.Path)
	edited := strings.Replace(string(data), "Agent wording.", "Person's wording.", 1)
	edited = strings.Replace(edited, "class: invariant", "class: policy", 1)
	if err := os.WriteFile(prop.Path, []byte(edited), 0644); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadIntentProposal(prop.Path)
	if err != nil {
		t.Fatal(err)
	}
	approve(t, root, reloaded)
	if r, _ := ruleIn(t, root, prop.RuleID); r.Statement != "Person's wording." || r.Class != RulePolicy {
		t.Fatalf("approved rule ignores the person's edit: %+v", r)
	}
}

func TestPlanProposalRefusals(t *testing.T) {
	root := applyRoot(t, map[string]string{"domains/orders.md": ordersIntent})
	cases := map[string]NewIntentProposal{
		"amend in missing doc": {Change: "amend", Doc: "nowhere", RuleID: "ORD-001", Statement: "s", Why: "w"},
		"amend unknown rule":   {Change: "amend", Doc: "orders", RuleID: "ORD-099", Statement: "s", Why: "w"},
		"retire retired rule":  {Change: "retire", Doc: "orders", RuleID: "ORD-003", Why: "w"},
	}
	for name, np := range cases {
		path, err := CreateIntentProposal(filepath.Join(root, IntentDir), np)
		if err != nil {
			t.Fatal(err)
		}
		prop, _ := LoadIntentProposal(path)
		if _, err := PlanProposal(root, loadAll(t, root), prop, approvalDay); err == nil {
			t.Errorf("%s: planned without error", name)
		}
	}
}

func TestRejectIntentProposal(t *testing.T) {
	root := applyRoot(t, map[string]string{"domains/orders.md": ordersIntent})
	before, _ := os.ReadFile(filepath.Join(root, IntentDir, "domains", "orders.md"))
	prop := propose(t, root, NewIntentProposal{Change: "retire", RuleID: "ORD-001", Why: "w"})
	if _, err := RejectIntentProposal(prop.Path, "Pat", "", approvalDay); err == nil {
		t.Fatal("reject without a reason accepted")
	}
	dest, err := RejectIntentProposal(prop.Path, "Pat", "we still oversell-protect", approvalDay)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dest)
	for _, want := range []string{"status: rejected", "rejected_by: Pat", "reject_reason: we still oversell-protect"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("archive missing %q", want)
		}
	}
	after, _ := os.ReadFile(filepath.Join(root, IntentDir, "domains", "orders.md"))
	if string(after) != string(before) {
		t.Fatal("rejecting changed the intent doc")
	}
	if w := ProposalWarnings(filepath.Join(root, IntentDir)); len(w) != 0 {
		t.Fatalf("rejected proposal still warned about: %+v", w)
	}
}

func TestSetFrontmatterField(t *testing.T) {
	got := strings.Join(setFrontmatterField(strings.Split("---\na: 1\n---\nbody", "\n"), "b", "2"), "\n")
	if got != "---\na: 1\nb: 2\n---\nbody" {
		t.Errorf("insert = %q", got)
	}
	got = strings.Join(setFrontmatterField(strings.Split("---\na: 1\n---\nbody", "\n"), "a", "9"), "\n")
	if got != "---\na: 9\n---\nbody" {
		t.Errorf("replace = %q", got)
	}
	got = strings.Join(setFrontmatterField([]string{"# T"}, "a", "1"), "\n")
	if got != "---\na: 1\n---\n\n# T" {
		t.Errorf("create = %q", got)
	}
}
