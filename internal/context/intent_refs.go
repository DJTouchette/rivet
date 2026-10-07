package context

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Code points at the rule it enforces with a marker comment, in any language:
//
//	// rivet:intent <ID>
//	# rivet:intent <ID>, <ID>
//	/* rivet:intent: <ID> */
//
// where <ID> is a rule ID such as BIL-001.
//
// A marker in a test file says the test verifies the rule; anywhere else it
// says the code enforces it. Only explicit markers count. A bare "BIL-001" in a
// string or a log line is not a claim that the rule is enforced there, and
// treating it as one would let incidental mentions satisfy the coverage check.

// RuleRef is one rule ID named by a `rivet:intent` marker.
type RuleRef struct {
	ID   string `json:"id"`
	File string `json:"file"` // slash-separated, relative to the project root
	Line int    `json:"line"`
	Test bool   `json:"test"` // the marker is in a test file
}

// intentMarkerRe captures the ID list after a marker. The list is
// comma-separated; anything after the last ID is ignored, so a trailing
// explanation ("rivet:intent <ID> — rejects edits after issue") is fine.
var intentMarkerRe = regexp.MustCompile(`rivet:intent[\s:]+(` + ruleIDPattern + `(?:\s*,\s*` + ruleIDPattern + `)*)`)

// intentMarkerERE is the POSIX ERE handed to git grep as a coarse prefilter;
// intentMarkerRe makes the real decision on each line it returns.
const intentMarkerERE = `rivet:intent[[:space:]:]+[A-Z]`

// defaultIntentExcludes are never scanned. .rivet/ holds the intent docs
// themselves and talks about the marker syntax; testdata/ is Go's convention
// for fixtures (including fixture repos full of markers for rules this project
// doesn't define); dependencies are someone else's code.
var defaultIntentExcludes = []string{
	".rivet/**",
	"**/testdata/**",
	"**/node_modules/**",
	"**/vendor/**",
}

// ScanOptions configures where markers are looked for.
type ScanOptions struct {
	// Exclude are extra globs (slash paths relative to the root) to skip, on top
	// of defaultIntentExcludes. From config: intent.exclude.
	Exclude []string
}

func (o ScanOptions) excludes() []string {
	return append(append([]string{}, defaultIntentExcludes...), o.Exclude...)
}

// ScanRuleRefs finds every `rivet:intent` marker in the working tree under
// root — tracked files plus untracked files git isn't ignoring. Outside a git
// repository it falls back to walking the directory.
func ScanRuleRefs(root string, opts ScanOptions) ([]RuleRef, error) {
	if isGitRepo(root) {
		return gitGrepRefs(root, opts, "--untracked")
	}
	return walkRefs(root, opts)
}

// ScanRuleRefsAt finds markers as they were at a git revision. rev may be a
// commit-ish, or "" with cached=true to read the index (the staged state).
func ScanRuleRefsAt(root, rev string, cached bool, opts ScanOptions) ([]RuleRef, error) {
	if cached {
		return gitGrepRefs(root, opts, "--cached")
	}
	return gitGrepRefs(root, opts, rev)
}

func isGitRepo(root string) bool {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// gitGrepRefs runs git grep for markers. source is "--untracked" (working
// tree), "--cached" (index), or a revision.
func gitGrepRefs(root string, opts ScanOptions, source string) ([]RuleRef, error) {
	isRev := source != "--untracked" && source != "--cached"
	args := []string{"-C", root, "grep", "-I", "-n", "-z"}
	if isRev {
		args = append(args, "-E", intentMarkerERE, source)
	} else {
		args = append(args, source, "-E", intentMarkerERE)
	}
	args = append(args, "--", ".")
	for _, ex := range opts.excludes() {
		args = append(args, ":(exclude,glob)"+ex)
	}

	cmd := exec.Command("git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		// Exit 1 with no stderr is git grep's "no matches".
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && stderr.Len() == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("git grep for rivet:intent markers: %v: %s", err, strings.TrimSpace(stderr.String()))
	}

	var refs []RuleRef
	for _, rec := range strings.Split(string(out), "\n") {
		if rec == "" {
			continue
		}
		// -z output: <file>\0<line>\0<content>, file prefixed "<rev>:" for a rev.
		parts := strings.SplitN(rec, "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		file := parts[0]
		if isRev {
			file = strings.TrimPrefix(file, source+":")
		}
		if skipMarkerFile(file) {
			continue
		}
		line, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		refs = append(refs, refsOnLine(file, line, parts[2])...)
	}
	sortRefs(refs)
	return refs, nil
}

// walkRefs is the non-git fallback.
func walkRefs(root string, opts ScanOptions) ([]RuleRef, error) {
	excludes := opts.excludes()
	var refs []RuleRef
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if p == root {
				return nil
			}
			if d.Name() == ".git" || excluded(rel+"/x", excludes) {
				return fs.SkipDir
			}
			return nil
		}
		if excluded(rel, excludes) || skipMarkerFile(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 4<<20 {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
			return nil // binary
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
		n := 0
		for sc.Scan() {
			n++
			if text := sc.Text(); strings.Contains(text, "rivet:intent") {
				refs = append(refs, refsOnLine(rel, n, text)...)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning for rivet:intent markers: %w", err)
	}
	sortRefs(refs)
	return refs, nil
}

func excluded(rel string, globs []string) bool {
	for _, g := range globs {
		if governsPath(g, rel) {
			return true
		}
	}
	return false
}

// skipMarkerFile drops prose files. A README that mentions a rule documents
// it; it does not enforce it, and counting it would let documentation satisfy
// the coverage check.
func skipMarkerFile(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".md", ".mdx", ".markdown", ".rst", ".adoc":
		return true
	}
	return false
}

func refsOnLine(file string, line int, text string) []RuleRef {
	var refs []RuleRef
	test := IsTestFile(file)
	for _, m := range intentMarkerRe.FindAllStringSubmatch(text, -1) {
		for _, id := range strings.Split(m[1], ",") {
			refs = append(refs, RuleRef{ID: strings.TrimSpace(id), File: file, Line: line, Test: test})
		}
	}
	return refs
}

func sortRefs(refs []RuleRef) {
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].File != refs[j].File {
			return refs[i].File < refs[j].File
		}
		if refs[i].Line != refs[j].Line {
			return refs[i].Line < refs[j].Line
		}
		return refs[i].ID < refs[j].ID
	})
}

// testDirs are path segments that mark everything beneath them as tests.
var testDirs = map[string]bool{
	"test": true, "tests": true, "__tests__": true, "spec": true, "specs": true,
	"e2e": true, "integration_tests": true, "testing": true,
}

// IsTestFile reports whether a slash path is a test by the naming conventions
// of the common ecosystems: Go, JS/TS, Python, Ruby, Elixir, Rust, JVM and .NET.
// CamelCase suffixes are matched case-sensitively so Latest.cs is not a test.
func IsTestFile(rel string) bool {
	rel = filepath.ToSlash(rel)
	base := path.Base(rel)
	lower := strings.ToLower(base)

	for _, s := range []string{"_test.go", "_test.py", "_test.exs", "_spec.rb", "_test.rb", "_test.rs", "_test.dart", "_test.ts", "_test.js"} {
		if strings.HasSuffix(lower, s) {
			return true
		}
	}
	if strings.Contains(lower, ".test.") || strings.Contains(lower, ".spec.") || strings.HasPrefix(lower, "test_") {
		return true
	}
	stem := strings.TrimSuffix(base, path.Ext(base))
	for _, s := range []string{"Test", "Tests", "Spec", "Specs", "IT"} {
		if strings.HasSuffix(stem, s) && len(stem) > len(s) {
			switch strings.ToLower(path.Ext(base)) {
			case ".java", ".kt", ".scala", ".cs", ".fs", ".vb", ".swift", ".groovy":
				return true
			}
		}
	}

	dirs := strings.Split(path.Dir(rel), "/")
	for _, d := range dirs {
		ld := strings.ToLower(d)
		if testDirs[ld] {
			return true
		}
		// .NET test projects: Acme.Billing.Tests/, Acme.Billing.UnitTests/
		if strings.Contains(ld, ".") && (strings.HasSuffix(ld, "tests") || strings.HasSuffix(ld, ".test")) {
			return true
		}
	}
	return false
}
