package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/djtouchette/rivet/internal/provider"
	"gopkg.in/yaml.v3"
)

// The codex half of the golden set. Same fixture, same code paths, different
// provider. See goldens_test.go for how the goldens are compared and why the
// fixture is pinned the way it is.

func TestCodexArtifactGoldens(t *testing.T) {
	goldenDir := absFromTestdata(t, "golden")
	root := buildFixtureProject(t, provider.Codex())

	for _, a := range codexArtifacts {
		got, err := os.ReadFile(a.path)
		if err != nil {
			t.Errorf("rivet did not generate %s: %v", a.path, err)
			continue
		}
		assertNoMachinePaths(t, a.path, got, root)
		compareGolden(t, filepath.Join(goldenDir, a.golden), got)
	}
}

// TestCodexArtifactSetGolden pins which files exist after a codex setup. It is
// also where the two deliberate omissions show up: no .mcp.json, because codex
// registers MCP servers globally in its own config.toml, and no subagent
// briefs, because rivet does not ship codex subagents yet.
func TestCodexArtifactSetGolden(t *testing.T) {
	goldenDir := absFromTestdata(t, "golden")
	root := buildFixtureProject(t, provider.Codex())

	var b strings.Builder
	for _, p := range projectFiles(t, root) {
		b.WriteString(p)
		b.WriteString("\n")
	}
	compareGolden(t, filepath.Join(goldenDir, "artifact-tree-codex.golden"), []byte(b.String()))
}

// TestCodexMCPTOMLGolden pins the table rivet tells you to paste when codex is
// not on PATH. TestCodexMCPConfigRoundTrip in internal/provider is what stops
// this golden from freezing bytes codex cannot read.
func TestCodexMCPTOMLGolden(t *testing.T) {
	goldenDir := absFromTestdata(t, "golden")
	compareGolden(t, filepath.Join(goldenDir, "codex-mcp.toml.golden"), []byte(provider.CodexMCPTOML))
}

// codexProjectDocMaxBytes is codex's default project_doc_max_bytes. An
// AGENTS.md past this limit is truncated, and a truncated instruction file
// fails silently: the agent reads a prefix and never learns that the rest
// existed. The generated block grows with the number of capabilities and
// context docs, so this needs an assertion rather than an assumption.
const codexProjectDocMaxBytes = 32768

func TestAgentsMDFitsCodexProjectDocLimit(t *testing.T) {
	buildFixtureProject(t, provider.Codex())

	got, err := os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatalf("reading AGENTS.md: %v", err)
	}
	if len(got) >= codexProjectDocMaxBytes {
		t.Errorf("AGENTS.md is %d bytes, at or past codex's project_doc_max_bytes default of %d; codex would truncate it",
			len(got), codexProjectDocMaxBytes)
	}
}

// codexSkillFrontmatter is the schema codex enforces on a SKILL.md, read from
// the validator codex bundles at $CODEX_HOME/skills/.system/skill-creator/
// scripts/quick_validate.py. Rivet's skill bodies are hand-written constants,
// so nothing else would catch a name or description that codex rejects.
var codexSkillAllowedKeys = map[string]bool{
	"name":          true,
	"description":   true,
	"license":       true,
	"allowed-tools": true,
	"metadata":      true,
}

var codexSkillNamePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

func TestCodexSkillFrontmatterMatchesCodexSchema(t *testing.T) {
	buildFixtureProject(t, provider.Codex())

	for _, a := range codexArtifacts {
		if filepath.Base(a.path) != "SKILL.md" {
			continue
		}

		raw, err := os.ReadFile(a.path)
		if err != nil {
			t.Errorf("reading %s: %v", a.path, err)
			continue
		}
		checkCodexSkill(t, a.path, string(raw))
	}
}

func checkCodexSkill(t *testing.T, path, content string) {
	t.Helper()

	if !strings.HasPrefix(content, "---\n") {
		t.Errorf("%s: no YAML frontmatter", path)
		return
	}
	end := strings.Index(content[4:], "\n---\n")
	if end < 0 {
		t.Errorf("%s: frontmatter is not terminated", path)
		return
	}

	var fm map[string]interface{}
	if err := yaml.Unmarshal([]byte(content[4:4+end]), &fm); err != nil {
		t.Errorf("%s: frontmatter is not valid YAML: %v", path, err)
		return
	}

	for k := range fm {
		if !codexSkillAllowedKeys[k] {
			t.Errorf("%s: frontmatter key %q is not one codex allows", path, k)
		}
	}

	name, _ := fm["name"].(string)
	if name == "" {
		t.Errorf("%s: frontmatter has no name", path)
	} else {
		if !codexSkillNamePattern.MatchString(name) {
			t.Errorf("%s: name %q is not hyphen-case", path, name)
		}
		if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") || strings.Contains(name, "--") {
			t.Errorf("%s: name %q starts, ends, or doubles a hyphen", path, name)
		}
		if len(name) > 64 {
			t.Errorf("%s: name is %d characters, over codex's limit of 64", path, len(name))
		}
		if want := filepath.Base(filepath.Dir(path)); name != want {
			t.Errorf("%s: name is %q but the skill directory is %q", path, name, want)
		}
	}

	desc, _ := fm["description"].(string)
	if desc == "" {
		t.Errorf("%s: frontmatter has no description", path)
		return
	}
	if strings.ContainsAny(desc, "<>") {
		t.Errorf("%s: description contains an angle bracket, which codex rejects", path)
	}
	if len(desc) > 1024 {
		t.Errorf("%s: description is %d characters, over codex's limit of 1024", path, len(desc))
	}
}
