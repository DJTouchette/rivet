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

// HasErrors reports whether any warning is error severity. Callers use this to
// decide an exit code — errors mean a doc is broken, not merely untidy.
func (r LintResult) HasErrors() bool {
	for _, w := range r.Warnings {
		if w.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Lint validates context documents and returns any issues found.
// projectRoot is the project root directory used for resolving related_paths.
func Lint(docs []*Document, projectRoot string) LintResult {
	result := LintResult{DocsRead: len(docs)}

	// Link validation and collision detection are corpus-wide, so the known
	// names are collected before any single doc is checked.
	names := make(map[string]int, len(docs))
	for _, doc := range docs {
		names[doc.Name]++
	}

	// Untagged themes need corpus-wide term frequencies to tell a distinctive
	// subject from a word everyone uses.
	docFreq := bodyTermDocFreq(docs)

	reportedDuplicate := make(map[string]bool)
	for _, doc := range docs {
		result.Warnings = append(result.Warnings, lintDoc(doc, projectRoot, names)...)

		// Rule: untagged-theme — a subject the body dwells on that the tags
		// never mention, so retrieval can't find the doc by it.
		for _, theme := range untaggedThemes(doc, docFreq, len(docs)) {
			result.Warnings = append(result.Warnings, LintWarning{
				Document: doc.Name,
				Kind:     doc.Kind,
				Path:     doc.Path,
				Severity: SeverityWarning,
				Rule:     "untagged-theme",
				Message: fmt.Sprintf("body mentions %q %d times but no tag covers it — add it to tags, or retrieval won't find this doc by that term",
					theme.term, theme.count),
			})
		}

		// A duplicate is one problem, not one per copy, so it's reported once
		// against the first doc carrying the name.
		if names[doc.Name] > 1 && !reportedDuplicate[doc.Name] {
			reportedDuplicate[doc.Name] = true
			result.Warnings = append(result.Warnings, LintWarning{
				Document: doc.Name,
				Kind:     doc.Kind,
				Path:     doc.Path,
				Severity: SeverityError,
				Rule:     "duplicate-name",
				Message: fmt.Sprintf("%d documents share the name %q — 'context show' and [[links]] resolve to only one of them, and which one is not defined",
					names[doc.Name], doc.Name),
			})
		}
	}

	return result
}

// StaleTestDays is the number of days after which a runbook's last_tested is
// flagged. Untested runbooks are dangerous to follow under pressure.
const StaleTestDays = 180

func lintDoc(doc *Document, projectRoot string, knownNames map[string]int) []LintWarning {
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
	case KindCode:
		// Code-extracted docs come from `rivet:context` comments and .context/
		// sidecars. There is nowhere to put an owner or a last_reviewed date in a
		// code comment, so demanding frontmatter here would only generate noise
		// nobody can act on. Content rules below still apply.
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
		// Distinct rules for absent vs old, mirroring how runbooks separate
		// untested-runbook from stale-test. One rule name for both conditions
		// made "show me the docs that have actually gone stale" unanswerable.
		if doc.LastReviewed.IsZero() {
			add(SeverityWarning, "missing-review", "no last_reviewed date in frontmatter — add one so staleness can be tracked")
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
	stripped := stripHeadingsAndPlaceholders(doc.Body)
	if strings.TrimSpace(stripped) == "" {
		add(SeverityError, "empty-body", "document has no content beyond headings")
	}

	// Rule: broken-wikilink — [[links]] pointing at a doc that doesn't exist.
	// Nothing renders these, so a typo is invisible without this check.
	//
	// A nil knownNames means the caller is linting one document without knowing
	// the rest of the corpus. Reporting every link as broken would be worse than
	// saying nothing, so link checking is skipped entirely.
	if knownNames != nil {
		for _, link := range WikiLinks(doc.Body) {
			if link.Target == doc.Name {
				add(SeverityWarning, "self-wikilink",
					fmt.Sprintf("[[%s]] links to this document — remove it", link.Target))
				continue
			}
			if knownNames[link.Target] == 0 {
				add(SeverityWarning, "broken-wikilink",
					fmt.Sprintf("[[%s]] does not match any known document — check the name or write the doc", link.Target))
			}
		}
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

// stripHeadingsAndPlaceholders removes markdown headings and unfilled
// placeholder comments, leaving only real body content for emptiness checks.
// Document.Body already excludes frontmatter.
func stripHeadingsAndPlaceholders(body string) string {
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
		refs := extractBacktickPaths(line, root)
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
func extractBacktickPaths(line, root string) []string {
	var paths []string

	parts := strings.Split(line, "`")
	// Backtick content is at odd indices: text`content`text`content`text
	for i := 1; i < len(parts); i += 2 {
		candidate := strings.TrimSpace(parts[i])
		if looksLikeFilePath(candidate, root) {
			paths = append(paths, candidate)
		}
	}
	return paths
}

// looksLikeFilePath returns true if s looks like a relative file or directory
// path anchored from the project root.
//
// The bar is deliberately high. Backticks in prose hold far more than paths —
// function names, flags, YAML keys, module references — and a false positive
// here produces a "stale reference" warning for something that was never a
// path, which is worse than missing a genuinely dead one.
func looksLikeFilePath(s, root string) bool {
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
	// Globs and placeholders are documentation conventions, not paths, and
	// resolving them literally invents rot that isn't there: real docs write
	// `database/migrations/*.sql` and `environment/.env.<env>` to describe a
	// family of files. Checking whether the containing directory exists would be
	// a different, weaker rule; this check is about a specific file having moved.
	if strings.ContainsAny(s, "*?<>[]") {
		return false
	}
	// Must not contain uppercase (filters module refs like Module.func/1, Billing.can_*?/1).
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			return false
		}
	}
	// Must not end with arity notation (func/2, handler/3) — Elixir and Erlang
	// docs are full of these and they are not paths.
	if lastSlash := strings.LastIndex(s, "/"); lastSlash >= 0 {
		if isDigits(s[lastSlash+1:]) {
			return false
		}
	}

	// Must be rooted, so domain-relative prose like "quotes/quote.ex" doesn't
	// get treated as a path from the project root. Two ways to qualify:
	// a conventional source directory, or — for any layout the list doesn't
	// anticipate, like services/ or packages/ — a first segment that really is
	// a directory in this project.
	for _, prefix := range conventionalSourceDirs {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return firstSegmentIsProjectDir(s, root)
}

// conventionalSourceDirs are prefixes that mark a path as project-rooted even
// when the directory is absent — which is the point: a doc referencing
// `lib/app/gone.ex` in a project with no lib/ is a stale reference worth
// reporting, not an unrecognised layout.
var conventionalSourceDirs = []string{
	"lib/", "src/", "app/", "pkg/", "internal/", "cmd/",
	"test/", "tests/", "spec/", "priv/", "config/", "bin/",
	"assets/", "static/", "public/", "web/", "backend/", "frontend/",
}

// firstSegmentIsProjectDir reports whether a candidate's first path segment is
// a real directory under root. This is what lets the check work in a repo laid
// out as services/, packages/, apps/ or anything else, without maintaining a
// list of every convention in existence.
func firstSegmentIsProjectDir(s, root string) bool {
	segment := s
	if slash := strings.Index(s, "/"); slash >= 0 {
		segment = s[:slash]
	}
	if segment == "" || segment == "." || segment == ".." {
		return false
	}

	// Dot-directories hold state and config, not source, and much of what docs
	// say about them is a forward reference: the runbook for enabling embeddings
	// legitimately talks about `.rivet/embeddings/` before anything has created
	// it. Treating those as stale references is noise by construction.
	if strings.HasPrefix(segment, ".") {
		return false
	}

	info, err := os.Stat(filepath.Join(root, segment))
	return err == nil && info.IsDir()
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
