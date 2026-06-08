package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NewRunbook is the payload for CreateRunbookDraft. Title and Steps are
// required; everything else is optional but Triggers are strongly encouraged
// (without them the runbook can't be found by symptom).
type NewRunbook struct {
	Title        string
	Triggers     []string
	Severity     string
	Owner        string
	RelatedPaths []string
	Steps        string // the procedure (markdown; required)
	Verification string
	Rollback     string
	Escalation   string
}

// CreateRunbookDraft writes a new runbook to runbooksDir/drafts/ for human
// review. Drafts are excluded from the loaded/retrievable set until promoted,
// so an agent-authored draft never silently becomes an authoritative procedure.
// The filename is <slug>-<shortid>.md so concurrent writers don't collide.
func CreateRunbookDraft(runbooksDir string, r NewRunbook) (string, error) {
	if strings.TrimSpace(r.Title) == "" {
		return "", fmt.Errorf("title is required")
	}
	if strings.TrimSpace(r.Steps) == "" {
		return "", fmt.Errorf("steps are required")
	}
	draftsDir := filepath.Join(runbooksDir, DraftsSubdir)
	if err := os.MkdirAll(draftsDir, 0755); err != nil {
		return "", fmt.Errorf("creating %s: %w", draftsDir, err)
	}

	id, err := shortID()
	if err != nil {
		return "", err
	}
	path := filepath.Join(draftsDir, fmt.Sprintf("%s-%s.md", slugify(r.Title), id))
	if err := os.WriteFile(path, []byte(renderRunbook(r)), 0644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

func renderRunbook(r NewRunbook) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", r.Title)
	if len(r.Triggers) > 0 {
		b.WriteString("triggers:\n")
		for _, t := range r.Triggers {
			fmt.Fprintf(&b, "  - %s\n", t)
		}
	}
	if r.Severity != "" {
		fmt.Fprintf(&b, "severity: %s\n", r.Severity)
	}
	if r.Owner != "" {
		fmt.Fprintf(&b, "owner: %s\n", r.Owner)
	}
	if len(r.RelatedPaths) > 0 {
		b.WriteString("related_paths:\n")
		for _, p := range r.RelatedPaths {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
	}
	b.WriteString("status: draft\n")
	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "# %s\n\n", r.Title)
	b.WriteString("> Draft runbook pending human review. Verify every step before relying on it.\n\n")
	b.WriteString("## Steps\n")
	b.WriteString(strings.TrimSpace(r.Steps))
	b.WriteString("\n")
	if s := strings.TrimSpace(r.Verification); s != "" {
		b.WriteString("\n## Verification\n")
		b.WriteString(s)
		b.WriteString("\n")
	}
	if s := strings.TrimSpace(r.Rollback); s != "" {
		b.WriteString("\n## Rollback\n")
		b.WriteString(s)
		b.WriteString("\n")
	}
	if s := strings.TrimSpace(r.Escalation); s != "" {
		b.WriteString("\n## Escalation\n")
		b.WriteString(s)
		b.WriteString("\n")
	}
	return b.String()
}

// PromoteRunbookDraft moves a draft from runbooks/drafts/ up to the runbooks
// root, making it an active runbook. It strips the `status: draft` line so the
// promoted file is clean. Returns the new path. This is the human-gated step.
func PromoteRunbookDraft(draftPath string) (string, error) {
	data, err := os.ReadFile(draftPath)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", draftPath, err)
	}

	// drafts/ lives directly under the runbooks dir; promote to its parent.
	draftsDir := filepath.Dir(draftPath)
	if filepath.Base(draftsDir) != DraftsSubdir {
		return "", fmt.Errorf("%s is not in a %s/ directory", draftPath, DraftsSubdir)
	}
	dest := filepath.Join(filepath.Dir(draftsDir), filepath.Base(draftPath))
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("%s already exists", dest)
	}

	cleaned := stripStatusDraft(string(data))
	if err := os.WriteFile(dest, []byte(cleaned), 0644); err != nil {
		return "", fmt.Errorf("writing %s: %w", dest, err)
	}
	if err := os.Remove(draftPath); err != nil {
		return "", fmt.Errorf("removing draft %s: %w", draftPath, err)
	}
	return dest, nil
}

// stripStatusDraft removes the `status: draft` line and the draft-warning
// blockquote, collapsing any blank lines left behind so the promoted file has
// no whitespace artifacts.
func stripStatusDraft(raw string) string {
	var out []string
	prevBlank := false
	for _, line := range strings.Split(raw, "\n") {
		t := strings.TrimSpace(line)
		if t == "status: draft" || strings.HasPrefix(t, "> Draft runbook pending") {
			continue
		}
		blank := t == ""
		if blank && prevBlank {
			continue // collapse runs of blank lines (e.g. where the blockquote was)
		}
		out = append(out, line)
		prevBlank = blank
	}
	return strings.Join(out, "\n")
}
