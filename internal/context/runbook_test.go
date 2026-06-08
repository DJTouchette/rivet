package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func rb(name, title string, triggers []string) *Document {
	return &Document{Name: name, Kind: KindRunbook, Title: title, Triggers: triggers, Body: "do things"}
}

func TestRecommendRunbooks_TriggerRanking(t *testing.T) {
	books := []*Document{
		rb("payments", "Payment recovery", []string{"payments failing", "webhook backlog"}),
		rb("deploy", "Deploy rollback", []string{"bad deploy", "rollback"}),
	}
	matches := RecommendRunbooks(books, "payments are failing", 5)
	if len(matches) == 0 {
		t.Fatal("expected a match")
	}
	if matches[0].Name != "payments" {
		t.Errorf("top match = %q, want payments", matches[0].Name)
	}
	hasTrigger := false
	for _, s := range matches[0].Signals {
		if s == "trigger-match" {
			hasTrigger = true
		}
	}
	if !hasTrigger {
		t.Errorf("expected trigger-match signal, got %v", matches[0].Signals)
	}
}

func TestRecommendRunbooks_NoMatch(t *testing.T) {
	books := []*Document{rb("payments", "Payment recovery", []string{"payments failing"})}
	if got := RecommendRunbooks(books, "kubernetes pod scheduling", 5); len(got) != 0 {
		t.Errorf("expected no matches, got %+v", got)
	}
	if got := RecommendRunbooks(books, "", 5); got != nil {
		t.Error("empty query should return nil")
	}
	if got := RecommendRunbooks(nil, "x", 5); got != nil {
		t.Error("no runbooks should return nil")
	}
}

func TestScoreTriggerMatch(t *testing.T) {
	triggers := []string{"payments failing", "webhook queue backlog"}
	// All tokens hit a trigger → 0.7.
	if w, _ := scoreTriggerMatch(triggers, []string{"payments", "webhook"}); w != 0.7 {
		t.Errorf("full coverage = %v, want 0.7", w)
	}
	// Partial coverage → 0.6 * fraction.
	if w, _ := scoreTriggerMatch(triggers, []string{"payments", "kubernetes"}); w != 0.3 {
		t.Errorf("half coverage = %v, want 0.3", w)
	}
	if w, _ := scoreTriggerMatch(nil, []string{"x"}); w != 0 {
		t.Errorf("no triggers = %v, want 0", w)
	}
}

func TestCreateAndPromoteRunbookDraft(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runbooks")

	draftPath, err := CreateRunbookDraft(dir, NewRunbook{
		Title:        "DB failover",
		Triggers:     []string{"primary db down"},
		Severity:     "critical",
		Owner:        "platform",
		Steps:        "1. Promote replica.",
		Verification: "Writes succeed.",
		Rollback:     "Repoint to primary.",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Draft lands in drafts/ and is excluded from LoadRunbooks.
	if filepath.Base(filepath.Dir(draftPath)) != DraftsSubdir {
		t.Errorf("draft not in drafts/: %s", draftPath)
	}
	loaded, _ := LoadRunbooks(filepath.Dir(dir))
	for _, d := range loaded {
		if strings.Contains(d.Path, DraftsSubdir) {
			t.Error("draft should not be loaded")
		}
	}
	raw, _ := os.ReadFile(draftPath)
	if !strings.Contains(string(raw), "status: draft") {
		t.Error("draft should carry status: draft")
	}

	// Promote moves it up and strips the draft marker.
	dest, err := PromoteRunbookDraft(draftPath)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(dest)) != "runbooks" {
		t.Errorf("promoted to wrong dir: %s", dest)
	}
	if _, err := os.Stat(draftPath); !os.IsNotExist(err) {
		t.Error("draft file should be gone after promotion")
	}
	promoted, _ := os.ReadFile(dest)
	if strings.Contains(string(promoted), "status: draft") {
		t.Error("promoted runbook should not keep status: draft")
	}
	if !strings.Contains(string(promoted), "primary db down") {
		t.Error("promoted runbook lost its triggers")
	}
}

func TestCreateRunbookDraft_Validation(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateRunbookDraft(dir, NewRunbook{Steps: "x"}); err == nil {
		t.Error("missing title should error")
	}
	if _, err := CreateRunbookDraft(dir, NewRunbook{Title: "x"}); err == nil {
		t.Error("missing steps should error")
	}
}

func TestPromoteRunbookDraft_Errors(t *testing.T) {
	// A path not under drafts/ is rejected.
	dir := t.TempDir()
	notDraft := filepath.Join(dir, "x.md")
	os.WriteFile(notDraft, []byte("# x"), 0o644)
	if _, err := PromoteRunbookDraft(notDraft); err == nil {
		t.Error("promoting a non-draft path should error")
	}
}

func TestLintRunbookRules(t *testing.T) {
	// A runbook with no triggers, no owner, no last_tested.
	bad := &Document{Name: "bad", Kind: KindRunbook, Title: "Bad", Body: "## Steps\ndo it"}
	warns := lintDoc(bad, t.TempDir())
	rules := map[string]bool{}
	for _, w := range warns {
		rules[w.Rule] = true
	}
	for _, want := range []string{"missing-triggers", "missing-owner", "untested-runbook"} {
		if !rules[want] {
			t.Errorf("expected rule %q, got %v", want, rules)
		}
	}
	// Context-only rules must NOT fire for runbooks.
	for _, notWant := range []string{"missing-tags", "missing-related-paths", "stale-review"} {
		if rules[notWant] {
			t.Errorf("rule %q should not apply to runbooks", notWant)
		}
	}
}

func TestLintRunbookStaleTest(t *testing.T) {
	old := &Document{
		Name: "old", Kind: KindRunbook, Title: "Old", Body: "## Steps\nx",
		Triggers: []string{"t"}, Owner: "o",
		LastTested: time.Now().AddDate(0, 0, -StaleTestDays-10),
	}
	var found bool
	for _, w := range lintDoc(old, t.TempDir()) {
		if w.Rule == "stale-test" {
			found = true
		}
	}
	if !found {
		t.Error("expected stale-test for an old last_tested")
	}
}

func TestLintWikiIsLenient(t *testing.T) {
	// A wiki page with no tags/owner/related_paths should not be nagged.
	w := &Document{Name: "page", Kind: KindWiki, Title: "Page", Body: "Some prose."}
	for _, warn := range lintDoc(w, t.TempDir()) {
		if warn.Rule == "missing-tags" || warn.Rule == "missing-owner" || warn.Rule == "missing-related-paths" || warn.Rule == "stale-review" {
			t.Errorf("wiki should not get context-doc nag %q", warn.Rule)
		}
	}
}

func TestKindWeight(t *testing.T) {
	if kindWeight(KindDomain) != 1.0 || kindWeight(KindModule) != 1.0 {
		t.Error("context kinds should weight 1.0")
	}
	if kindWeight(KindWiki) != 0.85 {
		t.Errorf("wiki weight = %v, want 0.85", kindWeight(KindWiki))
	}
}

func TestLoadWiki_DedupeOverlappingRoots(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs/sub/page.md"), "# Page")
	// Two overlapping roots that both cover docs/sub/page.md.
	docs, err := LoadWiki(root, []string{"docs/**", "docs/sub/**"})
	if err != nil {
		t.Fatal(err)
	}
	// The shared file must appear exactly once (deduped by absolute path).
	count := 0
	for _, d := range docs {
		if strings.HasSuffix(d.Path, "page.md") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("overlapping roots should dedupe to one entry, got %d", count)
	}
}

func TestPromoteRunbookDraft_Collision(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runbooks")
	draftPath, err := CreateRunbookDraft(dir, NewRunbook{Title: "Dup", Steps: "x"})
	if err != nil {
		t.Fatal(err)
	}
	// Pre-create the destination so promotion collides.
	dest := filepath.Join(dir, filepath.Base(draftPath))
	if err := os.WriteFile(dest, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PromoteRunbookDraft(draftPath); err == nil {
		t.Error("promotion onto an existing file should error")
	}
}

func TestStripStatusDraft_NoBlankArtifacts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runbooks")
	draftPath, _ := CreateRunbookDraft(dir, NewRunbook{Title: "Clean", Triggers: []string{"t"}, Steps: "1. do"})
	dest, err := PromoteRunbookDraft(draftPath)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(dest)
	if strings.Contains(string(body), "\n\n\n") {
		t.Errorf("promoted runbook has blank-line artifacts:\n%s", body)
	}
	if strings.Contains(string(body), "status: draft") || strings.Contains(string(body), "Draft runbook pending") {
		t.Error("draft markers should be stripped")
	}
}

func TestRecommend_WikiRankedBelowContext(t *testing.T) {
	// A context doc and a wiki doc that match the query equally on body should
	// see the wiki ranked lower due to kindWeight.
	ctxDoc := &Document{Name: "ctx", Kind: KindDomain, Title: "Billing", Body: "invoice retry logic"}
	wikiDoc := &Document{Name: "wiki", Kind: KindWiki, Title: "Billing", Body: "invoice retry logic"}
	recs := Recommend([]*Document{ctxDoc, wikiDoc}, "invoice retry", 5)
	if len(recs) != 2 {
		t.Fatalf("expected 2 results, got %d", len(recs))
	}
	if recs[0].Kind != KindDomain {
		t.Errorf("context doc should rank first, got %q", recs[0].Kind)
	}
	if recs[1].Score >= recs[0].Score {
		t.Errorf("wiki (%.3f) should score below context (%.3f)", recs[1].Score, recs[0].Score)
	}
}
