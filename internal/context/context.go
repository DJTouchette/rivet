package context

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Kind represents the category of a context document.
type Kind string

const (
	KindDomain   Kind = "domain"
	KindModule   Kind = "module"
	KindParadigm Kind = "paradigm"
)

// Document is a loaded context document from .rivet/context/.
type Document struct {
	Name         string   // filename without extension (e.g. "billing")
	Kind         Kind     // domain, module, or paradigm
	Title        string   // first # heading, or Name if none found
	Tags         []string // from frontmatter: tags
	RelatedPaths []string // from frontmatter: related_paths (glob patterns)
	Body         string   // markdown content (after frontmatter)
	RawBody      string   // full file content including frontmatter
	Path         string   // filesystem path
}

// URI returns the MCP resource URI for this document.
func (d *Document) URI() string {
	return fmt.Sprintf("rivet://context/%ss/%s", d.Kind, d.Name)
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
			tags, relatedPaths, content := parseFrontmatter(raw)
			title := extractTitle(content, name)

			docs = append(docs, &Document{
				Name:         name,
				Kind:         kd.kind,
				Title:        title,
				Tags:         tags,
				RelatedPaths: relatedPaths,
				Body:         content,
				RawBody:      raw,
				Path:         path,
			})
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

// parseFrontmatter extracts YAML frontmatter from markdown content.
// Returns tags, related_paths, and the body after frontmatter.
// If no frontmatter is present, returns nil slices and the original body.
func parseFrontmatter(raw string) (tags []string, relatedPaths []string, body string) {
	if !strings.HasPrefix(strings.TrimSpace(raw), "---") {
		return nil, nil, raw
	}

	// Find the closing ---
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
		return nil, nil, raw
	}

	// Parse frontmatter lines (simple key: value and list items)
	var currentKey string
	for _, line := range lines[startIdx+1 : endIdx] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// List item: "  - value"
		if strings.HasPrefix(trimmed, "- ") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			val = strings.Trim(val, "\"'")
			switch currentKey {
			case "tags":
				tags = append(tags, val)
			case "related_paths":
				relatedPaths = append(relatedPaths, val)
			}
			continue
		}

		// Key: value or Key: [inline list]
		if idx := strings.Index(trimmed, ":"); idx > 0 {
			currentKey = strings.TrimSpace(trimmed[:idx])
			valPart := strings.TrimSpace(trimmed[idx+1:])

			// Inline list: tags: [billing, invoice, payment]
			if strings.HasPrefix(valPart, "[") && strings.HasSuffix(valPart, "]") {
				inner := valPart[1 : len(valPart)-1]
				for _, item := range strings.Split(inner, ",") {
					item = strings.TrimSpace(item)
					item = strings.Trim(item, "\"'")
					if item == "" {
						continue
					}
					switch currentKey {
					case "tags":
						tags = append(tags, item)
					case "related_paths":
						relatedPaths = append(relatedPaths, item)
					}
				}
				continue
			}

			// Single value on same line (not a list)
			if valPart != "" {
				valPart = strings.Trim(valPart, "\"'")
				switch currentKey {
				case "tags":
					tags = append(tags, valPart)
				case "related_paths":
					relatedPaths = append(relatedPaths, valPart)
				}
			}
		}
	}

	// Body is everything after the closing ---
	body = strings.Join(lines[endIdx+1:], "\n")
	body = strings.TrimLeft(body, "\n")

	return tags, relatedPaths, body
}
