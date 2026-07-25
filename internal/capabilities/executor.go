package capabilities

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

var (
	ErrNoCommand          = errors.New("capability has no command defined")
	ErrDangerousNoApprove = errors.New("dangerous capability requires explicit approval (use --approve)")
	ErrNotFound           = errors.New("capability not found")
)

// Result holds the output of a capability execution.
type Result struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"` // format hint from capability (e.g. "json", "text")
}

// InProcessRunner executes a command in-process, returning stdout, stderr,
// exit code, and any fatal error. Used for built-in capabilities.
type InProcessRunner func(args []string) (stdout, stderr string, exitCode int, err error)

// Executor runs capabilities.
type Executor struct {
	registry    *Registry
	inProcRunners map[string]InProcessRunner // keyed by Command[0] (e.g. "vaulty")
}

// NewExecutor creates an executor backed by a registry.
func NewExecutor(reg *Registry) *Executor {
	return &Executor{
		registry:    reg,
		inProcRunners: make(map[string]InProcessRunner),
	}
}

// RegisterInProcess registers an in-process runner for builtins whose
// Command[0] matches the given name.
func (e *Executor) RegisterInProcess(name string, runner InProcessRunner) {
	e.inProcRunners[name] = runner
}

// HasInProcess reports whether an in-process runner is registered for the given
// Command[0]. RunCapability falls back to os/exec when one is missing, which
// for a builtin means shelling out to a binary rivet doesn't ship; callers use
// this to assert up front that every builtin is actually runnable.
func (e *Executor) HasInProcess(name string) bool {
	_, ok := e.inProcRunners[name]
	return ok
}

// Run looks up the named capability and executes it with the given extra args.
// For dangerous capabilities, approved must be true or ErrDangerousNoApprove is returned.
func (e *Executor) Run(ctx context.Context, name string, extraArgs []string, approved bool) (*Result, error) {
	cap := e.registry.Get(name)
	if cap == nil {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return e.RunCapability(ctx, cap, extraArgs, approved)
}

// RunCapability executes a capability directly.
func (e *Executor) RunCapability(ctx context.Context, cap *Capability, extraArgs []string, approved bool) (*Result, error) {
	if len(cap.Command) == 0 {
		return nil, fmt.Errorf("capability %q: %w", cap.Name, ErrNoCommand)
	}

	if cap.Safety == SafetyLevelDangerous && !approved {
		return nil, fmt.Errorf("capability %q: %w", cap.Name, ErrDangerousNoApprove)
	}

	// Build the args after Command[0].
	args := make([]string, 0, len(cap.Command)-1+len(extraArgs))
	args = append(args, cap.Command[1:]...)
	args = append(args, extraArgs...)

	// Route builtins to in-process runners.
	if cap.Builtin {
		if runner, ok := e.inProcRunners[cap.Command[0]]; ok {
			stdout, stderr, exitCode, err := runner(args)
			if err != nil {
				return nil, fmt.Errorf("capability %q: in-process execution: %w", cap.Name, err)
			}
			return &Result{
				Stdout:   stdout,
				Stderr:   stderr,
				ExitCode: exitCode,
				Output:   cap.Output,
			}, nil
		}
	}

	// Fall back to os/exec for external commands.
	cmd := exec.CommandContext(ctx, cap.Command[0], args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("capability %q: executing command: %w", cap.Name, err)
		}
	}

	return &Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Output:   cap.Output,
	}, nil
}
