package projectcli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Target is a resolved project CLI: how to execute it right now, and how to
// record it in config.yaml / capabilities.yaml.
//
// The two differ on purpose. Discovery has to exec something concrete, while
// what gets committed should survive a different machine: for a command on PATH
// that means the bare name, because the absolute path of a version-managed
// toolchain (mise, asdf, nvm) is specific to one user's install.
type Target struct {
	Exec   string // what to exec for discovery
	Config string // what to write to project_cli.command and the manifest's cli:
}

// ResolveTarget resolves the argument of `rivet project register-cli`.
//
// A file on disk wins, keeping `register-cli ./mycli` behaving exactly as
// before even when a same-named command also sits on PATH. Failing that, a bare
// name is looked up on PATH: an interpreted project CLI is invoked through its
// runner (`mix`, `npm`, `just`), which is not a file in the repo, and rejecting
// it with "binary not found: mix" made the Elixir scaffold unregisterable.
func ResolveTarget(arg string) (Target, error) {
	if strings.TrimSpace(arg) == "" {
		return Target{}, fmt.Errorf("no project CLI given")
	}

	abs, err := filepath.Abs(arg)
	if err != nil {
		return Target{}, fmt.Errorf("resolving path: %w", err)
	}

	if info, statErr := os.Stat(abs); statErr == nil {
		if info.IsDir() {
			return Target{}, fmt.Errorf("%s is a directory, not a binary", arg)
		}
		return Target{Exec: abs, Config: repoRelative(abs)}, nil
	}

	// Only a bare name can be a PATH command; anything with a separator was
	// meant as a path, and reporting it as missing is the useful answer.
	if !strings.ContainsRune(arg, filepath.Separator) {
		if resolved, lookErr := exec.LookPath(arg); lookErr == nil {
			return Target{Exec: resolved, Config: arg}, nil
		}
		return Target{}, fmt.Errorf("binary not found: %s (not a file, and not on PATH)", arg)
	}

	return Target{}, fmt.Errorf("binary not found: %s", arg)
}

// repoRelative prefers a repo-relative spelling so the config stays portable,
// falling back to the absolute path for a binary outside the working tree.
func repoRelative(abs string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return abs
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return abs
	}
	return "./" + rel
}

// StripBinaryPrefix drops the project CLI from the front of a discovered
// command, leaving the subcommand args the manifest is supposed to hold.
// Manifest.ToCapabilities prepends cli: to every command, so a surviving
// prefix runs as `<cli> /abs/path/<cli> query status`.
//
// Comparison resolves symlinks on both sides. The generated discover command
// reports os.Executable(), which on Linux reads /proc/self/exe and is therefore
// already symlink-resolved, while register-cli only ever had filepath.Abs of the
// path the user typed. Register a CLI through a symlink — the convention for
// pinning a built binary at the repo root — and the two strings differed, so the
// exact-equality strip silently did nothing.
//
// Any spelling of the binary rivet knows may be passed as a candidate: the typed
// path, the exec path, and the value recorded in the manifest (which for a PATH
// command is a bare name like "mix" that resolves to nothing at all).
func StripBinaryPrefix(cmd []string, candidates ...string) []string {
	if len(cmd) == 0 {
		return cmd
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if cmd[0] == candidate || samePath(cmd[0], candidate) {
			return cmd[1:]
		}
	}
	return cmd
}

// samePath reports whether two path spellings name the same file.
//
// Resolution is best-effort on each side independently: a path that cannot be
// resolved (a bare command name, a binary since deleted) degrades to its
// absolute form rather than failing the whole comparison, so a stale-but-equal
// pair still matches.
func samePath(a, b string) bool {
	ra, aResolved := resolvePath(a)
	rb, bResolved := resolvePath(b)
	if ra != rb {
		return false
	}
	// Two unresolvable paths matching only as strings is not evidence they are
	// the same executable — "query" and "query" would qualify. Require that at
	// least one side actually exists on disk.
	return aResolved || bResolved
}

// resolvePath returns the symlink-resolved absolute path, and whether resolution
// actually succeeded.
func resolvePath(p string) (string, bool) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p, false
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs, false
	}
	return resolved, true
}
