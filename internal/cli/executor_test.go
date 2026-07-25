package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/djtouchette/rivet/internal/capabilities"
)

// Every builtin capability must resolve to an in-process runner.
// Executor.RunCapability falls through to os/exec when Command[0] has no
// registered runner, so a missing entry doesn't fail loudly at startup — it
// fails in the user's shell with `exec: "witness": executable file not found`,
// for a tool rivet embeds and never installs on PATH. The table is derived from
// the builtin list itself so adding a new builtin family without wiring its
// runner into newExecutor breaks this test instead of a user's session.
func TestNewExecutorHasRunnerForEveryBuiltin(t *testing.T) {
	// Both optional groups on: schema.* and vaulty.* are filtered out for
	// projects that don't use them, but `rivet project run` must be able to
	// execute them wherever they are enabled.
	builtins := capabilities.BuiltinsFor(capabilities.BuiltinGroups{Schema: true, Vaulty: true})
	if len(builtins) == 0 {
		t.Fatal("no builtins returned; test would vacuously pass")
	}

	exec := newExecutor(capabilities.NewRegistry())

	for _, cap := range builtins {
		t.Run(cap.Name, func(t *testing.T) {
			if !cap.Builtin {
				t.Skipf("%s is not marked builtin", cap.Name)
			}
			if len(cap.Command) == 0 {
				t.Fatalf("%s has no command", cap.Name)
			}
			if !exec.HasInProcess(cap.Command[0]) {
				t.Errorf("no in-process runner for %q (capability %s); "+
					"it would shell out to a %q binary on PATH",
					cap.Command[0], cap.Name, cap.Command[0])
			}
		})
	}
}

// The whole point of newExecutor is that there is exactly one runner table. Two
// call sites building their own executors is how `rivet serve` ended up able to
// run witness.* while `rivet project run` could not — the lists drifted and
// nothing failed until a user hit the gap. Guard the invariant at the source
// level: capabilities.NewExecutor may only be called from newExecutor itself.
func TestOnlyOneExecutorConstructionSite(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	var callers []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if n := strings.Count(string(src), "capabilities.NewExecutor("); n > 0 {
			for i := 0; i < n; i++ {
				callers = append(callers, name)
			}
		}
	}

	if len(callers) != 1 {
		t.Errorf("capabilities.NewExecutor called %d time(s) in package cli (%v); "+
			"expected exactly one, inside newExecutor — every entry point must share the runner table",
			len(callers), callers)
	}
}
