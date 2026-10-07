package mcp

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	rivetctx "github.com/djtouchette/rivet/internal/context"
)

const noIntentText = "No intent documents found. Business rules live in .rivet/intent/{domains,cross-cutting}/ — a person creates them with 'rivet intent scaffold <domain>'."

func (s *Server) textResult(req *Request, text string, isErr bool) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentItem{{Type: "text", Text: text}}, IsError: isErr},
	}
}

func boolArg(args map[string]interface{}, key string) bool {
	b, _ := args[key].(bool)
	return b
}

// handleIntent implements rivet.intent.
func (s *Server) handleIntent(req *Request, args map[string]interface{}) *Response {
	if len(s.intents) == 0 {
		return s.textResult(req, noIntentText, false)
	}

	id := strings.TrimSpace(stringArg(args, "id"))
	path := strings.TrimSpace(stringArg(args, "path"))
	query := strings.TrimSpace(stringArg(args, "query"))
	since := strings.TrimSpace(stringArg(args, "since"))
	changes := boolArg(args, "changes")
	staged := boolArg(args, "staged")

	modes := 0
	for _, set := range []bool{id != "", path != "", query != "", since != "" || changes || staged} {
		if set {
			modes++
		}
	}
	if modes > 1 {
		return s.textResult(req, "Error: give one of 'id', 'path', 'query', or 'changes'/'since'/'staged'", true)
	}

	switch {
	case id != "":
		r, doc := rivetctx.FindRule(s.intents, id)
		if r == nil {
			return s.textResult(req, fmt.Sprintf("Error: no intent doc defines rule %q. Call rivet.intent with no arguments to list the rules.", id), true)
		}
		refs, err := rivetctx.ScanRuleRefs(s.intentRoot, s.intentOpts)
		if err != nil {
			return s.textResult(req, fmt.Sprintf("%s\n\nCaveat: could not scan for rivet:intent markers (%v), so where this rule is enforced is unknown.",
				rivetctx.FormatRuleDetail(*r, doc, nil), err), false)
		}
		var cov *rivetctx.RuleCoverage
		for _, c := range rivetctx.CheckIntent(s.intents, refs).Rules {
			if c.Rule.ID == r.ID {
				cov = &c
				break
			}
		}
		return s.textResult(req, rivetctx.FormatRuleDetail(*r, doc, cov), false)

	case path != "":
		rel := s.relToRoot(path)
		refs, err := rivetctx.ScanRuleRefs(s.intentRoot, s.intentOpts)
		text := rivetctx.FormatRulesForPath(s.intents, rel, refs)
		if err != nil {
			text += fmt.Sprintf("\nCaveat: could not scan for rivet:intent markers (%v); rules marked in this file may be missing.\n", err)
		}
		return s.textResult(req, text, false)

	case query != "":
		var opts []rivetctx.Option
		if s.semantic != nil {
			opts = append(opts, rivetctx.WithSemantic(s.semantic))
		}
		recs := rivetctx.RecommendIntent(s.intents, query, 3, opts...)
		if len(recs) == 0 {
			return s.textResult(req, fmt.Sprintf("No intent doc matches %q. That is not proof no rule applies — check rivet.intent with the 'path' of the file you're changing.", query), false)
		}
		return s.textResult(req, rivetctx.FormatIntentRecommendations(recs)+semanticCaveat(s.semantic), false)

	case since != "" || changes || staged:
		spec := rivetctx.ChangeSpec{Since: since, Staged: staged}
		rep, err := rivetctx.Affected(s.intentRoot, s.intents, spec, s.intentOpts)
		if err != nil {
			return s.textResult(req, fmt.Sprintf("Error: could not work out which rules the change touches: %v", err), true)
		}
		return s.textResult(req, rivetctx.FormatAffected(rep), false)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Intent documents (%d):\n%s\n\n", len(s.intents), rivetctx.IntentPreamble)
	for _, d := range s.intents {
		scope := string(d.Scope)
		if d.Domain != "" {
			scope += ", governs " + d.Domain
		}
		fmt.Fprintf(&b, "%s — %s (%s)\n", d.Name, d.Title, scope)
		b.WriteString(rivetctx.FormatIntentSummary(d, "  "))
		b.WriteString("\n")
	}
	b.WriteString("Call rivet.intent with 'path' for the rules governing a file, or 'changes' for the rules your change touches.")
	return s.textResult(req, b.String(), false)
}

// relToRoot turns an agent-supplied path into the slash-relative form markers
// and related_paths use. Absolute paths under the root are made relative.
func (s *Server) relToRoot(p string) string { return relTo(s.intentRoot, p) }

func relTo(root, p string) string {
	if filepath.IsAbs(p) {
		root, err := filepath.Abs(root)
		if err == nil {
			if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
				p = rel
			}
		}
	}
	return filepath.ToSlash(filepath.Clean(p))
}

// handleIntentPropose implements rivet.intent-propose: the agent's half of
// supervised rule writing. It drafts a complete change — for an add, under the
// next free ID — validated against the loaded rules, and records the current
// rule's fingerprint so a stale proposal can't be approved over a newer
// decision. It never modifies a loaded intent doc; a person applies the draft
// with `rivet intent approve`.
// rivet:intent CTX-002, CTX-003
func (s *Server) handleIntentPropose(req *Request, args map[string]interface{}) *Response {
	p := rivetctx.NewIntentProposal{
		Change:      strings.ToLower(strings.TrimSpace(stringArg(args, "change"))),
		Doc:         strings.TrimSpace(stringArg(args, "doc")),
		RuleID:      strings.TrimSpace(stringArg(args, "rule_id")),
		Class:       rivetctx.RuleClass(strings.TrimSpace(stringArg(args, "class"))),
		Statement:   stringArg(args, "statement"),
		Why:         stringArg(args, "why"),
		Enforcement: strings.TrimSpace(stringArg(args, "enforcement")),
		Scope:       rivetctx.IntentScope(strings.TrimSpace(stringArg(args, "scope"))),
		Evidence:    stringArg(args, "evidence"),
		Author:      stringArg(args, "author"),
	}
	if p.Doc != "" && !strings.HasPrefix(p.Doc, "intent/") {
		p.Doc = "intent/" + p.Doc
	}

	switch p.Change {
	case rivetctx.ProposeAdd:
		if p.RuleID != "" {
			return s.textResult(req, "Error: omit rule_id when adding a rule — the next free ID is assigned for you, and IDs are never reused.", true)
		}
		if p.Doc == "" {
			return s.textResult(req, "Error: 'doc' is required to add a rule (an existing intent doc such as 'intent/billing', or a new one to create).", true)
		}
		var doc *rivetctx.Document
		for _, d := range s.intents {
			if d.Name == p.Doc {
				doc = d
			}
		}
		p.RuleID = rivetctx.NextRuleID(s.intents, doc, p.Doc)
	case rivetctx.ProposeAmend, rivetctx.ProposeRetire:
		if p.RuleID == "" {
			return s.textResult(req, fmt.Sprintf("Error: 'rule_id' is required to %s a rule.", p.Change), true)
		}
		r, doc := rivetctx.FindRule(s.intents, p.RuleID)
		if r == nil {
			return s.textResult(req, fmt.Sprintf("Error: no intent doc defines %s. Call rivet.intent with no arguments to list the rules.", p.RuleID), true)
		}
		if p.Change == rivetctx.ProposeRetire && !r.Active() {
			return s.textResult(req, fmt.Sprintf("Error: %s is already retired.", p.RuleID), true)
		}
		if p.Doc != "" && p.Doc != doc.Name {
			return s.textResult(req, fmt.Sprintf("Error: %s is defined in %s, not %s.", p.RuleID, doc.Name, p.Doc), true)
		}
		p.Doc = doc.Name
		cur := *r
		p.Current = &cur
	}

	path, err := rivetctx.CreateIntentProposal(s.intentDir, p)
	if err != nil {
		return s.textResult(req, "Error: "+err.Error(), true)
	}
	name := strings.TrimSuffix(filepath.Base(path), ".md")
	if rel, err := filepath.Rel(s.intentRoot, path); err == nil {
		path = rel
	}

	created := ""
	if p.Change == rivetctx.ProposeAdd {
		known := false
		for _, d := range s.intents {
			known = known || d.Name == p.Doc
		}
		if !known {
			created = fmt.Sprintf(" Approving it creates %s.", p.Doc)
		}
	}
	msg := fmt.Sprintf("Drafted proposal %s: %s %s in %s (%s).%s\n\n"+
		"It is NOT a rule yet: nothing changes, and the code must keep meeting the current rules, until the user approves it. "+
		"Tell the user it is waiting and ask whether to approve it now. If they want to, call rivet.intent-approve with proposal=%q — "+
		"rivet shows them the change in a confirmation prompt that only they can answer. They can also read it first with 'rivet intent review %s', "+
		"or reject it with 'rivet intent reject %s --reason ...'.\n\n"+
		"Once approved, mark the code and tests that enforce %s with rivet:intent comments.",
		name, p.Change, p.RuleID, p.Doc, filepath.ToSlash(path), created, name, name, name, p.RuleID)
	return s.textResult(req, msg, false)
}

// witnessIntentNote is appended, as its own content block, to witness test
// selections: the business rules the change touches and the tests marked as
// verifying them. Those tests are chosen by rule rather than by dependency
// graph, so they cover the case where a change breaks a rule in a file the
// graph doesn't connect to its test. "" when there is nothing to say.
func (s *Server) witnessIntentNote(tool string, args []string) string {
	if len(s.intents) == 0 {
		return ""
	}
	var spec rivetctx.ChangeSpec
	switch tool {
	case "witness.select":
		spec = witnessSelectSpec(s.intentRoot, args)
	case "witness.staged":
		spec.Staged = true
	case "witness.since":
		spec.Since = flagValue(args, "--since")
	default:
		return ""
	}

	rep, err := rivetctx.Affected(s.intentRoot, s.intents, spec, s.intentOpts)
	if err != nil {
		return fmt.Sprintf("[rivet] Could not work out which business rules this change touches (%v). Call rivet.intent with 'path' for each changed file before treating the selection as complete.", err)
	}
	if len(rep.Rules) == 0 {
		return ""
	}

	var b strings.Builder
	var ids, untested []string
	for _, a := range rep.Rules {
		ids = append(ids, fmt.Sprintf("%s (%s)", a.Rule.ID, a.Rule.Class))
		if a.Rule.Active() && len(a.Tests) == 0 && a.Rule.Enforcement != rivetctx.EnforcementManual {
			untested = append(untested, a.Rule.ID)
		}
	}
	fmt.Fprintf(&b, "[rivet] This change touches business rules: %s.\n", strings.Join(ids, ", "))
	if len(rep.Tests) > 0 {
		b.WriteString("Tests marked as verifying them — run these too, whether or not witness selected them:\n")
		for _, t := range rep.Tests {
			b.WriteString("  " + t + "\n")
		}
	}
	if len(untested) > 0 {
		fmt.Fprintf(&b, "No test is marked for %s — say so when you report that tests pass.\n", strings.Join(untested, ", "))
	}
	for _, a := range rep.Rules {
		if a.LostEnforcement {
			fmt.Fprintf(&b, "WARNING: this change removed the last rivet:intent marker for %s — nothing enforces it now.\n", a.Rule.ID)
		}
	}
	if len(rep.RuleChanges) > 0 {
		b.WriteString("This change also edits rule definitions; the code enforcing them must change with them.\n")
	}
	b.WriteString("Call rivet.intent with {\"changes\": true} (or the same 'since'/'staged') for the rule text and why each is touched.")
	return b.String()
}

// witnessValueFlags are witness select flags that take a value, so the value
// isn't mistaken for a changed path.
var witnessValueFlags = map[string]bool{
	"--kind": true, "--exclude": true, "--max": true, "--min-score": true,
	"--format": true, "--since": true, "--root": true,
}

// witnessSelectSpec mirrors witness select's own input: explicit paths when
// given, --since/--staged when passed through, else the working tree.
func witnessSelectSpec(root string, args []string) rivetctx.ChangeSpec {
	var spec rivetctx.ChangeSpec
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--staged":
			spec.Staged = true
		case a == "--since" && i+1 < len(args):
			spec.Since = args[i+1]
			i++
		case strings.HasPrefix(a, "--since="):
			spec.Since = strings.TrimPrefix(a, "--since=")
		case witnessValueFlags[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			// Not checked against the disk: a deleted file is still a change.
			spec.Paths = append(spec.Paths, relTo(root, a))
		}
	}
	if spec.Staged {
		spec.Since, spec.Paths = "", nil
	} else if spec.Since != "" {
		spec.Paths = nil
	}
	return spec
}

func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, flag+"=") {
			return strings.TrimPrefix(a, flag+"=")
		}
	}
	return ""
}

// handleIntentApprove implements rivet.intent-approve: the person's half of
// supervised rule writing, inside the agent's own session. The agent may call
// it; it cannot pass it. The decision is asked of the person through MCP
// elicitation — a prompt the client shows the user directly, whose answer
// never passes through the model — and a client that can't ask is refused
// rather than approving without asking.
func (s *Server) handleIntentApprove(req *Request, args map[string]interface{}) *Response {
	ref := strings.TrimSpace(stringArg(args, "proposal"))
	if ref == "" {
		return s.textResult(req, "Error: 'proposal' is required — the name from rivet.intent-propose's reply.", true)
	}
	path, err := rivetctx.ResolveIntentProposal(s.intentDir, ref)
	if err != nil {
		return s.textResult(req, "Error: "+err.Error(), true)
	}
	prop, err := rivetctx.LoadIntentProposal(path)
	if err != nil {
		return s.textResult(req, "Error: "+err.Error(), true)
	}

	// Plan against the docs as they are now, not as they were at startup:
	// an earlier approval this session may have changed them.
	intents, err := rivetctx.LoadIntent(s.intentRoot)
	if err != nil {
		return s.textResult(req, "Error: loading intent docs: "+err.Error(), true)
	}
	rivetctx.LinkIntentDomains(intents, s.contexts)
	plan, err := rivetctx.PlanProposal(s.intentRoot, intents, prop, time.Now())
	if err != nil {
		return s.textResult(req, "Not approved: "+err.Error(), true)
	}

	ask := s.elicitor()
	if ask == nil {
		return s.textResult(req, "Not approved: this MCP client did not offer to ask the user directly (no elicitation support), "+
			"and rivet will not approve a rule without the user's own confirmation. Tell the user the proposal is waiting; "+
			"they can update their client (Claude Code and Codex both support elicitation), or apply it by editing the intent doc themselves.", true)
	}

	// rivet:intent CTX-004
	message := "An agent asks you to ratify a business rule. Once approved, CI holds the code to it.\n\n" +
		rivetctx.FormatProposalPlan(plan, s.intentRoot) +
		fmt.Sprintf("\nType %s to approve it. Decline to leave the rules unchanged.", plan.RuleID)
	res, err := ask(message, approvalSchema(plan.RuleID))
	if err != nil {
		return s.textResult(req, "Not approved: could not ask the user ("+err.Error()+"). Nothing changed.", true)
	}

	switch res.Action {
	case ElicitAccept:
	case ElicitDecline:
		return s.textResult(req, fmt.Sprintf("The user declined to approve %s. Nothing changed; the proposal is still pending. "+
			"Ask whether they want it redrafted, or rejected with 'rivet intent reject %s --reason ...'.", plan.RuleID, prop.Name), false)
	default:
		return s.textResult(req, fmt.Sprintf("The user dismissed the approval prompt for %s. Nothing changed; the proposal is still pending.", plan.RuleID), false)
	}
	typed, _ := res.Content["confirm"].(string)
	if strings.TrimSpace(typed) != plan.RuleID {
		return s.textResult(req, fmt.Sprintf("Not approved: the confirmation typed did not match %s. Nothing changed; the proposal is still pending.", plan.RuleID), false)
	}

	approver := rivetctx.ApproverIdentity(s.intentRoot)
	if s.client.name != "" {
		approver += " (confirmed in " + s.client.name + ")"
	}
	if err := rivetctx.ApplyProposalPlan(plan, approver, time.Now()); err != nil {
		return s.textResult(req, "Not approved: "+err.Error(), true)
	}
	if reloaded, err := rivetctx.LoadIntent(s.intentRoot); err == nil {
		rivetctx.LinkIntentDomains(reloaded, s.contexts)
		s.intents = reloaded
	}

	next := fmt.Sprintf("Mark the code and tests that enforce %s with rivet:intent comments — 'rivet intent check' fails on an invariant nothing marks.", plan.RuleID)
	if prop.Change == rivetctx.ProposeRetire {
		next = fmt.Sprintf("Remove the rivet:intent markers naming %s — 'rivet intent check' fails while code still points at a retired rule.", plan.RuleID)
	}
	return s.textResult(req, fmt.Sprintf("The user approved it: %s %s in %s is now a ratified rule (recorded as approved by %s).\n\n%s",
		prop.Change, plan.RuleID, plan.DocName, approver, next), false)
}

// approvalSchema is the form the person fills in. Two client constraints
// shape it, both verified against Codex 0.160:
//
//   - It must have a property. Codex auto-accepts an elicitation whose schema
//     has no properties when it runs with approvals off and full disk access,
//     so an empty "just confirm" schema would approve with nobody there.
//   - Each property may use only type, title, description, minLength,
//     maxLength, format and default. Codex parses fields strictly and, on any
//     other key (pattern, say), silently falls back to a bare accept/decline
//     with no input — and the typed-ID check would then reject every approval.
func approvalSchema(ruleID string) map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"confirm": map[string]interface{}{
				"type":        "string",
				"title":       "Type " + ruleID + " to approve",
				"description": "Typing the rule ID, rather than ticking a box, makes approving the wrong change by habit unlikely.",
			},
		},
		"required": []string{"confirm"},
	}
}
