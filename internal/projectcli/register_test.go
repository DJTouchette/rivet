package projectcli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The generated discover command reports os.Executable(), which is already
// symlink-resolved, while register-cli only had filepath.Abs of the path the
// user typed. Registering a binary through a symlink — the usual way to pin a
// built CLI at the repo root — produced two different strings for one file, so
// the exact-equality strip silently did nothing and an absolute path was baked
// into the manifest as Command[0].
func TestStripBinaryPrefix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test not supported on windows")
	}

	dir := t.TempDir()
	real := filepath.Join(dir, "tools", "projectcli")
	if err := os.MkdirAll(real, 0755); err != nil {
		t.Fatal(err)
	}
	realBin := filepath.Join(real, "projectcli")
	if err := os.WriteFile(realBin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	linkBin := filepath.Join(dir, "mycli")
	if err := os.Symlink(realBin, linkBin); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		cmd        []string
		candidates []string
		want       []string
	}{
		{
			name:       "exact match still strips",
			cmd:        []string{linkBin, "query", "status"},
			candidates: []string{linkBin},
			want:       []string{"query", "status"},
		},
		{
			name:       "symlink target strips against the link the user typed",
			cmd:        []string{realBin, "query", "status"},
			candidates: []string{linkBin},
			want:       []string{"query", "status"},
		},
		{
			name:       "link strips against the resolved path",
			cmd:        []string{linkBin, "check", "health"},
			candidates: []string{realBin},
			want:       []string{"check", "health"},
		},
		{
			name: "a PATH command name strips by string",
			// Mix reports its runner by name, not by path; nothing on disk
			// resolves, so only exact equality can match here.
			cmd:        []string{"mix", "demo.query.status"},
			candidates: []string{"/usr/bin/mix", "mix"},
			want:       []string{"demo.query.status"},
		},
		{
			name:       "an unrelated first token is left alone",
			cmd:        []string{"query", "status"},
			candidates: []string{realBin, linkBin},
			want:       []string{"query", "status"},
		},
		{
			name: "two unresolvable equal-looking tokens do not match",
			// "query" is not the binary just because a caller passed the same
			// nonexistent spelling as a candidate; only one leading token may
			// ever be dropped, and dropping the wrong one is unrecoverable.
			cmd:        []string{"query", "status"},
			candidates: []string{"query/status"},
			want:       []string{"query", "status"},
		},
		{
			name:       "empty command is untouched",
			cmd:        nil,
			candidates: []string{realBin},
			want:       nil,
		},
		{
			name:       "empty candidates are ignored",
			cmd:        []string{realBin, "query"},
			candidates: []string{"", realBin},
			want:       []string{"query"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripBinaryPrefix(tt.cmd, tt.candidates...)
			if len(got) != len(tt.want) {
				t.Fatalf("StripBinaryPrefix(%v) = %v, want %v", tt.cmd, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("StripBinaryPrefix(%v) = %v, want %v", tt.cmd, got, tt.want)
				}
			}
		})
	}
}

// The symlink case again, end to end through the resolver: a binary registered
// by its link must strip the path the CLI actually reports about itself.
func TestStripBinaryPrefixWithResolvedTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test not supported on windows")
	}

	dir := t.TempDir()
	realBin := filepath.Join(dir, "projectcli")
	if err := os.WriteFile(realBin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "mycli")
	if err := os.Symlink(realBin, link); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)
	target, err := ResolveTarget("./mycli")
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}

	// What os.Executable() would report: the resolved path, not the link.
	resolved, err := filepath.EvalSymlinks(realBin)
	if err != nil {
		t.Fatal(err)
	}
	got := StripBinaryPrefix([]string{resolved, "query", "status"}, "./mycli", target.Exec, target.Config)
	if len(got) != 2 || got[0] != "query" {
		t.Errorf("command = %v, want [query status]", got)
	}
}

func TestResolveTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH lookup and permissions differ on windows")
	}

	// Resolve the temp root up front: on some platforms t.TempDir() hands back a
	// path containing a symlink (macOS /var -> /private/var) while os.Getwd()
	// reports the resolved one, which would make the repo-relative expectations
	// below fail for reasons that have nothing to do with the code under test.
	dir := evalSymlinks(t, t.TempDir())
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	// A command reachable only through PATH — how an interpreted project CLI
	// (mix, npm, just) is invoked. It is not a file in the repo, and rejecting
	// it made the Elixir scaffold impossible to register.
	pathBin := filepath.Join(binDir, "fakemix")
	if err := os.WriteFile(pathBin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	repoBin := filepath.Join(dir, "mycli")
	if err := os.WriteFile(repoBin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)

	tests := []struct {
		name       string
		arg        string
		wantErr    string
		wantConfig string
		wantExec   string
	}{
		{
			name: "relative path stays relative in config",
			arg:  "./mycli", wantConfig: "./mycli", wantExec: repoBin,
		},
		{
			name: "bare filename in cwd is treated as a path, not a PATH lookup",
			arg:  "mycli", wantConfig: "./mycli", wantExec: repoBin,
		},
		{
			name: "PATH command records the bare name",
			// The absolute path of a version-managed toolchain (mise, asdf) is
			// specific to one machine; committing it makes the config useless
			// to everyone else on the team.
			arg: "fakemix", wantConfig: "fakemix", wantExec: pathBin,
		},
		{
			name: "a directory is rejected",
			arg:  "./sub", wantErr: "not a binary",
		},
		{
			name: "a missing path is rejected",
			arg:  "./nope", wantErr: "binary not found",
		},
		{
			name: "a missing bare name reports both lookups",
			arg:  "nope", wantErr: "not on PATH",
		},
		{
			name: "empty argument is rejected",
			arg:  "  ", wantErr: "no project CLI given",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveTarget(tt.arg)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveTarget(%q): %v", tt.arg, err)
			}
			if got.Config != tt.wantConfig {
				t.Errorf("Config = %q, want %q", got.Config, tt.wantConfig)
			}
			if got.Exec != tt.wantExec {
				t.Errorf("Exec = %q, want %q", got.Exec, tt.wantExec)
			}
		})
	}
}

// A binary outside the working tree can't be written as a repo-relative path,
// so the absolute one has to survive.
func TestResolveTargetOutsideWorkingTree(t *testing.T) {
	outside := evalSymlinks(t, t.TempDir())
	bin := filepath.Join(outside, "tool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())

	got, err := ResolveTarget(bin)
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if got.Config != bin {
		t.Errorf("Config = %q, want the absolute path %q", got.Config, bin)
	}
}

func evalSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolving %s: %v", path, err)
	}
	return resolved
}
