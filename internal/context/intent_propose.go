package context

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Agents can write rules, but only under a person's supervision. An agent
// never edits a ratified intent doc: a rule it wrote directly would carry the
// same authority as one the business decided, and "the code does X, so X must
// be the rule" is exactly how a bug becomes policy. Instead it drafts a
// complete, machine-applicable change — the rule text, its class, its why —
// as a proposal under .rivet/intent/proposals/, which LoadIntent never reads.
// A person reviews it (and may edit the file), then approves it with
// `rivet intent approve`, which applies it to the doc. See intent_apply.go.

// Proposal change kinds.
const (
	ProposeAdd    = "add"
	ProposeAmend  = "amend"
	ProposeRetire = "retire"
)

// Proposal statuses.
const (
	ProposalProposed = "proposed"
	ProposalApproved = "approved"
	ProposalRejected = "rejected"
)

// proposalArchiveSubdir holds decided proposals — the audit trail of who
// approved or rejected what. It is under proposals/, so never loaded.
const proposalArchiveSubdir = "archive"

// NewIntentProposal is the payload for CreateIntentProposal.
type NewIntentProposal struct {
	Change string // add | amend | retire
	Doc    string // intent doc the change targets, e.g. "intent/billing"; may be new for add
	// RuleID is required for amend/retire. For add it is the ID the rule will
	// get — the caller assigns the next free one (NextRuleID); approval
	// renumbers it if another rule took the ID in the meantime.
	RuleID      string
	Class       RuleClass // invariant | policy; for amend, empty keeps the current class
	Statement   string    // proposed rule text (required for add/amend)
	Why         string    // the business reason (required)
	Enforcement string    // optional `enforced:` value: "manual — <how>" or "pending"
	Scope       IntentScope
	Evidence    string // what prompted it: code, tickets, conversation (optional)
	Author      string
	// Current is the rule as it stands, for amend/retire. Its fingerprint is
	// recorded so approval can refuse a proposal written against a rule that
	// has since changed.
	Current *Rule
}

// IntentProposal is a filed proposal.
type IntentProposal struct {
	Path     string    `json:"path"`
	Name     string    `json:"name"` // file name without .md — what approve/reject take
	Title    string    `json:"title"`
	Change   string    `json:"change"`
	Doc      string    `json:"doc"`
	RuleID   string    `json:"rule_id,omitempty"`
	Class    RuleClass `json:"class,omitempty"`
	Scope    string    `json:"scope,omitempty"`
	Base     string    `json:"base,omitempty"` // fingerprint of the rule the proposal was written against
	Date     string    `json:"date"`
	Author   string    `json:"author,omitempty"`
	Status   string    `json:"status"`
	Rule     *Rule     `json:"proposed_rule,omitempty"` // add/amend: the rule as it would read
	Reason   string    `json:"reason,omitempty"`        // retire: why
	Evidence string    `json:"evidence,omitempty"`
}

// RuleFingerprint identifies a rule's content, for detecting that it changed
// between a proposal being written and being approved.
func RuleFingerprint(r Rule) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		string(r.Class), oneLine(r.Statement, 1<<20), oneLine(r.Why, 1<<20), r.Enforcement, oneLine(r.EnforcementNote, 1<<20),
	}, "\x00")))
	return hex.EncodeToString(sum[:6])
}

// DefaultRulePrefix derives a rule ID prefix from a doc name: the first three
// letters, uppercased (billing → BIL).
func DefaultRulePrefix(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (b.Len() > 0 && r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
		if b.Len() == 3 {
			break
		}
	}
	if b.Len() == 0 {
		return "RULE"
	}
	return b.String()
}

// docPrefix is the prefix new rules in a doc get: its declared prefix:, else
// the prefix its existing rules use, else one derived from its name.
func docPrefix(doc *Document, docName string) string {
	if doc != nil {
		if doc.Prefix != "" {
			return doc.Prefix
		}
		if len(doc.Rules) > 0 {
			p, _ := splitRuleID(doc.Rules[0].ID)
			return p
		}
	}
	return DefaultRulePrefix(strings.TrimPrefix(docName, "intent/"))
}

// NextRuleID returns the next unused ID for a doc: one past the highest
// number any intent doc uses with the doc's prefix — retired rules included,
// since their IDs are never reused — zero-padded like the existing IDs
// (three digits when there are none). doc may be nil for a doc that doesn't
// exist yet.
func NextRuleID(intents []*Document, doc *Document, docName string) string {
	prefix := docPrefix(doc, docName)
	maxN, width := 0, 3
	for _, d := range intents {
		for _, r := range d.Rules {
			p, n := splitRuleID(r.ID)
			if p != prefix {
				continue
			}
			if n > maxN {
				maxN = n
			}
			if w := len(r.ID) - len(p) - 1; w > width {
				width = w
			}
		}
	}
	return fmt.Sprintf("%s-%0*d", prefix, width, maxN+1)
}

// CreateIntentProposal validates p and writes it to
// <intentDir>/proposals/<change>-<subject>-<id>.md. It never touches a
// ratified doc.
func CreateIntentProposal(intentDir string, p NewIntentProposal) (string, error) {
	p.Change = strings.ToLower(strings.TrimSpace(p.Change))
	switch p.Change {
	case ProposeAdd, ProposeAmend, ProposeRetire:
	default:
		return "", fmt.Errorf("change must be one of add, amend, retire (got %q)", p.Change)
	}
	p.Doc = strings.TrimSpace(p.Doc)
	if p.Doc == "" {
		return "", fmt.Errorf("doc is required (the intent doc this change targets, e.g. intent/billing)")
	}
	if !strings.HasPrefix(p.Doc, "intent/") {
		p.Doc = "intent/" + p.Doc
	}
	if base := strings.TrimPrefix(p.Doc, "intent/"); base == "" || strings.ContainsAny(base, "/\\ ") {
		return "", fmt.Errorf("doc %q must be intent/<name> with a single-segment name", p.Doc)
	}
	if p.Change != ProposeAdd && strings.TrimSpace(p.RuleID) == "" {
		return "", fmt.Errorf("rule_id is required to %s a rule", p.Change)
	}
	if p.RuleID != "" && !IsRuleID(p.RuleID) {
		return "", fmt.Errorf("rule_id %q is not a rule ID (expected PREFIX-<number>, e.g. BIL-001)", p.RuleID)
	}
	if p.Change == ProposeAdd && p.RuleID == "" {
		return "", fmt.Errorf("rule_id is required: assign the next free ID with NextRuleID")
	}
	if p.Change != ProposeRetire && strings.TrimSpace(p.Statement) == "" {
		return "", fmt.Errorf("statement is required to %s a rule", p.Change)
	}
	if strings.TrimSpace(p.Why) == "" {
		return "", fmt.Errorf("why is required — a rule change without its business reason can't be ratified")
	}
	p.Class = RuleClass(strings.ToLower(strings.TrimSpace(string(p.Class))))
	switch {
	case p.Class == "" && p.Change == ProposeAdd:
		p.Class = RuleInvariant
	case p.Class == "" && p.Change == ProposeAmend && p.Current != nil && p.Current.Active():
		p.Class = p.Current.Class
	case p.Class == "" && p.Change == ProposeAmend:
		p.Class = RuleInvariant
	case p.Change == ProposeRetire:
		p.Class = RuleRetired
	case p.Class != RuleInvariant && p.Class != RulePolicy:
		return "", fmt.Errorf("class must be invariant or policy (got %q)", p.Class)
	}
	if p.Scope == "" {
		p.Scope = IntentScopeDomain
	}
	if p.Scope != IntentScopeDomain && p.Scope != IntentScopeCrossCutting {
		return "", fmt.Errorf("scope must be domain or cross-cutting (got %q)", p.Scope)
	}

	dir := filepath.Join(intentDir, IntentProposalsSubdir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	id, err := shortID()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s-%s.md", p.Change, slugify(p.RuleID), id))
	if err := os.WriteFile(path, []byte(renderIntentProposal(p, strings.TrimSuffix(filepath.Base(path), ".md"))), 0644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

// renderRuleBlock renders a rule in intent-doc form.
func renderRuleBlock(id string, class RuleClass, statement, why, enforcement string) []string {
	head := fmt.Sprintf("- **%s** %s", id, oneLine(statement, 1<<20))
	if class == RuleRetired {
		head = fmt.Sprintf("- ~~**%s**~~ %s", id, oneLine(statement, 1<<20))
	}
	lines := []string{head}
	if w := oneLine(why, 1<<20); w != "" {
		lines = append(lines, "  why: "+w)
	}
	if e := strings.TrimSpace(enforcement); e != "" {
		lines = append(lines, "  enforced: "+e)
	}
	return lines
}

// enforcementText renders a parsed enforcement back to its `enforced:` value.
func enforcementText(mode, note string) string {
	if mode == "" {
		return ""
	}
	if note != "" && (mode == EnforcementManual || mode == EnforcementPending) {
		return mode + " — " + note
	}
	return mode
}

func renderIntentProposal(p NewIntentProposal, name string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "change: %s\n", p.Change)
	fmt.Fprintf(&b, "doc: %s\n", p.Doc)
	fmt.Fprintf(&b, "rule: %s\n", p.RuleID)
	if p.Change != ProposeRetire {
		fmt.Fprintf(&b, "class: %s\n", p.Class)
	}
	if p.Change == ProposeAdd {
		fmt.Fprintf(&b, "scope: %s\n", p.Scope)
	}
	if p.Current != nil {
		fmt.Fprintf(&b, "base: %s\n", RuleFingerprint(*p.Current))
	}
	fmt.Fprintf(&b, "date: %s\n", time.Now().Format("2006-01-02"))
	if p.Author != "" {
		fmt.Fprintf(&b, "author: %s\n", oneLine(p.Author, 200))
	}
	fmt.Fprintf(&b, "status: %s\n", ProposalProposed)
	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "# Proposal: %s %s in %s\n\n", p.Change, p.RuleID, p.Doc)
	fmt.Fprintf(&b, "> Drafted by an agent. Nothing changes until a person reviews it (`rivet intent review %s`) and approves it in a terminal (`rivet intent approve %s`). Edit this file first if the wording needs work.\n\n", name, name)

	if p.Change == ProposeRetire {
		b.WriteString("## Reason\n\n")
		b.WriteString(strings.TrimSpace(p.Why) + "\n\n")
	} else {
		b.WriteString("## Proposed rule\n\n")
		for _, l := range renderRuleBlock(p.RuleID, p.Class, p.Statement, p.Why, p.Enforcement) {
			b.WriteString(l + "\n")
		}
		b.WriteString("\n")
	}
	if p.Current != nil {
		b.WriteString("## Current rule\n\n")
		for _, l := range renderRuleBlock(p.Current.ID, p.Current.Class, p.Current.Statement, p.Current.Why,
			enforcementText(p.Current.Enforcement, p.Current.EnforcementNote)) {
			b.WriteString(l + "\n")
		}
		b.WriteString("\n")
	}
	if s := strings.TrimSpace(p.Evidence); s != "" {
		b.WriteString("## Evidence\n\n")
		b.WriteString(s + "\n")
	}
	return b.String()
}

// LoadIntentProposal reads and parses one proposal file.
func LoadIntentProposal(path string) (*IntentProposal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw := string(data)
	p := &IntentProposal{Path: path, Name: strings.TrimSuffix(filepath.Base(path), ".md")}
	_, body := parseFrontmatter(raw)
	p.Title = extractTitle(body, p.Name)
	for _, line := range frontmatterLines(raw) {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "change":
			p.Change = v
		case "doc":
			p.Doc = v
		case "rule":
			p.RuleID = v
		case "class":
			p.Class = RuleClass(v)
		case "scope":
			p.Scope = v
		case "base":
			p.Base = v
		case "date":
			p.Date = v
		case "author":
			p.Author = v
		case "status":
			p.Status = v
		}
	}
	if p.Status == "" {
		p.Status = ProposalProposed
	}
	if sec := markdownSection(body, "Proposed rule"); sec != "" {
		// The section is parsed as an Invariants section; the class comes
		// from frontmatter.
		if rules := ParseRules(p.Doc, path, "## Invariants\n"+sec, 0); len(rules) > 0 {
			r := rules[0]
			if p.Class != "" {
				r.Class = p.Class
			}
			r.Line, r.EndLine = 0, 0
			p.Rule = &r
		}
	}
	p.Reason = strings.TrimSpace(markdownSection(body, "Reason"))
	p.Evidence = strings.TrimSpace(markdownSection(body, "Evidence"))
	return p, nil
}

// markdownSection returns the text under an H2 heading, up to the next H1/H2.
func markdownSection(body, heading string) string {
	var out []string
	in := false
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "## ") || strings.HasPrefix(t, "# ") {
			if in {
				break
			}
			in = strings.EqualFold(strings.TrimSpace(strings.TrimLeft(t, "#")), heading)
			continue
		}
		if in {
			out = append(out, line)
		}
	}
	return strings.Trim(strings.Join(out, "\n"), "\n")
}

// ListIntentProposals returns the proposals awaiting review, oldest first.
// Decided proposals live in proposals/archive/ and are not listed.
func ListIntentProposals(intentDir string) ([]IntentProposal, error) {
	dir := filepath.Join(intentDir, IntentProposalsSubdir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []IntentProposal
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		p, err := LoadIntentProposal(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// ProposalWarnings reports each proposal still waiting on a person, so a
// branch carrying an unreviewed agent-drafted rule is visible in lint (and
// fails it under --strict).
func ProposalWarnings(intentDir string) []LintWarning {
	props, _ := ListIntentProposals(intentDir)
	var ws []LintWarning
	for _, p := range props {
		ws = append(ws, LintWarning{
			Document: p.Name, Kind: KindIntent, Path: p.Path, Severity: SeverityWarning, Rule: "open-proposal",
			Message: fmt.Sprintf("agent-drafted proposal to %s %s in %s awaits a person — rivet intent review %s, then approve or reject it",
				p.Change, p.RuleID, p.Doc, p.Name),
		})
	}
	return ws
}

// frontmatterLines returns the raw lines between a file's opening and closing
// "---" fences, or nil when it has no frontmatter.
func frontmatterLines(raw string) []string {
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return lines[1:i]
		}
	}
	return nil
}
