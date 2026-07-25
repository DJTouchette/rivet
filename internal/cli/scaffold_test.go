package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rivetctx "github.com/djtouchette/rivet/internal/context"
)

// Bug: the scaffolder gated stack.md on a non-empty framework list. Recon's
// frameworks are now evidence-backed rather than a manifest dump, so a repo that
// used to get a stack doc can now get none — with nothing printed and nothing
// written to say why. Every status must produce the doc, and every empty list
// must say which kind of empty it is.
func TestBuildFrameworkDocAlwaysWritesAndExplainsEmptiness(t *testing.T) {
	cases := []struct {
		name        string
		overview    reconOverview
		wantInDoc   []string
		notInDoc    []string
		wantNote    bool
		noteMustSay string
	}{
		{
			name: "found lists the frameworks and says nothing extra",
			overview: reconOverview{
				Frameworks: []reconFramework{
					{Name: "Cobra", Language: "go", Evidence: "go.mod: require github.com/spf13/cobra"},
					{Name: "testify", Language: "go", Evidence: "go.mod: require github.com/stretchr/testify"},
				},
				FrameworkStatus: detectStatusFound,
			},
			wantInDoc: []string{"**Frameworks:** Cobra, testify"},
			notInDoc:  []string{"undetermined", "none matched"},
			wantNote:  false,
		},
		{
			name:     "none_matched says recon looked and found nothing",
			overview: reconOverview{FrameworkStatus: detectStatusNoneMatched},
			// The distinguishing fact is that recon had rules and they didn't fire.
			wantInDoc:   []string{"none matched", "nothing proved a framework"},
			notInDoc:    []string{"no framework detector"},
			wantNote:    true,
			noteMustSay: "matched no framework",
		},
		{
			name:        "unsupported says recon had no rules at all",
			overview:    reconOverview{FrameworkStatus: detectStatusUnsupported},
			wantInDoc:   []string{"undetermined", "no framework detector"},
			notInDoc:    []string{"none matched"},
			wantNote:    true,
			noteMustSay: "no framework detector",
		},
		{
			// rivet embeds a pinned recon; an older one omits the field entirely.
			name:        "absent status is reported as ambiguous, not as none",
			overview:    reconOverview{},
			wantInDoc:   []string{"undetermined", "does not say whether"},
			wantNote:    true,
			noteMustSay: "no status",
		},
		{
			// Recon may add statuses (a detector that errored, say). An unknown
			// value must degrade to "undetermined" and be quoted verbatim rather
			// than fall through to the optimistic reading.
			name:        "unknown status is reported verbatim and treated as undetermined",
			overview:    reconOverview{FrameworkStatus: "errored"},
			wantInDoc:   []string{"undetermined", `"errored"`, "does not recognise"},
			notInDoc:    []string{"none matched"},
			wantNote:    true,
			noteMustSay: `"errored"`,
		},
		{
			name: "frameworks alongside a non-found status are not presented as complete",
			overview: reconOverview{
				Frameworks:      []reconFramework{{Name: "Flask", Language: "python"}},
				FrameworkStatus: "partial",
			},
			wantInDoc:   []string{"Flask", `recon status "partial"`, "may be incomplete"},
			wantNote:    true,
			noteMustSay: "incomplete",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := buildFrameworkDoc(c.overview, "go")

			if doc.path != filepath.Join(".rivet", "context", "paradigms", "stack.md") {
				t.Fatalf("unexpected path %q", doc.path)
			}
			for _, want := range c.wantInDoc {
				if !strings.Contains(doc.content, want) {
					t.Errorf("doc does not mention %q:\n%s", want, doc.content)
				}
			}
			for _, notWant := range c.notInDoc {
				if strings.Contains(doc.content, notWant) {
					t.Errorf("doc should not mention %q:\n%s", notWant, doc.content)
				}
			}
			if gotNote := doc.note != ""; gotNote != c.wantNote {
				t.Errorf("note present = %v, want %v (note: %q)", gotNote, c.wantNote, doc.note)
			}
			if c.noteMustSay != "" && !strings.Contains(doc.note, c.noteMustSay) {
				t.Errorf("note %q does not mention %q", doc.note, c.noteMustSay)
			}
		})
	}
}

// Tags feed retrieval scoring, and internal/context matches a tag by substring
// in either direction. Dependency names must therefore stay out of tags: a tag
// per manifest entry would give this one doc partial credit on unrelated queries
// ("auth" landing on oauthlib). Frameworks are display names and belong there.
func TestBuildFrameworkDocTags(t *testing.T) {
	ov := reconOverview{
		Frameworks: []reconFramework{
			{Name: "Spring Boot", Language: "java"},
			{Name: "Next.js", Language: "typescript"},
		},
		Dependencies: []reconDependency{
			{Name: "oauthlib", Version: "3.2.2", Manifest: "requirements.txt"},
			{Name: "@types/node", Version: "20.1.0", Manifest: "package.json"},
		},
		FrameworkStatus: detectStatusFound,
	}

	tags := tagsFromDoc(t, buildFrameworkDoc(ov, "java").content)

	for _, want := range []string{"java", "stack", "frameworks", "conventions", "spring-boot", "next.js"} {
		if !hasTag(tags, want) {
			t.Errorf("tags %v missing %q", tags, want)
		}
	}
	for _, notWant := range []string{"oauthlib", "@types/node"} {
		if hasTag(tags, notWant) {
			t.Errorf("dependency %q leaked into tags %v", notWant, tags)
		}
	}
}

// An empty language or framework list used to render as `tags: [go, ]`, i.e. an
// empty tag the frontmatter parser reads as real.
func TestBuildFrameworkDocTagsNeverEmptyOrDuplicated(t *testing.T) {
	ov := reconOverview{
		Frameworks:      []reconFramework{{Name: "Stack"}, {Name: "Stack"}},
		FrameworkStatus: detectStatusFound,
	}
	// No primary language: recon reported no languages at all.
	content := buildFrameworkDoc(ov, "").content

	if strings.Contains(content, "**Language:** \n") {
		t.Error("empty language rendered as a field")
	}
	tags := tagsFromDoc(t, content)
	seen := map[string]bool{}
	for _, tag := range tags {
		if strings.TrimSpace(tag) == "" {
			t.Fatalf("empty tag in %v", tags)
		}
		if seen[tag] {
			t.Errorf("duplicate tag %q in %v", tag, tags)
		}
		seen[tag] = true
	}
}

// The manifest list is what recon used to report as "frameworks". It stays in the
// doc — as dependencies — so the human filling the doc in still has the facts,
// and so an empty framework list next to a 200-entry manifest reads as a
// deliberate answer rather than a failure.
func TestBuildFrameworkDocDependencySection(t *testing.T) {
	var deps []reconDependency
	for i := 0; i < dependencyDisplayLimit+7; i++ {
		deps = append(deps, reconDependency{
			Name:     "dep" + string(rune('a'+i)),
			Version:  "1.0.0",
			Manifest: "pom.xml",
		})
	}
	ov := reconOverview{Dependencies: deps, FrameworkStatus: detectStatusNoneMatched}

	content := buildFrameworkDoc(ov, "java").content

	if !strings.Contains(content, "## Declared dependencies") {
		t.Fatalf("no dependency section:\n%s", content)
	}
	if !strings.Contains(content, "recon read 17 from pom.xml") {
		t.Errorf("dependency count/manifest not reported:\n%s", content)
	}
	if !strings.Contains(content, "- ... and 7 more") {
		t.Errorf("truncation not reported:\n%s", content)
	}
	if strings.Count(content, "- dep") != dependencyDisplayLimit {
		t.Errorf("listed %d deps, want %d", strings.Count(content, "- dep"), dependencyDisplayLimit)
	}
	// Dependencies are not frameworks, however many there are.
	if !strings.Contains(content, "**Frameworks:** none matched") {
		t.Errorf("a full manifest must not imply a framework:\n%s", content)
	}

	// With no dependencies the section is absent rather than empty.
	if strings.Contains(buildFrameworkDoc(reconOverview{}, "go").content, "Declared dependencies") {
		t.Error("dependency section written with no dependencies")
	}
}

// `rivet context lint --strict` gates CI, so a doc the scaffolder writes must not
// arrive with an error-severity problem or an untagged theme it could have
// tagged. Placeholder warnings are expected — they are the point of a scaffold.
func TestScaffoldedStackDocPassesLintExceptPlaceholders(t *testing.T) {
	statuses := []string{detectStatusFound, detectStatusNoneMatched, detectStatusUnsupported, "", "something_new"}

	for _, status := range statuses {
		t.Run("status="+status, func(t *testing.T) {
			ov := reconOverview{FrameworkStatus: status}
			if status == detectStatusFound {
				ov.Frameworks = []reconFramework{{Name: "Phoenix", Language: "elixir"}}
			}
			ov.Dependencies = []reconDependency{{Name: "jason", Version: "1.4.1", Manifest: "mix.exs"}}

			root := t.TempDir()
			doc := buildFrameworkDoc(ov, "elixir")
			path := filepath.Join(root, doc.path)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(doc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			// mix.exs is named in the body; a missing file would be a
			// stale-reference warning about the fixture, not about the template.
			if err := os.WriteFile(filepath.Join(root, "mix.exs"), []byte("defmodule X do end\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			docs, err := rivetctx.Load(filepath.Join(root, ".rivet", "context"))
			if err != nil {
				t.Fatal(err)
			}
			if len(docs) != 1 {
				t.Fatalf("loaded %d docs, want 1", len(docs))
			}

			for _, w := range rivetctx.Lint(docs, root).Warnings {
				switch w.Rule {
				case "placeholder-section", "missing-owner", "missing-review", "missing-related-paths":
					// Expected of any scaffold: the human fills these in.
				default:
					t.Errorf("unexpected lint %s (%s): %s", w.Rule, w.Severity, w.Message)
				}
			}
		})
	}
}

// The status and dependency fields must survive a real decode, and a payload
// from an older recon that omits them must still parse.
func TestReconOverviewDecode(t *testing.T) {
	current := `{
	  "root": "/repo",
	  "languages": [{"name": "python", "file_count": 12, "extensions": [".py"]}],
	  "frameworks": [{"name": "Flask", "language": "python", "evidence": "app.py: from flask import Flask"}],
	  "dependencies": [{"name": "flask", "version": "3.0.0", "language": "python", "manifest": "requirements.txt"}],
	  "framework_status": "found",
	  "entrypoint_status": "none_matched"
	}`

	var ov reconOverview
	if err := json.Unmarshal([]byte(current), &ov); err != nil {
		t.Fatal(err)
	}
	if ov.FrameworkStatus != detectStatusFound {
		t.Errorf("framework_status = %q", ov.FrameworkStatus)
	}
	if len(ov.Frameworks) != 1 || ov.Frameworks[0].Evidence == "" {
		t.Errorf("frameworks decoded as %+v — evidence is the proof, keep it", ov.Frameworks)
	}
	if len(ov.Dependencies) != 1 || ov.Dependencies[0].Manifest != "requirements.txt" {
		t.Errorf("dependencies decoded as %+v", ov.Dependencies)
	}

	legacy := `{"root": "/repo", "languages": [], "frameworks": [], "structure": [], "entrypoints": []}`
	var old reconOverview
	if err := json.Unmarshal([]byte(legacy), &old); err != nil {
		t.Fatalf("older recon payload must still decode: %v", err)
	}
	if old.FrameworkStatus != "" {
		t.Errorf("absent status decoded as %q, want empty", old.FrameworkStatus)
	}
}

func tagsFromDoc(t *testing.T, content string) []string {
	t.Helper()
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, "tags: [") {
			continue
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(line, "tags: ["), "]")
		var tags []string
		for _, part := range strings.Split(inner, ",") {
			tags = append(tags, strings.TrimSpace(part))
		}
		return tags
	}
	t.Fatalf("no tags line in:\n%s", content)
	return nil
}

func hasTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

// End-to-end against the embedded recon: a repo with source files but nothing
// recon recognises as a framework must still get a stack doc. rivet pins its
// recon build, so this is also the pinned-version path — whatever that build
// reports about framework status, a doc is produced.
func TestScaffoldWritesStackDocForAnUnrecognisedStack(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if err := os.MkdirAll(filepath.Join(dir, ".rivet"), 0o755); err != nil {
		t.Fatal(err)
	}
	// No manifest and no recognised framework marker: just source.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	docs, err := scaffold()
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	stackPath := filepath.Join(".rivet", "context", "paradigms", "stack.md")
	var stack *scaffoldDoc
	for i := range docs {
		if docs[i].path == stackPath {
			stack = &docs[i]
		}
	}
	if stack == nil {
		t.Fatalf("no stack doc scaffolded; got %d docs", len(docs))
	}
	if !strings.Contains(stack.content, "**Frameworks:**") {
		t.Errorf("stack doc has no frameworks line:\n%s", stack.content)
	}
	if len(stack.content) == 0 {
		t.Error("empty stack doc")
	}
}
