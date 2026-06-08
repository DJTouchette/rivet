package context

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Kind represents the category of a context document.
type Kind string

const (
	KindDomain   Kind = "domain"
	KindModule   Kind = "module"
	KindParadigm Kind = "paradigm"
	KindWiki     Kind = "wiki"    // free-form reference / narrative docs
	KindRunbook  Kind = "runbook" // actionable, trigger-keyed procedures
)

// IsContextKind reports whether a kind is one of the curated, code-adjacent
// context kinds (as opposed to wiki/runbook reference tiers).
func (k Kind) IsContextKind() bool {
	return k == KindDomain || k == KindModule || k == KindParadigm
}

// Document is a loaded context document from .rivet/context/, .rivet/wiki/, or
// .rivet/runbooks/.
type Document struct {
	Name         string    // filename without extension, or slash-rel path for nested wiki/runbooks
	Kind         Kind      // domain, module, paradigm, wiki, or runbook
	Title        string    // first # heading, or Name if none found
	Tags         []string  // from frontmatter: tags
	RelatedPaths []string  // from frontmatter: related_paths (glob patterns)
	Owner        string    // from frontmatter: owner (person/team responsible)
	LastReviewed time.Time // from frontmatter: last_reviewed (YYYY-MM-DD)
	Body         string    // markdown content (after frontmatter)
	RawBody      string    // full file content including frontmatter
	Path         string    // filesystem path

	// Runbook-specific frontmatter (empty/zero for other kinds).
	Triggers   []string  // symptoms/alerts that invoke this runbook (retrieval keys)
	Severity   string    // low | medium | high | critical
	LastTested time.Time // from frontmatter: last_tested (YYYY-MM-DD)
}

// URI returns the MCP resource URI for this document. Wiki and runbook docs get
// their own schemes; the curated context kinds keep their established URIs.
func (d *Document) URI() string {
	switch d.Kind {
	case KindWiki:
		return "rivet://wiki/" + d.Name
	case KindRunbook:
		return "rivet://runbook/" + d.Name
	default:
		return fmt.Sprintf("rivet://context/%ss/%s", d.Kind, d.Name)
	}
}

// EmbeddingText is the text embedded for semantic matching: the title (it
// carries the most signal per token) followed by the body. Tags and runbook
// triggers are included so a query that matches them conceptually — not just
// lexically — still lands (e.g. a symptom query finding a runbook by trigger).
func (d *Document) EmbeddingText() string {
	var b strings.Builder
	b.WriteString(d.Title)
	if len(d.Triggers) > 0 {
		b.WriteString("\n")
		b.WriteString(strings.Join(d.Triggers, " "))
	}
	if len(d.Tags) > 0 {
		b.WriteString("\n")
		b.WriteString(strings.Join(d.Tags, " "))
	}
	if d.Body != "" {
		b.WriteString("\n")
		b.WriteString(d.Body)
	}
	return b.String()
}

// Load reads all context documents from the given base directory.
// The base directory should contain domains/, modules/, and/or paradigms/ subdirectories.
// Missing subdirectories are silently skipped.
func Load(baseDir string) ([]*Document, error) {
	var docs []*Document

	kindDirs := []struct {
		dir  string
		kind Kind
	}{
		{"domains", KindDomain},
		{"modules", KindModule},
		{"paradigms", KindParadigm},
	}

	for _, kd := range kindDirs {
		dir := filepath.Join(baseDir, kd.dir)
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}

			path := filepath.Join(dir, entry.Name())
			body, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", path, err)
			}

			name := strings.TrimSuffix(entry.Name(), ".md")
			raw := string(body)
			fm, content := parseFrontmatter(raw)
			title := extractTitle(content, name)

			doc := &Document{
				Name:         name,
				Kind:         kd.kind,
				Title:        title,
				Tags:         fm.tags,
				RelatedPaths: fm.relatedPaths,
				Owner:        fm.owner,
				Body:         content,
				RawBody:      raw,
				Path:         path,
			}
			if fm.lastReviewed != "" {
				if t, err := time.Parse("2006-01-02", fm.lastReviewed); err == nil {
					doc.LastReviewed = t
				}
			}
			docs = append(docs, doc)
		}
	}

	sort.Slice(docs, func(i, j int) bool {
		if docs[i].Kind != docs[j].Kind {
			return docs[i].Kind < docs[j].Kind
		}
		return docs[i].Name < docs[j].Name
	})

	return docs, nil
}

// extractTitle finds the first # heading in the markdown, or returns fallback.
func extractTitle(body, fallback string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	return fallback
}

// frontmatter holds the parsed fields from a context doc's YAML frontmatter.
type frontmatter struct {
	tags         []string
	relatedPaths []string
	owner        string
	lastReviewed string
	triggers     []string // runbook: symptoms/alerts
	severity     string   // runbook: low|medium|high|critical
	lastTested   string   // runbook: YYYY-MM-DD
}

// parseFrontmatter extracts YAML frontmatter from markdown content.
// Returns the parsed fields and the body after frontmatter.
// If no frontmatter is present, returns a zero frontmatter and the original body.
func parseFrontmatter(raw string) (frontmatter, string) {
	var fm frontmatter
	if !strings.HasPrefix(strings.TrimSpace(raw), "---") {
		return fm, raw
	}

	lines := strings.Split(raw, "\n")
	startIdx := -1
	endIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if startIdx == -1 {
				startIdx = i
			} else {
				endIdx = i
				break
			}
		}
	}

	if startIdx == -1 || endIdx == -1 {
		return fm, raw
	}

	var currentKey string
	for _, line := range lines[startIdx+1 : endIdx] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// List item: "  - value"
		if strings.HasPrefix(trimmed, "- ") {
			val := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")), "\"'")
			switch currentKey {
			case "tags":
				fm.tags = append(fm.tags, val)
			case "related_paths":
				fm.relatedPaths = append(fm.relatedPaths, val)
			case "triggers":
				fm.triggers = append(fm.triggers, val)
			}
			continue
		}

		idx := strings.Index(trimmed, ":")
		if idx <= 0 {
			continue
		}
		currentKey = strings.TrimSpace(trimmed[:idx])
		valPart := strings.TrimSpace(trimmed[idx+1:])

		// Inline list: tags: [billing, invoice, payment]
		if strings.HasPrefix(valPart, "[") && strings.HasSuffix(valPart, "]") {
			inner := valPart[1 : len(valPart)-1]
			for _, item := range strings.Split(inner, ",") {
				item = strings.Trim(strings.TrimSpace(item), "\"'")
				if item == "" {
					continue
				}
				switch currentKey {
				case "tags":
					fm.tags = append(fm.tags, item)
				case "related_paths":
					fm.relatedPaths = append(fm.relatedPaths, item)
				case "triggers":
					fm.triggers = append(fm.triggers, item)
				}
			}
			continue
		}

		if valPart == "" {
			continue
		}
		valPart = strings.Trim(valPart, "\"'")
		switch currentKey {
		case "tags":
			fm.tags = append(fm.tags, valPart)
		case "related_paths":
			fm.relatedPaths = append(fm.relatedPaths, valPart)
		case "triggers":
			fm.triggers = append(fm.triggers, valPart)
		case "owner":
			fm.owner = valPart
		case "last_reviewed":
			fm.lastReviewed = valPart
		case "severity":
			fm.severity = valPart
		case "last_tested":
			fm.lastTested = valPart
		}
	}

	body := strings.TrimLeft(strings.Join(lines[endIdx+1:], "\n"), "\n")
	return fm, body
}
