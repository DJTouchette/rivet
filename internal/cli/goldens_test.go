package cli

import (
	"bytes"
	"flag"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// These goldens pin the exact bytes of every file rivet installs for Claude
// Code. They exist so that a refactor has to prove it did not change what the
// agent reads: the CLAUDE.md block, .mcp.json, the four skills and the two
// subagents. An assertion on a handful of substrings would not catch a mangled
// table, a wrong heading level, a doubled marker, or a file written to the
// wrong path. Byte equality does.
//
// Regenerate after a deliberate change, then read the diff before committing:
//
//	go test ./internal/cli -run Golden -update
//
// DETERMINISM. Three inputs would otherwise vary per machine and make these
// flap. All three are pinned in the fixture rather than by loosening the
// comparison:
//
//   - The vaulty.* and schema.* capability groups are auto-detected, and vaulty
//     detection stats the developer's home directory. The fixture config sets
//     tools.schema and tools.vaulty to false, so the capability list is a
//     property of the fixture alone.
//   - config.LoadOrDefault falls back to ~/.config/rivet/config.yaml when a
//     project has no config. The fixture ships one, and HOME is redirected at
//     an empty temp directory so the fallback cannot be reached even if it did
//     not.
//   - The project root is a temp directory whose name changes every run.
//     Nothing rivet generates should embed it, and assertNoMachinePaths fails
//     loudly if that ever stops being true.
//
// Map iteration order is already handled upstream: Registry.List sorts by name,
// context.Load sorts by kind then name, and encoding/json sorts map keys.

var updateGoldens = flag.Bool("update", false, "rewrite the golden files under testdata/golden")

// claudeArtifact is one generated file and the golden that pins its bytes.
// path is relative to the project root, spelled the way rivet writes it.
type claudeArtifact struct {
	path   string
	golden string
}

var claudeArtifacts = []claudeArtifact{
	{"CLAUDE.md", "CLAUDE.md.golden"},
	{".mcp.json", "mcp.json.golden"},
	{filepath.Join(".claude", "skills", "rivet-setup", "SKILL.md"), "skill-rivet-setup.md.golden"},
	{filepath.Join(".claude", "skills", "rivet-fill-context", "SKILL.md"), "skill-rivet-fill-context.md.golden"},
	{filepath.Join(".claude", "skills", "rivet-compact-context", "SKILL.md"), "skill-rivet-compact-context.md.golden"},
	{filepath.Join(".claude", "skills", "rivet-promote-learnings", "SKILL.md"), "skill-rivet-promote-learnings.md.golden"},
	{filepath.Join(".claude", "agents", "rivet-explorer.md"), "agent-rivet-explorer.md.golden"},
	{filepath.Join(".claude", "agents", "rivet-investigator.md"), "agent-rivet-investigator.md.golden"},
}

func TestClaudeArtifactGoldens(t *testing.T) {
	goldenDir := absFromTestdata(t, "golden")
	root := buildFixtureProject(t)

	for _, a := range claudeArtifacts {
		got, err := os.ReadFile(a.path)
		if err != nil {
			t.Errorf("rivet did not generate %s: %v", a.path, err)
			continue
		}
		assertNoMachinePaths(t, a.path, got, root)
		compareGolden(t, filepath.Join(goldenDir, a.golden), got)
	}
}

// TestClaudeArtifactSetGolden pins which files exist in the project after
// setup, not just what is in them. Without it, an artifact could stop being
// written entirely and every other golden would still pass. The listing
// includes the fixture's own files, which is deliberate: it puts the inputs
// and the outputs in one reviewable place.
func TestClaudeArtifactSetGolden(t *testing.T) {
	goldenDir := absFromTestdata(t, "golden")
	root := buildFixtureProject(t)

	var b strings.Builder
	for _, p := range projectFiles(t, root) {
		b.WriteString(p)
		b.WriteString("\n")
	}
	compareGolden(t, filepath.Join(goldenDir, "artifact-tree.golden"), []byte(b.String()))
}

// buildFixtureProject copies testdata/fixture into a temp directory, makes it
// the working directory, and runs the two code paths that produce everything
// rivet installs for Claude: ensureProjectSetup (the shared body of `rivet
// init` and `rivet update`) and the `rivet sync` command. It returns the
// project root.
func buildFixtureProject(t *testing.T) string {
	t.Helper()

	fixture := absFromTestdata(t, "fixture")
	root := t.TempDir()
	copyTree(t, fixture, root)

	// An empty HOME, so no user-level rivet config or vaulty store can leak
	// into the generated output.
	t.Setenv("HOME", t.TempDir())
	t.Chdir(root)

	if _, err := ensureProjectSetup(false); err != nil {
		t.Fatalf("ensureProjectSetup: %v", err)
	}

	cmd := newSyncCmd()
	// An empty slice, not nil: cobra falls back to os.Args when SetArgs gets
	// nil, and the test binary's own flags are not this command's.
	cmd.SetArgs([]string{})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rivet sync: %v", err)
	}

	return root
}

func compareGolden(t *testing.T, path string, got []byte) {
	t.Helper()

	if *updateGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, got, 0644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v\nrun: go test ./internal/cli -run Golden -update", path, err)
	}
	if bytes.Equal(got, want) {
		return
	}
	t.Errorf("%s is out of date.\n%s\nIf the change is intended, run: go test ./internal/cli -run Golden -update",
		filepath.Base(path), firstDifference(string(want), string(got)))
}

// firstDifference reports the first line that differs, which is enough to
// recognise an intended change from an accidental one without printing two
// multi-kilobyte files.
func firstDifference(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")

	for i := 0; i < len(wantLines) && i < len(gotLines); i++ {
		if wantLines[i] != gotLines[i] {
			return "line " + strconv.Itoa(i+1) + ":\n  want: " + wantLines[i] + "\n  got:  " + gotLines[i]
		}
	}
	if len(gotLines) > len(wantLines) {
		return "extra line " + strconv.Itoa(len(wantLines)+1) + ":\n  got:  " + gotLines[len(wantLines)]
	}
	return "missing line " + strconv.Itoa(len(gotLines)+1) + ":\n  want: " + wantLines[len(gotLines)]
}

// assertNoMachinePaths fails if a generated artifact embeds the project root.
// The root is a temp directory here and someone's checkout in real use, so a
// path leaking into the output is a bug in the generator, not something to
// paper over in the golden.
func assertNoMachinePaths(t *testing.T, name string, content []byte, root string) {
	t.Helper()

	if strings.Contains(string(content), root) {
		t.Errorf("%s embeds the absolute project path %q; generated artifacts must be path-independent", name, root)
	}
}

// projectFiles lists every file under dir, as slash-separated relative paths,
// sorted. Directories are omitted; an empty directory carries nothing an agent
// can read.
func projectFiles(t *testing.T, dir string) []string {
	t.Helper()

	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	sort.Strings(out)
	return out
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()

	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644)
	})
	if err != nil {
		t.Fatalf("copying %s to %s: %v", src, dst, err)
	}
}

// absFromTestdata resolves a testdata subdirectory to an absolute path. The
// tests chdir into a temp project root, so every testdata path has to be
// resolved before that happens.
func absFromTestdata(t *testing.T, name string) string {
	t.Helper()

	abs, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("resolving testdata/%s: %v", name, err)
	}
	return abs
}
