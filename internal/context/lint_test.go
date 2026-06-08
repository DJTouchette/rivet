package context

import (
	"os"
	"path/filepath"
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
		Body: `# Billing

- ` + "`lib/billing/invoice.ex`" + ` — handles invoices
- ` + "`lib/billing/deleted_file.ex`" + ` — this file was removed
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

	oldStale, missingStale := false, false
	for _, w := range result.Warnings {
		if w.Rule != "stale-review" {
			continue
		}
		switch w.Document {
		case "billing":
			oldStale = true
		case "auth":
			missingStale = true
		}
	}
	if !oldStale {
		t.Error("expected stale-review warning for old billing doc")
	}
	if !missingStale {
		t.Error("expected stale-review warning for auth doc missing last_reviewed")
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

	for _, tt := range tests {
		got := extractBacktickPaths(tt.line)
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

	for _, tt := range tests {
		got := looksLikeFilePath(tt.s)
		if got != tt.want {
			t.Errorf("looksLikeFilePath(%q) = %v, want %v", tt.s, got, tt.want)
		}
	}
}
