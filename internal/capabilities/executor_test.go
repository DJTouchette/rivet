package capabilities

import (
	"context"
	"errors"
	"runtime"
	"testing"
)

func newTestRegistry(t *testing.T, caps ...Capability) *Registry {
	t.Helper()
	reg := NewRegistry()
	for _, c := range caps {
		if err := reg.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

func TestExecutorRunSafe(t *testing.T) {
	reg := newTestRegistry(t, Capability{
		Name:    "echo-test",
		Kind:    KindProjectCommand,
		Command: []string{"echo", "hello"},
		Output:  "text",
		Safety:  SafetyLevelSafe,
	})

	exec := NewExecutor(reg)
	res, err := exec.Run(context.Background(), "echo-test", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if res.Stdout != "hello\n" {
		t.Errorf("expected stdout %q, got %q", "hello\n", res.Stdout)
	}
	if res.Output != "text" {
		t.Errorf("expected output format %q, got %q", "text", res.Output)
	}
}

func TestExecutorRunWithExtraArgs(t *testing.T) {
	reg := newTestRegistry(t, Capability{
		Name:    "echo-args",
		Kind:    KindProjectCommand,
		Command: []string{"echo", "base"},
		Output:  "text",
		Safety:  SafetyLevelSafe,
	})

	exec := NewExecutor(reg)
	res, err := exec.Run(context.Background(), "echo-args", []string{"extra1", "extra2"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "base extra1 extra2\n" {
		t.Errorf("expected stdout %q, got %q", "base extra1 extra2\n", res.Stdout)
	}
}

func TestExecutorRunNotFound(t *testing.T) {
	reg := NewRegistry()
	exec := NewExecutor(reg)
	_, err := exec.Run(context.Background(), "nonexistent", nil, false)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestExecutorRunNoCommand(t *testing.T) {
	reg := newTestRegistry(t, Capability{
		Name:   "no-cmd",
		Kind:   KindMCP,
		Safety: SafetyLevelSafe,
	})

	exec := NewExecutor(reg)
	_, err := exec.Run(context.Background(), "no-cmd", nil, false)
	if !errors.Is(err, ErrNoCommand) {
		t.Fatalf("expected ErrNoCommand, got %v", err)
	}
}

func TestExecutorDangerousRequiresApproval(t *testing.T) {
	reg := newTestRegistry(t, Capability{
		Name:    "danger",
		Kind:    KindProjectCommand,
		Command: []string{"echo", "boom"},
		Safety:  SafetyLevelDangerous,
	})

	exec := NewExecutor(reg)

	// Without approval — should fail.
	_, err := exec.Run(context.Background(), "danger", nil, false)
	if !errors.Is(err, ErrDangerousNoApprove) {
		t.Fatalf("expected ErrDangerousNoApprove, got %v", err)
	}

	// With approval — should succeed.
	res, err := exec.Run(context.Background(), "danger", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
}

func TestExecutorGuardedRunsWithoutApproval(t *testing.T) {
	reg := newTestRegistry(t, Capability{
		Name:    "guarded-cmd",
		Kind:    KindProjectCommand,
		Command: []string{"echo", "careful"},
		Safety:  SafetyLevelGuarded,
	})

	exec := NewExecutor(reg)
	res, err := exec.Run(context.Background(), "guarded-cmd", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "careful\n" {
		t.Errorf("expected stdout %q, got %q", "careful\n", res.Stdout)
	}
}

func TestExecutorNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses sh -c")
	}

	reg := newTestRegistry(t, Capability{
		Name:    "fail-cmd",
		Kind:    KindProjectCommand,
		Command: []string{"sh", "-c", "echo oops >&2; exit 42"},
		Output:  "text",
		Safety:  SafetyLevelSafe,
	})

	exec := NewExecutor(reg)
	res, err := exec.Run(context.Background(), "fail-cmd", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", res.ExitCode)
	}
	if res.Stderr != "oops\n" {
		t.Errorf("expected stderr %q, got %q", "oops\n", res.Stderr)
	}
}

func TestExecutorCapturesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses sh -c")
	}

	reg := newTestRegistry(t, Capability{
		Name:    "both-streams",
		Kind:    KindProjectCommand,
		Command: []string{"sh", "-c", "echo out; echo err >&2"},
		Output:  "text",
		Safety:  SafetyLevelSafe,
	})

	exec := NewExecutor(reg)
	res, err := exec.Run(context.Background(), "both-streams", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "out\n" {
		t.Errorf("expected stdout %q, got %q", "out\n", res.Stdout)
	}
	if res.Stderr != "err\n" {
		t.Errorf("expected stderr %q, got %q", "err\n", res.Stderr)
	}
}

func TestExecutorContextCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses sleep")
	}

	reg := newTestRegistry(t, Capability{
		Name:    "slow",
		Kind:    KindProjectCommand,
		Command: []string{"sleep", "60"},
		Safety:  SafetyLevelSafe,
	})

	exec := NewExecutor(reg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := exec.Run(ctx, "slow", nil, false)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
