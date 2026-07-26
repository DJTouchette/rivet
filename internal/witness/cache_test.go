package witness

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/djtouchette/rivet/internal/recon"
)

// "recon and witness share one cache" is asserted as a design guarantee in
// .rivet/context/modules/tool-embedding.md, and it is the reason witness's
// dependency-graph scoring is free: the index is already there.
//
// It is not free to keep. The two adapters inject the same --cache-dir string,
// but the tools behind them resolve a RELATIVE one differently — recon against
// the process's working directory, witness (v0.5.0 onward) against the git
// repository root. Run rivet from a subdirectory with a relative path and the
// guarantee silently becomes two full indexes: recon's under the subdirectory,
// witness's at the root. recon.CacheDir() is absolute so that cannot happen,
// and this is the end-to-end check that it did not.
func TestAdaptersShareOneCacheFromASubdirectory(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "go.mod", "module scratch\n\ngo 1.25\n")
	writeFile(t, repo, "calc/calc.go", "package calc\n\nfunc Add(a, b int) int { return a + b }\n")
	writeFile(t, repo, "calc/calc_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fail()\n\t}\n}\n")
	writeFile(t, repo, "sub/deep/note.md", "scratch\n")
	commit(t, repo)
	writeFile(t, repo, "calc/calc.go", "package calc\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Sub(a, b int) int { return a - b }\n")

	t.Chdir(filepath.Join(repo, "sub", "deep"))

	if _, _, _, err := recon.Run([]string{"overview"}); err != nil {
		t.Fatalf("recon overview: %v", err)
	}
	if _, _, _, err := Run([]string{"select", "--format", "paths"}); err != nil {
		t.Fatalf("witness select: %v", err)
	}

	caches := findCaches(t, repo)
	if len(caches) == 0 {
		t.Fatal("neither adapter created a cache, so there is nothing to assert about sharing one")
	}
	if len(caches) > 1 {
		t.Errorf("the adapters built %d caches, want 1 — they no longer agree on where it lives:\n  %s",
			len(caches), strings.Join(caches, "\n  "))
	}
}

// findCaches returns every .rivet/recon directory under root, repo-relative and
// sorted.
func findCaches(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || d.Name() != "recon" || filepath.Base(filepath.Dir(p)) != ".rivet" {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		found = append(found, rel)
		return fs.SkipDir
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	sort.Strings(found)
	return found
}
