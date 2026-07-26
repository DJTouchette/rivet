package recon

import (
	"os"
	"path/filepath"
	"testing"
)

// The one-cache invariant rests entirely on this path being absolute.
//
// Both adapters inject `--cache-dir <CacheDir()>`, but the two embedded tools
// do not resolve a RELATIVE one the same way: recon resolves it against the
// process's working directory, while witness from v0.5.0 resolves it against
// the git repository root. Run rivet from a subdirectory with a relative path
// and the same string names two different directories, so recon indexes one
// cache and witness builds a second full index next to it — the opposite of
// "a project gets one".
func TestCacheDirIsAbsolute(t *testing.T) {
	repo := t.TempDir()
	sub := filepath.Join(repo, "sub", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Chdir(sub)

	dir := CacheDir()
	if !filepath.IsAbs(dir) {
		t.Fatalf("CacheDir() = %q, want an absolute path: a relative one is resolved against the cwd by recon and against the repo root by witness, which is two caches", dir)
	}

	// Anchored at the working directory, which is where recon analyses. Pinning
	// it to the repository root instead would point one cache at two different
	// analyses when rivet is run from a subdirectory.
	want := filepath.Join(sub, ".rivet", "recon")
	if dir != want {
		t.Errorf("CacheDir() = %q, want %q", dir, want)
	}
}
