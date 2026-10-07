package context

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Proposal changes to intent. Agents never edit a ratified intent doc: a rule
// an agent wrote would have the same authority as one a person decided, and
// "the code does X, so X must be the rule" is exactly how a bug becomes
// policy. An agent that thinks a rule is wrong, missing, or obsolete files a
// proposal instead. Proposals live under .rivet/intent/proposals/, which
// LoadIntent never reads, and a person applies them by editing the doc.

// Proposal change kinds.
const (
	ProposeAdd    = "add"
	ProposeAmend  = "amend"
	ProposeRetire = "retire"
)

// NewIntentProposal is the payload for CreateIntentProposal.
type NewIntentProposal struct {
	Change    string // add | amend | retire
	Doc       string // intent doc the change targets, e.g. "intent/billing"
	RuleID    string // required for amend/retire; optional suggested ID for add
	Statement string // proposed rule text (required for add/amend)
	Why       string // the business reason (required)
	Evidence  string // what prompted it: code, tickets, conversation (optional)
	Author    string
}

// IntentProposal is a filed proposal, as listed for review.
type IntentProposal struct {
	Path   string `json:"path"`
	Title  string `json:"title"`
	Change string `json:"change"`
	Doc    string `json:"doc"`
	RuleID string `json:"rule_id,omitempty"`
	Date   string `json:"date"`
}

// CreateIntentProposal validates p and writes it to
// <intentDir>/proposals/<slug>-<id>.md. It never touches a ratified doc.
func CreateIntentProposal(intentDir string, p NewIntentProposal) (string, error) {
	p.Change = strings.ToLower(strings.TrimSpace(p.Change))
	switch p.Change {
	case ProposeAdd, ProposeAmend, ProposeRetire:
	default:
		return "", fmt.Errorf("change must be one of add, amend, retire (got %q)", p.Change)
	}
	if strings.TrimSpace(p.Doc) == "" {
		return "", fmt.Errorf("doc is required (the intent doc this change targets, e.g. intent/billing)")
	}
	if p.Change != ProposeAdd && strings.TrimSpace(p.RuleID) == "" {
		return "", fmt.Errorf("rule_id is required to %s a rule", p.Change)
	}
	if p.RuleID != "" && !IsRuleID(p.RuleID) {
		return "", fmt.Errorf("rule_id %q is not a rule ID (expected PREFIX-<number>, e.g. BIL-001)", p.RuleID)
	}
	if p.Change != ProposeRetire && strings.TrimSpace(p.Statement) == "" {
		return "", fmt.Errorf("statement is required to %s a rule", p.Change)
	}
	if strings.TrimSpace(p.Why) == "" {
		return "", fmt.Errorf("why is required — a rule change without its business reason can't be ratified")
	}
	if !strings.HasPrefix(p.Doc, "intent/") {
		p.Doc = "intent/" + p.Doc
	}

	dir := filepath.Join(intentDir, IntentProposalsSubdir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	id, err := shortID()
	if err != nil {
		return "", err
	}
	subject := p.RuleID
	if subject == "" {
		subject = strings.TrimPrefix(p.Doc, "intent/")
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s-%s.md", p.Change, slugify(subject), id))
	if err := os.WriteFile(path, []byte(renderIntentProposal(p)), 0644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

func renderIntentProposal(p NewIntentProposal) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "change: %s\n", p.Change)
	fmt.Fprintf(&b, "doc: %s\n", p.Doc)
	if p.RuleID != "" {
		fmt.Fprintf(&b, "rule: %s\n", p.RuleID)
	}
	fmt.Fprintf(&b, "date: %s\n", time.Now().Format("2006-01-02"))
	if p.Author != "" {
		fmt.Fprintf(&b, "author: %s\n", p.Author)
	}
	b.WriteString("status: proposed\n")
	b.WriteString("---\n\n")

	subject := p.RuleID
	if subject == "" {
		subject = "new rule"
	}
	fmt.Fprintf(&b, "# Proposal: %s %s in %s\n\n", p.Change, subject, p.Doc)
	b.WriteString("> Proposed by an agent. NOT ratified — this changes nothing until a person edits the intent doc.\n\n")
	if s := strings.TrimSpace(p.Statement); s != "" {
		b.WriteString("## Proposed rule\n\n")
		id := p.RuleID
		if id == "" {
			id = "NEW"
		}
		fmt.Fprintf(&b, "- **%s** %s\n", id, s)
		fmt.Fprintf(&b, "  why: %s\n\n", strings.TrimSpace(p.Why))
	} else {
		b.WriteString("## Why\n\n")
		b.WriteString(strings.TrimSpace(p.Why) + "\n\n")
	}
	if s := strings.TrimSpace(p.Evidence); s != "" {
		b.WriteString("## Evidence\n\n")
		b.WriteString(s + "\n")
	}
	return b.String()
}

// ListIntentProposals returns the proposals awaiting review, oldest first.
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
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		p := IntentProposal{Path: path}
		_, body := parseFrontmatter(string(data))
		p.Title = extractTitle(body, e.Name())
		for _, line := range frontmatterLines(string(data)) {
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
			case "date":
				p.Date = v
			}
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
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
