package cli

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// defaultRunbooks holds the operational runbooks that ship with rivet.
//
// These live as real markdown rather than Go string constants. The constants
// they replaced had to escape every backtick inline, which in a document that
// is mostly fenced shell blocks meant the shipped copy and the dogfooded copy
// in .rivet/runbooks/ were kept in sync by hand — two files, one of them
// barely readable, and no mechanism that would have caught drift between them.
// Embedding the markdown makes the readable copy the only copy.
//
//go:embed runbooks/*.md
var defaultRunbooks embed.FS

// ensureRunbooks writes the default operational runbooks to .rivet/runbooks/.
//
// Non-destructive: an existing file is never overwritten, because runbooks are
// meant to be edited — teams add site-specific steps and bump last_tested, and
// clobbering that would destroy work rivet did not author. (This is the one
// place refreshGenerated must not be used; agent briefs and skills are rivet's
// own output, a runbook is a shared document.)
//
// But silently skipping is its own failure, and the same one this codebase
// keeps finding: a project initialised months ago keeps a stale copy of a
// procedure rivet has since rewritten, and nothing ever mentions it. So compare
// and report, leaving the decision to update as an informed one rather than an
// invisible one.
//
// These ship so an agent can `rivet.runbook find ...` and follow a vetted
// procedure — note that runbook *retrieval* works on the default (lexical)
// build, so the semantic-search setup runbook is findable before semantic
// search is enabled.
func ensureRunbooks() ([]string, error) {
	var actions []string

	dir := filepath.Join(".rivet", "runbooks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}

	entries, err := defaultRunbooks.ReadDir("runbooks")
	if err != nil {
		return nil, fmt.Errorf("reading embedded runbooks: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // deterministic action order

	for _, name := range names {
		want, err := defaultRunbooks.ReadFile("runbooks/" + name)
		if err != nil {
			return nil, fmt.Errorf("reading embedded runbook %s: %w", name, err)
		}

		path := filepath.Join(dir, name)
		current, readErr := os.ReadFile(path)
		switch {
		case readErr == nil && string(current) == string(want):
			actions = append(actions, fmt.Sprintf("%s already current, skipped", path))
		case readErr == nil:
			actions = append(actions, fmt.Sprintf(
				"%s differs from the version rivet ships (yours: %s) — left as-is",
				path, runbookStamp(current)))
		default:
			if err := os.WriteFile(path, want, 0644); err != nil {
				return nil, fmt.Errorf("writing %s: %w", path, err)
			}
			actions = append(actions, "wrote "+path)
		}
	}
	return actions, nil
}

// runbookStamp summarises an on-disk runbook for the "yours differs" message,
// preferring last_tested since that is the field a team actually maintains.
func runbookStamp(content []byte) string {
	for _, line := range strings.Split(string(content), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "last_tested:"); ok {
			if v := strings.TrimSpace(rest); v != "" {
				return "last_tested " + v
			}
		}
	}
	return "edited locally"
}
