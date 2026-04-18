package context

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LearningEntry is a single capture-layer note from .rivet/learnings/.
// Learning entries are one file per entry so multiple authors can write
// concurrently without conflicting on a shared section.
type LearningEntry struct {
	Title        string    // from frontmatter: title (or first # heading)
	Date         time.Time // from frontmatter: date (YYYY-MM-DD)
	Author       string    // from frontmatter: author
	Confidence   string    // from frontmatter: confidence (low|medium|high)
	RelatedPaths []string  // from frontmatter: related_paths
	SuggestedDoc string    // from frontmatter: suggested_doc (optional promotion target)
	Promoted     bool      // from frontmatter: promoted (true once a context doc has absorbed it)
	PromotedTo   string    // from frontmatter: promoted_to (context doc name)
	Body         string    // markdown after frontmatter (Observation/Impact/Recommendation)
	RawBody      string    // full file content including frontmatter
	Path         string    // filesystem path
}

// LoadLearnings reads all *.md files under the given directory (non-recursive
// except it skips an "archive/" subdir). Missing directory returns no error.
func LoadLearnings(dir string) ([]*LearningEntry, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var out []*LearningEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		le, err := readLearning(path)
		if err != nil {
			return nil, err
		}
		out = append(out, le)
	}

	sort.Slice(out, func(i, j int) bool {
		if !out[i].Date.Equal(out[j].Date) {
			return out[i].Date.After(out[j].Date)
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func readLearning(path string) (*LearningEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	raw := string(data)
	fm, body := parseLearningFrontmatter(raw)

	le := &LearningEntry{
		Title:        fm.title,
		Author:       fm.author,
		Confidence:   fm.confidence,
		RelatedPaths: fm.relatedPaths,
		SuggestedDoc: fm.suggestedDoc,
		Promoted:     fm.promoted,
		PromotedTo:   fm.promotedTo,
		Body:         body,
		RawBody:      raw,
		Path:         path,
	}
	if fm.date != "" {
		if t, err := time.Parse("2006-01-02", fm.date); err == nil {
			le.Date = t
		}
	}
	if le.Title == "" {
		le.Title = extractTitle(body, strings.TrimSuffix(filepath.Base(path), ".md"))
	}
	return le, nil
}

type learningFrontmatter struct {
	title        string
	date         string
	author       string
	confidence   string
	suggestedDoc string
	promotedTo   string
	promoted     bool
	relatedPaths []string
}

// parseLearningFrontmatter parses a superset of the context doc frontmatter
// with scalar keys used for learnings.
func parseLearningFrontmatter(raw string) (learningFrontmatter, string) {
	var fm learningFrontmatter
	if !strings.HasPrefix(strings.TrimSpace(raw), "---") {
		return fm, raw
	}
	lines := strings.Split(raw, "\n")
	startIdx, endIdx := -1, -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
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
		if strings.HasPrefix(trimmed, "- ") {
			val := strings.Trim(strings.TrimPrefix(trimmed, "- "), "\"' ")
			if currentKey == "related_paths" {
				fm.relatedPaths = append(fm.relatedPaths, val)
			}
			continue
		}
		idx := strings.Index(trimmed, ":")
		if idx <= 0 {
			continue
		}
		currentKey = strings.TrimSpace(trimmed[:idx])
		val := strings.Trim(strings.TrimSpace(trimmed[idx+1:]), "\"'")
		switch currentKey {
		case "title":
			fm.title = val
		case "date":
			fm.date = val
		case "author":
			fm.author = val
		case "confidence":
			fm.confidence = val
		case "suggested_doc":
			fm.suggestedDoc = val
		case "promoted":
			fm.promoted = val == "true"
		case "promoted_to":
			fm.promotedTo = val
		case "related_paths":
			// Inline list: [a, b]
			if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
				inner := val[1 : len(val)-1]
				for _, item := range strings.Split(inner, ",") {
					item = strings.Trim(strings.TrimSpace(item), "\"'")
					if item != "" {
						fm.relatedPaths = append(fm.relatedPaths, item)
					}
				}
			}
		}
	}

	body := strings.TrimLeft(strings.Join(lines[endIdx+1:], "\n"), "\n")
	return fm, body
}

// NewLearning is the payload used by CreateLearning. Only Title and
// Observation are required.
type NewLearning struct {
	Title          string
	Author         string
	Confidence     string
	RelatedPaths   []string
	SuggestedDoc   string
	Observation    string
	Impact         string
	Recommendation string
	Date           time.Time // if zero, time.Now() is used
}

// CreateLearning writes a new learning entry to dir and returns the resulting
// LearningEntry. The filename is YYYY-MM-DD-<slug>-<shortid>.md — the short id
// guarantees concurrent writers don't collide.
func CreateLearning(dir string, l NewLearning) (*LearningEntry, error) {
	if strings.TrimSpace(l.Title) == "" {
		return nil, fmt.Errorf("title is required")
	}
	if strings.TrimSpace(l.Observation) == "" {
		return nil, fmt.Errorf("observation is required")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}

	when := l.Date
	if when.IsZero() {
		when = time.Now()
	}
	date := when.Format("2006-01-02")

	id, err := shortID()
	if err != nil {
		return nil, err
	}
	filename := fmt.Sprintf("%s-%s-%s.md", date, slugify(l.Title), id)
	path := filepath.Join(dir, filename)

	content := renderLearning(l, date)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("writing %s: %w", path, err)
	}

	return readLearning(path)
}

func renderLearning(l NewLearning, date string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", l.Title)
	fmt.Fprintf(&b, "date: %s\n", date)
	if l.Author != "" {
		fmt.Fprintf(&b, "author: %s\n", l.Author)
	}
	if l.Confidence != "" {
		fmt.Fprintf(&b, "confidence: %s\n", l.Confidence)
	}
	if l.SuggestedDoc != "" {
		fmt.Fprintf(&b, "suggested_doc: %s\n", l.SuggestedDoc)
	}
	if len(l.RelatedPaths) > 0 {
		b.WriteString("related_paths:\n")
		for _, p := range l.RelatedPaths {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
	}
	b.WriteString("promoted: false\n")
	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "# %s\n\n", l.Title)
	b.WriteString("## Observation\n")
	b.WriteString(strings.TrimSpace(l.Observation))
	b.WriteString("\n")

	if s := strings.TrimSpace(l.Impact); s != "" {
		b.WriteString("\n## Impact\n")
		b.WriteString(s)
		b.WriteString("\n")
	}
	if s := strings.TrimSpace(l.Recommendation); s != "" {
		b.WriteString("\n## Recommendation\n")
		b.WriteString(s)
		b.WriteString("\n")
	}
	return b.String()
}

// MarkPromoted rewrites the frontmatter of a learning file to set
// promoted: true and promoted_to: <docName>. It does not move the file;
// callers that want archival behavior can use ArchiveLearning.
func MarkPromoted(path, docName string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	raw := string(data)
	if !strings.HasPrefix(strings.TrimSpace(raw), "---") {
		return fmt.Errorf("%s has no frontmatter", path)
	}
	lines := strings.Split(raw, "\n")
	startIdx, endIdx := -1, -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			if startIdx == -1 {
				startIdx = i
			} else {
				endIdx = i
				break
			}
		}
	}
	if startIdx == -1 || endIdx == -1 {
		return fmt.Errorf("%s has malformed frontmatter", path)
	}

	// Rewrite frontmatter: update/insert promoted and promoted_to.
	var out []string
	out = append(out, lines[:startIdx+1]...)
	sawPromoted := false
	sawPromotedTo := false
	for _, line := range lines[startIdx+1 : endIdx] {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "promoted:"):
			out = append(out, "promoted: true")
			sawPromoted = true
		case strings.HasPrefix(trimmed, "promoted_to:"):
			if docName != "" {
				out = append(out, fmt.Sprintf("promoted_to: %s", docName))
			}
			sawPromotedTo = true
		default:
			out = append(out, line)
		}
	}
	if !sawPromoted {
		out = append(out, "promoted: true")
	}
	if !sawPromotedTo && docName != "" {
		out = append(out, fmt.Sprintf("promoted_to: %s", docName))
	}
	out = append(out, lines[endIdx:]...)

	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0644)
}

// ArchiveLearning moves the file into dir/archive/. Creates the archive dir if
// needed. Typically called after MarkPromoted to remove the entry from the
// active log.
func ArchiveLearning(path string) (string, error) {
	dir := filepath.Dir(path)
	archiveDir := filepath.Join(dir, "archive")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return "", fmt.Errorf("creating archive dir: %w", err)
	}
	dest := filepath.Join(archiveDir, filepath.Base(path))
	if err := os.Rename(path, dest); err != nil {
		return "", fmt.Errorf("archiving %s: %w", path, err)
	}
	return dest, nil
}

// CountActive returns the number of non-promoted learning files in dir.
func CountActive(dir string) int {
	entries, err := LoadLearnings(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.Promoted {
			n++
		}
	}
	return n
}

// slugify turns a title into a filesystem-safe kebab-case slug.
func slugify(s string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == ' ' || r == '-' || r == '_' || r == '/':
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "learning"
	}
	if len(slug) > 48 {
		slug = strings.TrimRight(slug[:48], "-")
	}
	return slug
}

func shortID() (string, error) {
	var buf [3]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generating id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}
