// Package queryextract finds SQL queries in application source code and
// turns them into types.QueryRef values that downstream analyzers can
// match against live schema.
//
// Extraction is regex-based — not a proper AST parse — so it catches the
// common cases reliably and misses the weird ones silently. We prefer false
// negatives (a query we didn't see) to false positives (a random string
// matched as SQL).
package queryextract

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/djtouchette/rivet/internal/schema/types"
)

// Options tunes the walk and dispatch.
type Options struct {
	Roots     []string
	Include   []string // glob patterns
	Exclude   []string // glob patterns
	Languages []string // if set, only these extractors run (csharp|go|python|node)
	MaxBytes  int64    // skip files larger than this (default 1 MiB)
}

// Extractor inspects a single file and returns the queries it found.
type Extractor interface {
	Lang() string
	// Match reports whether this extractor should handle the file.
	Match(path string) bool
	// Extract reads content and returns the queries found.
	Extract(path string, content string) []types.QueryRef
}

// Registry of available extractors. Populated by init() in per-language files.
var extractors []Extractor

func register(e Extractor) { extractors = append(extractors, e) }

// All returns every registered extractor (read-only).
func All() []Extractor { return extractors }

// Scan walks the configured roots and extracts queries from every matching file.
func Scan(opts Options) ([]types.QueryRef, error) {
	if opts.MaxBytes == 0 {
		opts.MaxBytes = 1 << 20 // 1 MiB
	}
	if len(opts.Roots) == 0 {
		opts.Roots = []string{"."}
	}

	wantLang := make(map[string]bool)
	for _, l := range opts.Languages {
		wantLang[l] = true
	}

	var out []types.QueryRef

	for _, root := range opts.Roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable entries
			}
			if d.IsDir() {
				if skipDir(d.Name()) {
					return fs.SkipDir
				}
				return nil
			}
			if matchesAny(path, opts.Exclude) {
				return nil
			}
			if len(opts.Include) > 0 && !matchesAny(path, opts.Include) {
				// fall through if no extractor matches either
			}

			info, err := d.Info()
			if err != nil {
				return nil
			}
			if info.Size() > opts.MaxBytes {
				return nil
			}

			for _, ext := range extractors {
				if len(wantLang) > 0 && !wantLang[ext.Lang()] {
					continue
				}
				if !ext.Match(path) {
					continue
				}
				content, err := readFile(path)
				if err != nil {
					return nil
				}
				refs := ext.Extract(path, content)
				out = append(out, refs...)
				break
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// matchesAny reports whether path matches any glob pattern.
func matchesAny(path string, patterns []string) bool {
	for _, pat := range patterns {
		// filepath.Match doesn't support **/; do a cheap substring fallback.
		if strings.Contains(pat, "**") {
			stripped := strings.ReplaceAll(pat, "**/", "")
			if ok, _ := filepath.Match(stripped, filepath.Base(path)); ok {
				return true
			}
			if strings.Contains(path, strings.ReplaceAll(pat, "**", "")) {
				return true
			}
		} else if ok, _ := filepath.Match(pat, filepath.Base(path)); ok {
			return true
		}
	}
	return false
}

// skipDir is a cheap heuristic to avoid expensive vendor/build trees.
func skipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "bin", "obj",
		".venv", "venv", "__pycache__", "dist", "build",
		".next", ".nuxt", ".svelte-kit", "target":
		return true
	}
	return strings.HasPrefix(name, ".")
}

// lineOf returns the 1-based line number in content at which offset `off` lives.
func lineOf(content string, off int) int {
	if off > len(content) {
		off = len(content)
	}
	line := 1
	for i := 0; i < off; i++ {
		if content[i] == '\n' {
			line++
		}
	}
	return line
}

// lineScanner yields (lineNum, text) pairs. Used when an extractor prefers
// a line-oriented pass to a multiline regex.
type lineScanner struct {
	*bufio.Scanner
	line int
}

func newLineScanner(s string) *lineScanner {
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	return &lineScanner{Scanner: sc}
}

func (l *lineScanner) Next() (int, string, bool) {
	if !l.Scan() {
		return 0, "", false
	}
	l.line++
	return l.line, l.Text(), true
}
