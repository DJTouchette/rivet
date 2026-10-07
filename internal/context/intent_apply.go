package context

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Applying a proposal is the person's half of supervised rule writing. The
// agent drafted the change; PlanProposal turns it into the exact edit to the
// intent doc so the person can read the diff, and ApplyProposalPlan writes it,
// stamps last_ratified, and archives the proposal with who approved it. The
// CLI gates ApplyProposalPlan behind an interactive confirmation — see
// `rivet intent approve`.

// ErrProposalStale means the rule changed after the proposal was drafted
// against it. Approving would silently overwrite a person's newer decision.
var ErrProposalStale = errors.New("the rule changed after this proposal was drafted")

// ProposalPlan is the edit a proposal makes, computed without writing.
type ProposalPlan struct {
	Proposal *IntentProposal
	DocName  string
	DocPath  string
	Before   string // doc content now ("" when the doc will be created)
	After    string // doc content once applied
	RuleID   string // the ID the rule ends up with
	Created  bool   // the doc doesn't exist yet
	// Renumbered is set when an added rule's ID was taken after the proposal
	// was drafted and the next free ID was used instead.
	Renumbered bool
}

// ResolveIntentProposal finds a pending proposal by path, file name, name
// without .md, or the short random suffix of its name.
func ResolveIntentProposal(intentDir, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("name a proposal; 'rivet intent proposals' lists them")
	}
	if info, err := os.Stat(ref); err == nil && !info.IsDir() {
		return ref, nil
	}
	dir := filepath.Join(intentDir, IntentProposalsSubdir)
	props, err := ListIntentProposals(intentDir)
	if err != nil {
		return "", err
	}
	ref = strings.TrimSuffix(filepath.Base(ref), ".md")
	var matches []string
	for _, p := range props {
		if p.Name == ref || strings.HasSuffix(p.Name, "-"+ref) {
			matches = append(matches, p.Path)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("no pending proposal %q in %s; 'rivet intent proposals' lists them", ref, dir)
	}
	return "", fmt.Errorf("%q matches %d proposals; use the full name", ref, len(matches))
}

// PlanProposal computes the edit a pending proposal makes to its intent doc.
// intents must be the current, loaded intent docs.
func PlanProposal(root string, intents []*Document, p *IntentProposal, today time.Time) (*ProposalPlan, error) {
	if p.Status != ProposalProposed {
		return nil, fmt.Errorf("proposal %s is already %s", p.Name, p.Status)
	}
	docName := p.Doc
	if !strings.HasPrefix(docName, "intent/") {
		docName = "intent/" + docName
	}
	plan := &ProposalPlan{Proposal: p, DocName: docName, RuleID: p.RuleID}

	var doc *Document
	for _, d := range intents {
		if d.Name == docName {
			doc = d
		}
	}

	if p.Change != ProposeRetire && p.Rule == nil {
		return nil, fmt.Errorf("proposal %s has no rule under its ## Proposed rule heading", p.Name)
	}
	if p.Change == ProposeRetire && strings.TrimSpace(p.Reason) == "" {
		return nil, fmt.Errorf("proposal %s gives no reason to retire %s", p.Name, p.RuleID)
	}

	var lines []string
	if doc == nil {
		if p.Change != ProposeAdd {
			return nil, fmt.Errorf("%s does not exist, so %s cannot be %sed", docName, p.RuleID, strings.TrimSuffix(p.Change, "e"))
		}
		plan.Created = true
		sub := "domains"
		if IntentScope(p.Scope) == IntentScopeCrossCutting {
			sub = "cross-cutting"
		}
		base := strings.TrimPrefix(docName, "intent/")
		plan.DocPath = filepath.Join(root, IntentDir, sub, base+".md")
		if _, err := os.Stat(plan.DocPath); err == nil {
			return nil, fmt.Errorf("%s exists but isn't loaded as %s — fix it before approving", plan.DocPath, docName)
		}
		lines = newIntentDocLines(base, docPrefix(nil, docName))
	} else {
		plan.DocPath = doc.Path
		plan.Before = doc.RawBody
		lines = strings.Split(doc.RawBody, "\n")
	}

	prop := p.Rule
	switch p.Change {
	case ProposeAdd:
		id := p.RuleID
		if r, _ := FindRule(intents, id); r != nil || !IsRuleID(id) ||
			(doc != nil && doc.Prefix != "" && !strings.HasPrefix(id, doc.Prefix+"-")) {
			id = NextRuleID(intents, doc, docName)
			plan.Renumbered = id != p.RuleID
		}
		plan.RuleID = id
		block := renderRuleBlock(id, prop.Class, prop.Statement, prop.Why, enforcementText(prop.Enforcement, prop.EnforcementNote))
		lines = insertIntoSection(lines, prop.Class, block)

	case ProposeAmend, ProposeRetire:
		var cur *Rule
		for i := range doc.Rules {
			if doc.Rules[i].ID == p.RuleID {
				cur = &doc.Rules[i]
			}
		}
		if cur == nil {
			if r, d := FindRule(intents, p.RuleID); r != nil {
				return nil, fmt.Errorf("%s is defined in %s, not %s", p.RuleID, d.Name, docName)
			}
			return nil, fmt.Errorf("%s no longer exists in %s", p.RuleID, docName)
		}
		if p.Base != "" && RuleFingerprint(*cur) != p.Base {
			return nil, fmt.Errorf("%w: %s now reads %q — reject this proposal and have it redrafted against the current rule",
				ErrProposalStale, cur.ID, oneLine(cur.Statement, 120))
		}
		if p.Change == ProposeRetire && !cur.Active() {
			return nil, fmt.Errorf("%s is already retired", cur.ID)
		}

		var block []string
		class := RuleRetired
		if p.Change == ProposeRetire {
			block = renderRuleBlock(cur.ID, RuleRetired, cur.Statement, p.Reason, "")
		} else {
			class = prop.Class
			block = renderRuleBlock(cur.ID, prop.Class, prop.Statement, prop.Why, enforcementText(prop.Enforcement, prop.EnforcementNote))
		}

		start, end := cur.Line-1, cur.EndLine // 0-based, end exclusive
		if class == cur.Class {
			// Same section: replace in place, keeping the rule's position.
			lines = append(append(append([]string{}, lines[:start]...), block...), lines[end:]...)
		} else {
			lines = append(append([]string{}, lines[:start]...), lines[end:]...)
			lines = insertIntoSection(lines, class, block)
		}
	default:
		return nil, fmt.Errorf("unknown change %q", p.Change)
	}

	lines = setFrontmatterField(lines, "last_ratified", today.Format("2006-01-02"))
	plan.After = strings.Join(lines, "\n")

	// Verify the edit says what the proposal says before anyone writes it:
	// re-parse the new doc and find the rule as intended.
	got := rulesFromRaw(docName, plan.DocPath, plan.After)
	var after *Rule
	for i := range got {
		if got[i].ID == plan.RuleID {
			if after != nil {
				return nil, fmt.Errorf("applying %s would leave %s defined twice in %s", p.Name, plan.RuleID, docName)
			}
			after = &got[i]
		}
	}
	switch {
	case after == nil:
		return nil, fmt.Errorf("applying %s did not produce %s in %s", p.Name, plan.RuleID, docName)
	case p.Change == ProposeRetire && after.Active():
		return nil, fmt.Errorf("applying %s left %s active", p.Name, plan.RuleID)
	case p.Change != ProposeRetire && (after.Class != prop.Class || oneLine(after.Statement, 1<<20) != oneLine(prop.Statement, 1<<20)):
		return nil, fmt.Errorf("applying %s produced %s as %q (%s), not the proposed text", p.Name, plan.RuleID, after.Statement, after.Class)
	}
	return plan, nil
}

// ApplyProposalPlan writes the planned doc and archives the proposal as
// approved by approver. It refuses if the doc changed since the plan was made.
func ApplyProposalPlan(plan *ProposalPlan, approver string, today time.Time) error {
	if strings.TrimSpace(approver) == "" {
		return fmt.Errorf("approval needs the approving person's name")
	}
	current, err := os.ReadFile(plan.DocPath)
	switch {
	case plan.Created && err == nil:
		return fmt.Errorf("%s was created while this proposal was being reviewed", plan.DocPath)
	case !plan.Created && err != nil:
		return err
	case !plan.Created && string(current) != plan.Before:
		return fmt.Errorf("%w: %s was edited during review", ErrProposalStale, plan.DocPath)
	}
	if err := os.MkdirAll(filepath.Dir(plan.DocPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(plan.DocPath, []byte(plan.After), 0644); err != nil {
		return err
	}
	_, err = archiveProposal(plan.Proposal.Path, map[string]string{
		"status":      ProposalApproved,
		"approved_by": oneLine(approver, 200),
		"approved_on": today.Format("2006-01-02"),
		"applied_as":  plan.RuleID,
	})
	return err
}

// RejectIntentProposal archives a pending proposal as rejected, with the
// reviewer and reason, without touching any intent doc.
func RejectIntentProposal(path, reviewer, reason string, today time.Time) (string, error) {
	p, err := LoadIntentProposal(path)
	if err != nil {
		return "", err
	}
	if p.Status != ProposalProposed {
		return "", fmt.Errorf("proposal %s is already %s", p.Name, p.Status)
	}
	if strings.TrimSpace(reason) == "" {
		return "", fmt.Errorf("give a reason, so whoever drafted it knows what to change")
	}
	fields := map[string]string{
		"status":        ProposalRejected,
		"rejected_on":   today.Format("2006-01-02"),
		"reject_reason": oneLine(reason, 500),
	}
	if strings.TrimSpace(reviewer) != "" {
		fields["rejected_by"] = oneLine(reviewer, 200)
	}
	return archiveProposal(path, fields)
}

// archiveProposal stamps fields into a proposal's frontmatter and moves it to
// proposals/archive/, the audit trail of decided proposals.
func archiveProposal(path string, fields map[string]string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	for _, k := range []string{"status", "approved_by", "approved_on", "applied_as", "rejected_by", "rejected_on", "reject_reason"} {
		if v, ok := fields[k]; ok {
			lines = setFrontmatterField(lines, k, v)
		}
	}
	dir := filepath.Join(filepath.Dir(path), proposalArchiveSubdir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, filepath.Base(path))
	if err := os.WriteFile(dest, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return dest, nil
}

func newIntentDocLines(base, prefix string) []string {
	title := strings.ToUpper(base[:1]) + base[1:]
	return []string{
		"---",
		"tags: [" + base + "]",
		"owner:",
		"prefix: " + prefix,
		"---",
		"",
		"# " + title + " — intent",
		"",
	}
}

// sectionHeadings are the H2 headings rules are filed under, and the order a
// missing one is created in relative to the others.
var sectionHeadings = map[RuleClass]string{
	RuleInvariant: "Invariants",
	RulePolicy:    "Policies",
	RuleRetired:   "Retired",
}

// insertIntoSection appends block to the end of the H2 section for class,
// creating the section if the doc lacks it. Single-line placeholder comments
// in that section (from a scaffold) are dropped: the section now has content.
func insertIntoSection(lines []string, class RuleClass, block []string) []string {
	bodyStart := frontmatterEnd(lines)

	isH2 := func(l string) bool { t := strings.TrimSpace(l); return strings.HasPrefix(t, "## ") }
	isH1or2 := func(l string) bool {
		t := strings.TrimSpace(l)
		return strings.HasPrefix(t, "# ") || strings.HasPrefix(t, "## ")
	}

	head := -1
	inFence := false
	for i := bodyStart; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
		}
		if inFence || !isH2(lines[i]) {
			continue
		}
		if c, ok := classifySection(strings.ToLower(strings.TrimSpace(strings.TrimLeft(t, "#")))); ok && c == class {
			head = i
			break
		}
	}

	if head < 0 {
		// Create the section: invariants and policies go before any of the
		// prose sections that follow them in the template; retired goes last.
		at := len(lines)
		if class != RuleRetired {
			for i := bodyStart; i < len(lines); i++ {
				t := strings.ToLower(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(lines[i]), "#")))
				if !isH2(lines[i]) {
					continue
				}
				if strings.HasPrefix(t, "non-goal") || strings.HasPrefix(t, "open question") || strings.HasPrefix(t, "retired") ||
					(class == RuleInvariant && strings.HasPrefix(t, "polic")) {
					at = i
					break
				}
			}
		}
		section := append([]string{"## " + sectionHeadings[class], ""}, block...)
		section = append(section, "")
		before := trimTrailingBlank(lines[:at])
		if len(before) > 0 {
			before = append(before, "")
		}
		return append(append(before, section...), lines[at:]...)
	}

	end := len(lines)
	inFence = false
	for i := head + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
		}
		if !inFence && isH1or2(lines[i]) {
			end = i
			break
		}
	}
	var sectionBody []string
	for _, l := range lines[head+1 : end] {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "<!--") && strings.HasSuffix(t, "-->") {
			continue
		}
		sectionBody = append(sectionBody, l)
	}
	sectionBody = trimTrailingBlank(sectionBody)
	if len(sectionBody) == 0 || strings.TrimSpace(sectionBody[0]) != "" {
		sectionBody = append([]string{""}, sectionBody...)
	}
	sectionBody = append(sectionBody, block...)
	if end < len(lines) {
		sectionBody = append(sectionBody, "")
	}

	out := append([]string{}, lines[:head+1]...)
	out = append(out, sectionBody...)
	out = append(out, lines[end:]...)
	if end == len(lines) && (len(out) == 0 || out[len(out)-1] != "") {
		out = append(out, "") // keep a trailing newline
	}
	return out
}

func trimTrailingBlank(lines []string) []string {
	out := append([]string{}, lines...)
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

// frontmatterEnd returns the index of the first line after the frontmatter,
// or 0 when there is none.
func frontmatterEnd(lines []string) int {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return 0
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return i + 1
		}
	}
	return 0
}

// setFrontmatterField sets key: value in the frontmatter, replacing an
// existing line or adding one before the closing fence, and creating the
// frontmatter if the file has none.
func setFrontmatterField(lines []string, key, value string) []string {
	end := frontmatterEnd(lines)
	if end == 0 {
		return append([]string{"---", key + ": " + value, "---", ""}, lines...)
	}
	for i := 1; i < end-1; i++ {
		k, _, ok := strings.Cut(lines[i], ":")
		if ok && strings.TrimSpace(k) == key && !strings.HasPrefix(lines[i], " ") {
			out := append([]string{}, lines...)
			out[i] = key + ": " + value
			return out
		}
	}
	out := append([]string{}, lines[:end-1]...)
	out = append(out, key+": "+value)
	return append(out, lines[end-1:]...)
}

// LineDiff renders a minimal line diff (LCS-based) for a person reviewing a
// proposal: " " context, "-" removed, "+" added, with unchanged runs longer
// than a few lines collapsed.
func LineDiff(before, after string) string {
	a := strings.Split(strings.TrimRight(before, "\n"), "\n")
	b := strings.Split(strings.TrimRight(after, "\n"), "\n")
	if before == "" {
		a = nil
	}
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	type op struct {
		kind byte
		text string
	}
	var ops []op
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, op{' ', a[i]})
			i++
			j++
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, op{'-', a[i]})
			i++
		default:
			ops = append(ops, op{'+', b[j]})
			j++
		}
	}

	const ctx = 2
	nearChange := func(k int) bool {
		for d := -ctx; d <= ctx; d++ {
			if k+d >= 0 && k+d < len(ops) && ops[k+d].kind != ' ' {
				return true
			}
		}
		return false
	}
	var out strings.Builder
	hidden := false
	for k, o := range ops {
		if o.kind != ' ' || nearChange(k) {
			out.WriteString(string(o.kind) + " " + o.text + "\n")
			hidden = false
			continue
		}
		if !hidden {
			out.WriteString("  ...\n")
			hidden = true
		}
	}
	return out.String()
}

// FormatProposalPlan describes a proposal and the diff approving it makes —
// what a person reads before deciding, in the CLI and in the MCP prompt alike.
// root makes the doc path relative for display.
func FormatProposalPlan(plan *ProposalPlan, root string) string {
	var b strings.Builder
	p := plan.Proposal
	fmt.Fprintf(&b, "Proposal %s\n", p.Name)
	fmt.Fprintf(&b, "  %s %s in %s", p.Change, plan.RuleID, plan.DocName)
	if p.Rule != nil && p.Change != ProposeRetire {
		fmt.Fprintf(&b, " (%s)", p.Rule.Class)
	}
	b.WriteString("\n")
	if p.Author != "" || p.Date != "" {
		fmt.Fprintf(&b, "  drafted %s %s\n", p.Date, strings.TrimSpace("by "+p.Author))
	}
	if plan.Renumbered {
		fmt.Fprintf(&b, "  note: %s was taken after this was drafted, so it will be added as %s\n", p.RuleID, plan.RuleID)
	}
	if p.Evidence != "" {
		fmt.Fprintf(&b, "  evidence: %s\n", oneLine(p.Evidence, 200))
	}
	rel := plan.DocPath
	if r, err := filepath.Rel(root, plan.DocPath); err == nil {
		rel = r
	}
	verb := "Changes"
	if plan.Created {
		verb = "Creates"
	}
	fmt.Fprintf(&b, "\n%s %s:\n\n", verb, filepath.ToSlash(rel))
	b.WriteString(LineDiff(plan.Before, plan.After))
	return b.String()
}

// ApproverIdentity is who an approval is recorded against: the git identity
// configured for root, else the OS user.
func ApproverIdentity(root string) string {
	if out, err := gitOut(root, "config", "user.name"); err == nil {
		if name := strings.TrimSpace(out); name != "" {
			if email, err := gitOut(root, "config", "user.email"); err == nil && strings.TrimSpace(email) != "" {
				return name + " <" + strings.TrimSpace(email) + ">"
			}
			return name
		}
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return os.Getenv("USERNAME")
}
