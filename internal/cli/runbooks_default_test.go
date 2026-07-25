package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
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

	// An agent on a lexical-only build can find the setup procedure by symptom.
	matches := rivetctx.RecommendRunbooks(books, "turn on semantic search", 5)
	if len(matches) == 0 || !strings.Contains(matches[0].Title, "semantic search") {
		t.Errorf("setup runbook not found by symptom: %+v", matches)
	}

	// The exact query rivet doctor and the README tell people to use must
	// reach it too — those strings are a promise, not a hint.
	matches = rivetctx.RecommendRunbooks(books, "set up embeddings", 5)
	if len(matches) == 0 || matches[0].Name != "setup-semantic-search" {
		t.Errorf(`"set up embeddings" should rank setup-semantic-search first, got %+v`, matches)
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

// A runbook is a shared document, not rivet's own generated output: a team's
// edits must survive re-running update. But skipping in silence is how a
// project ends up carrying a procedure rivet rewrote months ago and never
// hearing about it, so the difference has to be reported.
func TestEnsureRunbooks_KeepsLocalEditsAndReportsDrift(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := ensureRunbooks(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(".rivet", "runbooks", "setup-semantic-search.md")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := string(original) + "\n## Our extra step\n"
	if err := os.WriteFile(path, []byte(edited), 0644); err != nil {
		t.Fatal(err)
	}

	actions, err := ensureRunbooks()
	if err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != edited {
		t.Error("a locally edited runbook was overwritten")
	}

	var reported bool
	for _, a := range actions {
		if strings.Contains(a, "setup-semantic-search.md") && strings.Contains(a, "differs") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("local edit was not reported back to the user; actions = %v", actions)
	}
}

// "Differs from what rivet ships" covers two opposite cases. A copy that
// matches a version rivet shipped previously carries none of the user's work,
// so leaving it in place forever is just skip-if-exists again — found in the
// wild on a project still carrying the ONNX-first setup procedure months after
// it was rewritten.
func TestEnsureRunbooks_UpdatesUnmodifiedOldVersions(t *testing.T) {
	t.Chdir(t.TempDir())

	dir := filepath.Join(".rivet", "runbooks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "setup-semantic-search.md")

	// Any content whose digest is registered as superseded stands in for "an
	// older copy rivet itself wrote".
	old := []byte("stale but rivet's own\n")
	sum := sha256.Sum256(old)
	supersededRunbooks["setup-semantic-search.md"] = append(
		supersededRunbooks["setup-semantic-search.md"], hex.EncodeToString(sum[:]))
	t.Cleanup(func() {
		s := supersededRunbooks["setup-semantic-search.md"]
		supersededRunbooks["setup-semantic-search.md"] = s[:len(s)-1]
	})

	if err := os.WriteFile(path, old, 0644); err != nil {
		t.Fatal(err)
	}

	actions, err := ensureRunbooks()
	if err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) == string(old) {
		t.Error("an unmodified old version should have been updated")
	}

	var reported bool
	for _, a := range actions {
		if strings.Contains(a, "setup-semantic-search.md") && strings.Contains(a, "updated") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("update was not reported; actions = %v", actions)
	}
}

// The digests in supersededRunbooks must name versions that are genuinely
// superseded. Listing the current one would make rivet overwrite a file with
// itself and report an update that did not happen.
func TestSupersededDigestsAreNotCurrent(t *testing.T) {
	for name, digests := range supersededRunbooks {
		want, err := defaultRunbooks.ReadFile("runbooks/" + name)
		if err != nil {
			t.Errorf("supersededRunbooks names %q, which rivet does not ship", name)
			continue
		}
		cur := sha256.Sum256(want)
		for _, d := range digests {
			if d == hex.EncodeToString(cur[:]) {
				t.Errorf("%s: the currently shipped version is listed as superseded", name)
			}
		}
	}
}
