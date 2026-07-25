package context

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLintMissingTags(t *testing.T) {
	doc := &Document{
		Name: "billing",
		Kind: KindDomain,
		Path: "test/billing.md",
		Body: "# Billing\n\nHandles invoices.",
	}
	result := Lint([]*Document{doc}, t.TempDir())

	found := false
	for _, w := range result.Warnings {
		if w.Rule == "missing-tags" {
			found = true
		}
	}
	if !found {
		t.Error("expected missing-tags warning")
	}
}

func TestLintMissingRelatedPaths(t *testing.T) {
	doc := &Document{
		Name: "billing",
		Kind: KindDomain,
		Path: "test/billing.md",
		Tags: []string{"billing"},
		Body: "# Billing\n\nHandles invoices.",
	}
	result := Lint([]*Document{doc}, t.TempDir())

	found := false
	for _, w := range result.Warnings {
		if w.Rule == "missing-related-paths" {
			found = true
		}
	}
	if !found {
		t.Error("expected missing-related-paths warning")
	}
}

func TestLintPlaceholderSections(t *testing.T) {
	doc := &Document{
		Name:         "billing",
		Kind:         KindDomain,
		Path:         "test/billing.md",
		Tags:         []string{"billing"},
		RelatedPaths: []string{"lib/billing/**"},
		Body: `# Billing

## Overview

<!-- Describe what this domain does -->

## Gotchas

<!-- Non-obvious things -->
`,
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "lib", "billing"), 0755)

	result := Lint([]*Document{doc}, root)

	var placeholderWarning *LintWarning
	for i := range result.Warnings {
		if result.Warnings[i].Rule == "placeholder-section" {
			placeholderWarning = &result.Warnings[i]
		}
	}
	if placeholderWarning == nil {
		t.Fatal("expected placeholder-section warning")
	}
	if placeholderWarning.Severity != SeverityWarning {
		t.Errorf("expected warning severity, got %s", placeholderWarning.Severity)
	}
}

func TestLintEmptyBody(t *testing.T) {
	doc := &Document{
		Name:         "billing",
		Kind:         KindDomain,
		Path:         "test/billing.md",
		Tags:         []string{"billing"},
		RelatedPaths: []string{"lib/billing/**"},
		Body:         "# Billing\n\n",
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "lib", "billing"), 0755)

	result := Lint([]*Document{doc}, root)

	found := false
	for _, w := range result.Warnings {
		if w.Rule == "empty-body" {
			found = true
			if w.Severity != SeverityError {
				t.Errorf("expected error severity, got %s", w.Severity)
			}
		}
	}
	if !found {
		t.Error("expected empty-body warning")
	}
}

func TestLintStaleRelatedPath(t *testing.T) {
	root := t.TempDir()
	doc := &Document{
		Name:         "billing",
		Kind:         KindDomain,
		Path:         "test/billing.md",
		Tags:         []string{"billing"},
		RelatedPaths: []string{"lib/billing/**", "lib/nonexistent/**"},
		Body:         "# Billing\n\nHandles invoices.",
	}
	os.MkdirAll(filepath.Join(root, "lib", "billing"), 0755)

	result := Lint([]*Document{doc}, root)

	found := false
	for _, w := range result.Warnings {
		if w.Rule == "stale-related-path" {
			found = true
		}
	}
	if !found {
		t.Error("expected stale-related-path warning for lib/nonexistent/**")
	}
}

func TestLintStaleReference(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "lib", "billing"), 0755)
	os.WriteFile(filepath.Join(root, "lib", "billing", "invoice.ex"), []byte("defmodule"), 0644)

	doc := &Document{
		Name:         "billing",
		Kind:         KindDomain,
		Path:         "test/billing.md",
		Tags:         []string{"billing"},
		RelatedPaths: []string{"lib/billing/**"},
		// Deliberately neutral prose. A sentence that says the file is gone is
		// documenting history and is exempt on purpose — see
		// TestLintStaleReferenceAllowsDocumentedRemovals — so wording it that
		// way here would test the exemption rather than the rule.
		Body: `# Billing

- ` + "`lib/billing/invoice.ex`" + ` — handles invoices
- ` + "`lib/billing/deleted_file.ex`" + ` — computes the discount ladder
`,
	}

	result := Lint([]*Document{doc}, root)

	found := false
	for _, w := range result.Warnings {
		if w.Rule == "stale-reference" {
			found = true
		}
	}
	if !found {
		t.Error("expected stale-reference warning for lib/billing/deleted_file.ex")
	}
}

// A doc that records what a path used to be is doing its job. Flagging it asks
// the author to delete the fact they went out of their way to write down.
func TestLintStaleReferenceAllowsDocumentedRemovals(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "pipelines"), 0755)

	doc := &Document{
		Name: "deployment",
		Kind: KindParadigm,
		Path: "test/deployment.md",
		Tags: []string{"deployment"},
		Body: `# Deployment

- The old branch-per-env model and ` + "`pipelines/templates/deploy-stage.yml`" + ` are retired.
- ` + "`pipelines/legacy/build.yml`" + ` was replaced by the multi-stage pipeline.
- Config moved: ` + "`config/old-settings.yml`" + ` is no longer read.
`,
	}

	result := Lint([]*Document{doc}, root)

	for _, w := range result.Warnings {
		if w.Rule == "stale-reference" {
			t.Errorf("unexpected stale-reference for a documented removal: %s", w.Message)
		}
	}
}

// A missing file is only evidence of a stale doc if the file was ever meant to
// be there. Build outputs, runtime state and local config are legitimately
// absent from a clean checkout and legitimately named in prose.
func TestLintStaleReferenceSkipsGitignoredPaths(t *testing.T) {
	root := t.TempDir()
	if err := exec.Command("git", "-C", root, "init", "-q").Run(); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	os.MkdirAll(filepath.Join(root, "qa", "workbench"), 0755)
	// Nested .gitignore files, as real projects have — the reason this uses
	// git check-ignore rather than parsing the root .gitignore.
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("environment/.env.*\n"), 0644)
	os.WriteFile(filepath.Join(root, "qa", ".gitignore"), []byte("/index.html\n"), 0644)
	os.WriteFile(filepath.Join(root, "qa", "workbench", ".gitignore"), []byte("proposals.json\n"), 0644)

	doc := &Document{
		Name: "qa",
		Kind: KindParadigm,
		Path: "test/qa.md",
		Tags: []string{"qa"},
		Body: `# QA

- ` + "`qa/index.html`" + ` is the generated static site.
- Proposal state lives in ` + "`qa/workbench/proposals.json`" + `.
- Credentials come from ` + "`environment/.env.dev`" + `.
- Engine source is ` + "`qa/workbench/engine.ts`" + `.
`,
	}

	result := Lint([]*Document{doc}, root)

	var flagged []string
	for _, w := range result.Warnings {
		if w.Rule == "stale-reference" {
			flagged = append(flagged, w.Message)
		}
	}
	// The three gitignored paths are exempt; the tracked-but-absent one is not,
	// which is what proves the rule still works rather than being switched off.
	if len(flagged) != 1 {
		t.Fatalf("expected exactly 1 stale-reference (engine.ts), got %d: %v", len(flagged), flagged)
	}
	if !strings.Contains(flagged[0], "engine.ts") {
		t.Errorf("wrong path flagged: %s", flagged[0])
	}
}

// Outside a git repository the rule must keep working rather than silently
// passing everything.
func TestLintStaleReferenceWorksWithoutGit(t *testing.T) {
	root := t.TempDir()

	doc := &Document{
		Name: "billing",
		Kind: KindDomain,
		Path: "test/billing.md",
		Tags: []string{"billing"},
		Body: "# Billing\n\n- `lib/billing/invoice.ex` computes invoices.\n",
	}

	result := Lint([]*Document{doc}, root)

	found := false
	for _, w := range result.Warnings {
		if w.Rule == "stale-reference" {
			found = true
		}
	}
	if !found {
		t.Error("stale reference should still be reported when root is not a git repo")
	}
}

func TestLintStaleReferenceIgnoresNonRootedPaths(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "lib", "billing"), 0755)

	doc := &Document{
		Name:         "billing",
		Kind:         KindDomain,
		Path:         "test/billing.md",
		Tags:         []string{"billing"},
		RelatedPaths: []string{"lib/billing/**"},
		Body: `# Billing

Uses ` + "`changeset/2`" + ` and ` + "`Billing.handle_webhook/2`" + ` internally.
Route: ` + "`/api/billing`" + `.
MIME: ` + "`text/xml`" + `.
Template: ` + "`lib/billing/{context}.ex`" + `.
Short: ` + "`billing/invoice.ex`" + ` is domain-relative.
`,
	}

	result := Lint([]*Document{doc}, root)

	for _, w := range result.Warnings {
		if w.Rule == "stale-reference" {
			t.Errorf("unexpected stale-reference: %s", w.Message)
		}
	}
}

func TestLintCleanDoc(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "lib", "billing"), 0755)
	os.WriteFile(filepath.Join(root, "lib", "billing", "invoice.ex"), []byte("defmodule"), 0644)

	doc := &Document{
		Name:         "billing",
		Kind:         KindDomain,
		Path:         filepath.Join(root, "billing.md"),
		Tags:         []string{"billing", "invoice"},
		RelatedPaths: []string{"lib/billing/**"},
		Owner:        "damien",
		LastReviewed: time.Now().AddDate(0, 0, -1),
		Body: `# Billing

## Overview

Handles invoice generation and payment retries.

## Key modules

- ` + "`lib/billing/invoice.ex`" + ` — invoice generation
`,
	}

	result := Lint([]*Document{doc}, root)

	if len(result.Warnings) != 0 {
		for _, w := range result.Warnings {
			t.Errorf("unexpected warning: [%s] %s: %s", w.Severity, w.Rule, w.Message)
		}
	}
}

func TestLintMissingOwner(t *testing.T) {
	doc := &Document{
		Name:         "billing",
		Kind:         KindDomain,
		Path:         "test/billing.md",
		Tags:         []string{"billing"},
		RelatedPaths: []string{"lib/billing/**"},
		LastReviewed: time.Now(),
		Body:         "# Billing\n\nHandles invoices.",
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "lib", "billing"), 0755)

	result := Lint([]*Document{doc}, root)

	found := false
	for _, w := range result.Warnings {
		if w.Rule == "missing-owner" {
			found = true
		}
	}
	if !found {
		t.Error("expected missing-owner warning")
	}
}

func TestLintStaleReview(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "lib", "billing"), 0755)

	old := &Document{
		Name:         "billing",
		Kind:         KindDomain,
		Path:         "test/billing.md",
		Tags:         []string{"billing"},
		RelatedPaths: []string{"lib/billing/**"},
		Owner:        "damien",
		LastReviewed: time.Now().AddDate(0, 0, -StaleReviewDays-1),
		Body:         "# Billing\n\nHandles invoices.",
	}
	missing := &Document{
		Name:         "auth",
		Kind:         KindDomain,
		Path:         "test/auth.md",
		Tags:         []string{"auth"},
		RelatedPaths: []string{"lib/billing/**"},
		Owner:        "damien",
		Body:         "# Auth\n\nHandles auth.",
	}

	result := Lint([]*Document{old, missing}, root)

	// An absent date and an expired one are separate rules, so that "which docs
	// have actually gone stale" is answerable without matching on the message.
	rules := map[string]string{}
	for _, w := range result.Warnings {
		if w.Rule == "stale-review" || w.Rule == "missing-review" {
			rules[w.Document] = w.Rule
		}
	}

	if rules["billing"] != "stale-review" {
		t.Errorf("old billing doc should be stale-review, got %q", rules["billing"])
	}
	if rules["auth"] != "missing-review" {
		t.Errorf("auth doc with no last_reviewed should be missing-review, got %q", rules["auth"])
	}
}

func TestLintDocsReadCount(t *testing.T) {
	docs := []*Document{
		{Name: "a", Kind: KindDomain, Body: "# A\n\nContent."},
		{Name: "b", Kind: KindModule, Body: "# B\n\nContent."},
	}
	result := Lint(docs, t.TempDir())
	if result.DocsRead != 2 {
		t.Errorf("expected DocsRead=2, got %d", result.DocsRead)
	}
}

func TestExtractBacktickPaths(t *testing.T) {
	tests := []struct {
		line string
		want int
	}{
		{"`lib/billing/invoice.ex` — handles invoices", 1},
		{"`lib/a.ex` and `lib/b.ex` are related", 2},
		{"no backticks here", 0},
		{"`billing` is not a path", 0},                        // no slash
		{"`mix ecto.migrate` is a command", 0},                // has space
		{"`https://example.com/api` is a URL", 0},             // URL
		{"`lib/billing/invoice.ex` and `--flag` stuff", 1},    // flag filtered
		{"`Billing.Invoice` is a module name, not a path", 0}, // no slash
		{"`changeset/2` is arity notation", 0},                // arity
		{"`Module.func/1` is a module ref", 0},                // uppercase
		{"`/api/billing` is a route", 0},                      // starts with /
		{"`text/xml` is a MIME type", 0},                      // no known prefix
		{"`billing/invoice.ex` is not rooted", 0},             // no known prefix
		{"`lib/billing/{context}.ex` has template braces", 0}, // braces
		{"`:telemetry.execute/3` is an atom ref", 0},          // starts with :
	}

	// An empty root means the "is it a real project directory" fallback can
	// never fire, so these cases exercise the conventional-prefix path alone.
	root := t.TempDir()
	for _, tt := range tests {
		got := extractBacktickPaths(tt.line, root)
		if len(got) != tt.want {
			t.Errorf("extractBacktickPaths(%q) = %v (len %d), want len %d", tt.line, got, len(got), tt.want)
		}
	}
}

func TestLooksLikeFilePath(t *testing.T) {
	tests := []struct {
		s    string
		want bool
	}{
		{"lib/billing/invoice.ex", true},
		{"src/handlers/", true},
		{"internal/pkg/bar.go", true},
		{"test/billing_test.exs", true},
		{"app/models/user.rb", true},
		{"billing", false},                  // no slash
		{"mix ecto.migrate", false},         // has space
		{"--json", false},                   // flag
		{"https://example.com", false},      // URL
		{"fn()", false},                     // parens
		{"a=b/c", false},                    // equals
		{"", false},                         // empty
		{"ab", false},                       // too short
		{"changeset/2", false},              // arity notation
		{"handle_webhook/3", false},         // arity notation
		{"Module.func/1", false},            // uppercase module
		{"Billing.Invoice", false},          // uppercase, no slash
		{"/api/billing", false},             // route (starts with /)
		{":telemetry.execute/3", false},     // atom ref
		{"text/xml", false},                 // no known prefix
		{"billing/invoice.ex", false},       // no known prefix
		{"lib/billing/{context}.ex", false}, // braces
	}

	root := t.TempDir()
	for _, tt := range tests {
		got := looksLikeFilePath(tt.s, root)
		if got != tt.want {
			t.Errorf("looksLikeFilePath(%q, emptyRoot) = %v, want %v", tt.s, got, tt.want)
		}
	}
}

// The conventional-prefix list can't anticipate every layout, so a first
// segment that really is a directory in this project also qualifies.
func TestLooksLikeFilePathAcceptsRealProjectDirs(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"services", "packages"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	tests := []struct {
		name string
		s    string
		want bool
	}{
		{"unconventional dir that exists", "services/billing/handler.go", true},
		{"another one", "packages/ui/index.ts", true},
		{"unconventional dir that doesn't exist", "widgets/thing.go", false},
		// A conventional prefix qualifies whether or not the directory exists —
		// that's the point, since a reference into a missing lib/ is stale.
		{"conventional dir that doesn't exist", "lib/app/gone.ex", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeFilePath(tt.s, root); got != tt.want {
				t.Errorf("looksLikeFilePath(%q) = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}

// Dot-directories hold state that rivet creates on demand, so docs legitimately
// reference paths inside them before they exist. Flagging those is noise.
func TestLooksLikeFilePathIgnoresDotDirs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rivet"), 0755); err != nil {
		t.Fatalf("mkdir .rivet: %v", err)
	}

	if looksLikeFilePath(".rivet/embeddings/", root) {
		t.Error("a path inside a dot-directory should not be treated as a source reference")
	}
}

// Docs describe families of files with globs and placeholders. Resolving those
// literally reported rot that did not exist — a real corpus had five such
// "stale" references, four of whose directories were present and correct.
func TestLooksLikeFilePathIgnoresGlobsAndPlaceholders(t *testing.T) {
	root := t.TempDir()
	for _, s := range []string{
		"database/migrations/*.sql",
		"environment/.env.<env>",
		"qa/workbench/jstests/<slug>.mjs",
		"src/**/handler.go",
		"lib/app/[id].ex",
	} {
		if looksLikeFilePath(s, root) {
			t.Errorf("%q is a pattern, not a path to resolve", s)
		}
	}
	// A concrete path must still be checked.
	if !looksLikeFilePath("lib/app/gone.ex", root) {
		t.Error("a literal path should still be treated as a reference")
	}
}
