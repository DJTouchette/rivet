package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StaleReviewDays is the number of days after which an unreviewed context doc
// is flagged as stale.
const StaleReviewDays = 90

// Severity indicates how serious a lint warning is.
type Severity string

const (
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// LintWarning is a single issue found in a context document.
type LintWarning struct {
	Document string   `json:"document"`
	Kind     Kind     `json:"kind"`
	Path     string   `json:"path"`
	Severity Severity `json:"severity"`
	Rule     string   `json:"rule"`
	Message  string   `json:"message"`
}

// LintResult holds all warnings for a set of context documents.
type LintResult struct {
	Warnings []LintWarning `json:"warnings"`
	DocsRead int           `json:"docs_read"`
}

// Lint validates context documents and returns any issues found.
// projectRoot is the project root directory used for resolving related_paths.
func Lint(docs []*Document, projectRoot string) LintResult {
	result := LintResult{DocsRead: len(docs)}

	for _, doc := range docs {
		result.Warnings = append(result.Warnings, lintDoc(doc, projectRoot)...)
	}

	return result
}

// StaleTestDays is the number of days after which a runbook's last_tested is
// flagged. Untested runbooks are dangerous to follow under pressure.
const StaleTestDays = 180

func lintDoc(doc *Document, projectRoot string) []LintWarning {
	var warnings []LintWarning
	add := func(sev Severity, rule, msg string) {
		warnings = append(warnings, LintWarning{
			Document: doc.Name,
			Kind:     doc.Kind,
			Path:     doc.Path,
			Severity: sev,
			Rule:     rule,
			Message:  msg,
		})
	}

	switch doc.Kind {
	case KindRunbook:
		// Runbooks are found by symptom and followed under pressure, so the
		// rules differ: triggers are essential, and untested is dangerous.
		if len(doc.Triggers) == 0 {
			add(SeverityWarning, "missing-triggers", "no triggers in frontmatter — add symptoms/alerts so this runbook can be found when it's needed")
		}
		if strings.TrimSpace(doc.Owner) == "" {
			add(SeverityWarning, "missing-owner", "no owner in frontmatter — add the team responsible for this procedure")
		}
		if doc.LastTested.IsZero() {
			add(SeverityWarning, "untested-runbook", "no last_tested date — a runbook that's never been verified is risky to follow")
		} else if age := int(time.Since(doc.LastTested).Hours() / 24); age > StaleTestDays {
			add(SeverityWarning, "stale-test",
				fmt.Sprintf("last tested %d days ago (threshold: %d) — re-test and update last_tested", age, StaleTestDays))
		}
	case KindWiki:
		// Wiki is free-form (often imported); only flag genuinely broken content.
	default:
		// Curated context kinds.
		if len(doc.Tags) == 0 {
			add(SeverityWarning, "missing-tags", "no tags in frontmatter — add tags for better recommendation matching")
		}
		if len(doc.RelatedPaths) == 0 {
			add(SeverityWarning, "missing-related-paths", "no related_paths in frontmatter — add glob patterns to link this doc to source files")
		}
		if strings.TrimSpace(doc.Owner) == "" {
			add(SeverityWarning, "missing-owner", "no owner in frontmatter — add an owner responsible for keeping this doc accurate")
		}
		if doc.LastReviewed.IsZero() {
			add(SeverityWarning, "stale-review", "no last_reviewed date in frontmatter — add one so staleness can be tracked")
		} else if age := int(time.Since(doc.LastReviewed).Hours() / 24); age > StaleReviewDays {
			add(SeverityWarning, "stale-review",
				fmt.Sprintf("last reviewed %d days ago (threshold: %d) — re-review and update last_reviewed", age, StaleReviewDays))
		}
	}

	// Rules below apply to every kind.

	// Rule: placeholder-section — unfilled HTML comment placeholders.
	placeholders := countPlaceholders(doc.Body)
	if placeholders > 0 {
		add(SeverityWarning, "placeholder-section",
			fmt.Sprintf("%d unfilled placeholder section(s) — replace <!-- ... --> comments with real content", placeholders))
	}

	// Rule: empty-body — body has no meaningful content.
	stripped := stripFrontmatterAndHeadings(doc.Body)
	if strings.TrimSpace(stripped) == "" {
		add(SeverityError, "empty-body", "document has no content beyond headings")
	}

	// Rule: stale-related-path — related_paths that match nothing on disk.
	for _, pattern := range doc.RelatedPaths {
		if !globMatchesAnything(pattern, projectRoot) {
			add(SeverityWarning, "stale-related-path",
				fmt.Sprintf("related_path %q matches no files on disk", pattern))
		}
	}

	// Rule: stale-reference — backtick-quoted paths in body that don't exist.
	staleRefs := findStaleReferences(doc.Body, projectRoot)
	for _, ref := range staleRefs {
		add(SeverityWarning, "stale-reference",
			fmt.Sprintf("referenced path `%s` not found on disk", ref))
	}

	return warnings
}

// countPlaceholders counts HTML comment placeholders like <!-- ... -->.
func countPlaceholders(body string) int {
	count := 0
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "<!--") && strings.HasSuffix(trimmed, "-->") {
			count++
		}
	}
	return count
}

// stripFrontmatterAndHeadings removes markdown headings and frontmatter,
// leaving only body content for emptiness checks.
func stripFrontmatterAndHeadings(body string) string {
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "<!--") && strings.HasSuffix(trimmed, "-->") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// globMatchesAnything checks if a glob pattern matches at least one file.
func globMatchesAnything(pattern, root string) bool {
	// Handle ** patterns by checking if the prefix directory exists.
	if strings.Contains(pattern, "**") {
		prefix := strings.SplitN(pattern, "**", 2)[0]
		prefix = strings.TrimRight(prefix, "/")
		if prefix == "" {
			return true // "**" matches everything
		}
		dir := filepath.Join(root, prefix)
		info, err := os.Stat(dir)
		if err != nil {
			return false
		}
		return info.IsDir()
	}

	// For simple globs, use filepath.Glob.
	full := filepath.Join(root, pattern)
	matches, err := filepath.Glob(full)
	if err != nil {
		return false
	}
	return len(matches) > 0
}

// findStaleReferences extracts backtick-quoted paths from body text
// and returns those that don't exist on disk.
func findStaleReferences(body, root string) []string {
	var stale []string
	seen := make(map[string]bool)

	for _, line := range strings.Split(body, "\n") {
		refs := extractBacktickPaths(line)
		for _, ref := range refs {
			if seen[ref] {
				continue
			}
			seen[ref] = true

			full := filepath.Join(root, ref)
			if _, err := os.Stat(full); os.IsNotExist(err) {
				// Also check without trailing / for directory refs.
				clean := strings.TrimSuffix(ref, "/")
				fullClean := filepath.Join(root, clean)
				if _, err := os.Stat(fullClean); os.IsNotExist(err) {
					stale = append(stale, ref)
				}
			}
		}
	}
	return stale
}

// extractBacktickPaths finds backtick-quoted strings that look like file paths.
// Matches patterns like `lib/app/foo.ex`, `src/handlers/`, `internal/pkg/bar.go`.
func extractBacktickPaths(line string) []string {
	var paths []string

	parts := strings.Split(line, "`")
	// Backtick content is at odd indices: text`content`text`content`text
	for i := 1; i < len(parts); i += 2 {
		candidate := strings.TrimSpace(parts[i])
		if looksLikeFilePath(candidate) {
			paths = append(paths, candidate)
		}
	}
	return paths
}

// looksLikeFilePath returns true if s looks like a relative file or directory path
// anchored from the project root (starts with a known source directory).
func looksLikeFilePath(s string) bool {
	if s == "" || len(s) < 3 {
		return false
	}
	// Must contain a slash.
	if !strings.Contains(s, "/") {
		return false
	}
	// Must not contain spaces.
	if strings.Contains(s, " ") {
		return false
	}
	// Must not start with - (flags), / (routes), or : (atoms/params).
	if s[0] == '-' || s[0] == '/' || s[0] == ':' {
		return false
	}
	// Must not be a URL.
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return false
	}
	// Must not contain parens, equals, braces, or template patterns.
	if strings.ContainsAny(s, "()={}") {
		return false
	}
	// Must not contain uppercase (filters module refs like Module.func/1, Billing.can_*?/1).
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			return false
		}
	}
	// Must not end with arity notation (func/2, handler/3).
	lastSlash := strings.LastIndex(s, "/")
	if lastSlash >= 0 {
		after := s[lastSlash+1:]
		if isDigits(after) {
			return false
		}
		// Also catch func_name/2 patterns where the part before / has no dots or slashes.
		if isDigits(after) || (len(after) <= 2 && isDigits(after)) {
			return false
		}
	}
	// Must start with a known source directory prefix to avoid matching
	// domain-relative paths like "quotes/quote.ex" that aren't rooted.
	knownPrefixes := []string{
		"lib/", "src/", "app/", "pkg/", "internal/", "cmd/",
		"test/", "tests/", "spec/", "priv/", "config/", "bin/",
		"assets/", "static/", "public/", "web/", "backend/", "frontend/",
	}
	for _, prefix := range knownPrefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// isDigits returns true if s is non-empty and contains only digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
