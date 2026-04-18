package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateLearningAndLoad(t *testing.T) {
	dir := t.TempDir()

	entry, err := CreateLearning(dir, NewLearning{
		Title:          "Retry Split Across Systems",
		Author:         "damien",
		Confidence:     "medium",
		RelatedPaths:   []string{"services/scheduler/**", "adapters/retry/**"},
		SuggestedDoc:   "retry",
		Observation:    "Retry scheduling is in scheduler, execution is in adapter.",
		Impact:         "Easy to miss one side when changing retry behavior.",
		Recommendation: "Always check both layers.",
		Date:           time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if entry.Title != "Retry Split Across Systems" {
		t.Errorf("title mismatch: %q", entry.Title)
	}
	if entry.Author != "damien" {
		t.Errorf("author mismatch: %q", entry.Author)
	}
	if entry.SuggestedDoc != "retry" {
		t.Errorf("suggested_doc mismatch: %q", entry.SuggestedDoc)
	}
	if entry.Promoted {
		t.Error("new entry should not be promoted")
	}
	if len(entry.RelatedPaths) != 2 {
		t.Errorf("expected 2 related_paths, got %d", len(entry.RelatedPaths))
	}
	if !strings.HasPrefix(filepath.Base(entry.Path), "2026-04-18-retry-split-across-systems-") {
		t.Errorf("unexpected filename: %s", filepath.Base(entry.Path))
	}
	if !strings.HasSuffix(entry.Path, ".md") {
		t.Errorf("expected .md suffix, got %s", entry.Path)
	}

	entries, err := LoadLearnings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	got := entries[0]
	if got.Title != entry.Title {
		t.Errorf("loaded title mismatch: %q", got.Title)
	}
	if !strings.Contains(got.Body, "## Observation") {
		t.Error("expected Observation section in body")
	}
	if !strings.Contains(got.Body, "Always check both layers.") {
		t.Error("expected Recommendation text in body")
	}
}

func TestCreateLearningRequiresTitleAndObservation(t *testing.T) {
	dir := t.TempDir()

	if _, err := CreateLearning(dir, NewLearning{Observation: "ok"}); err == nil {
		t.Error("expected error for missing title")
	}
	if _, err := CreateLearning(dir, NewLearning{Title: "ok"}); err == nil {
		t.Error("expected error for missing observation")
	}
}

func TestCreateLearningConcurrencyNoCollision(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC)

	var paths []string
	for i := 0; i < 5; i++ {
		entry, err := CreateLearning(dir, NewLearning{
			Title:       "Same Title",
			Observation: "same day different entries",
			Date:        now,
		})
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, entry.Path)
	}
	seen := make(map[string]bool)
	for _, p := range paths {
		if seen[p] {
			t.Errorf("collision: %s written twice", p)
		}
		seen[p] = true
	}
}

func TestMarkPromotedAndCountActive(t *testing.T) {
	dir := t.TempDir()
	e1, err := CreateLearning(dir, NewLearning{Title: "A", Observation: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateLearning(dir, NewLearning{Title: "B", Observation: "b"}); err != nil {
		t.Fatal(err)
	}

	if n := CountActive(dir); n != 2 {
		t.Errorf("expected 2 active, got %d", n)
	}

	if err := MarkPromoted(e1.Path, "retry"); err != nil {
		t.Fatal(err)
	}

	entries, err := LoadLearnings(dir)
	if err != nil {
		t.Fatal(err)
	}
	var promoted *LearningEntry
	for _, e := range entries {
		if e.Path == e1.Path {
			promoted = e
		}
	}
	if promoted == nil {
		t.Fatal("promoted entry not found after reload")
	}
	if !promoted.Promoted {
		t.Error("expected promoted=true after MarkPromoted")
	}
	if promoted.PromotedTo != "retry" {
		t.Errorf("expected promoted_to=retry, got %q", promoted.PromotedTo)
	}

	if n := CountActive(dir); n != 1 {
		t.Errorf("expected 1 active after promotion, got %d", n)
	}
}

func TestArchiveLearning(t *testing.T) {
	dir := t.TempDir()
	e, err := CreateLearning(dir, NewLearning{Title: "X", Observation: "x"})
	if err != nil {
		t.Fatal(err)
	}
	dest, err := ArchiveLearning(e.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.Path); !os.IsNotExist(err) {
		t.Error("original path should not exist after archive")
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("archive dest missing: %v", err)
	}
	if filepath.Dir(dest) != filepath.Join(dir, "archive") {
		t.Errorf("expected archive subdir, got %s", dest)
	}

	// Archive is excluded from LoadLearnings.
	entries, err := LoadLearnings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 active entries after archive, got %d", len(entries))
	}
}

func TestLoadLearningsMissingDir(t *testing.T) {
	entries, err := LoadLearnings(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestRecommendLearningsExcludesPromoted(t *testing.T) {
	dir := t.TempDir()
	active, err := CreateLearning(dir, NewLearning{
		Title:        "Retry Split",
		Observation:  "scheduler and adapter are separate",
		RelatedPaths: []string{"services/scheduler/**"},
	})
	if err != nil {
		t.Fatal(err)
	}
	old, err := CreateLearning(dir, NewLearning{
		Title:       "Retry Already Promoted",
		Observation: "should not appear",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkPromoted(old.Path, "retry"); err != nil {
		t.Fatal(err)
	}

	entries, err := LoadLearnings(dir)
	if err != nil {
		t.Fatal(err)
	}

	recs := RecommendLearnings(entries, "retry scheduler", 5)
	if len(recs) != 1 {
		t.Fatalf("expected 1 rec (promoted excluded), got %d", len(recs))
	}
	if recs[0].Path != active.Path {
		t.Errorf("expected active entry, got %s", recs[0].Path)
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"Retry Split Across Systems", "retry-split-across-systems"},
		{"  Multiple   Spaces  ", "multiple-spaces"},
		{"Path/With/Slashes", "path-with-slashes"},
		{"!!!", "learning"},
		{"UPPER_lower-MIX", "upper-lower-mix"},
	}
	for _, tc := range cases {
		if got := slugify(tc.in); got != tc.out {
			t.Errorf("slugify(%q) = %q, want %q", tc.in, got, tc.out)
		}
	}
}
