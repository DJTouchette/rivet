package cli

import (
	"strings"
	"testing"

	rivetctx "github.com/djtouchette/rivet/internal/context"
)

func TestEnsureRunbooks(t *testing.T) {
	t.Chdir(t.TempDir())

	actions, err := ensureRunbooks()
	if err != nil {
		t.Fatalf("ensureRunbooks: %v", err)
	}
	if len(actions) != 2 {
		t.Fatalf("expected 2 runbooks written, got %d: %v", len(actions), actions)
	}

	// They load, parse their triggers, and are valid (not drafts).
	books, err := rivetctx.LoadRunbooks(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 2 {
		t.Fatalf("loaded %d runbooks, want 2", len(books))
	}

	// An agent on a lexical-only build can find the ONNX setup by symptom.
	matches := rivetctx.RecommendRunbooks(books, "turn on semantic search", 5)
	if len(matches) == 0 || !strings.Contains(matches[0].Title, "semantic search") {
		t.Errorf("setup runbook not found by symptom: %+v", matches)
	}

	// Shipped defaults must be lint-clean (triggers, owner, last_tested set).
	res := rivetctx.Lint(books, ".")
	if len(res.Warnings) != 0 {
		t.Errorf("default runbooks should lint clean, got: %+v", res.Warnings)
	}
}

func TestEnsureRunbooks_Idempotent(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := ensureRunbooks(); err != nil {
		t.Fatal(err)
	}
	// Second run must not overwrite — every action reports "skipped".
	actions, err := ensureRunbooks()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range actions {
		if !strings.Contains(a, "skipped") {
			t.Errorf("re-running should skip existing files, got %q", a)
		}
	}
}
