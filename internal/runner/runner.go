// Package runner provides an injectable command runner with a typed
// result and error taxonomy. The Runner interface allows callers to
// execute external commands and receive structured results, enabling
// hermetic testing through mock implementations.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// Result holds the captured output from a command execution.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// RunError classifies a failed command execution with structured fields.
type RunError struct {
	ExitCode int
	Stderr   string
	// Err is the underlying error (e.g. exec.Error, context error).
	Err error
}

func (e *RunError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("exit code %d: %s", e.ExitCode, e.Err)
	}
	if e.Stderr != "" {
		return fmt.Sprintf("exit code %d: %s", e.ExitCode, e.Stderr)
	}
	return fmt.Sprintf("exit code %d", e.ExitCode)
}

func (e *RunError) Unwrap() error {
	return e.Err
}

// Runner is an injectable interface for executing commands.
type Runner interface {
	// Run executes the command and returns the structured result.
	// On a non-zero exit code, Run returns both a non-nil *Result with
	// captured output and a non-nil *RunError.
	Run(ctx context.Context, name string, args ...string) (*Result, error)
}

// OSExecutor is the default Runner that spawns real OS processes.
type OSExecutor struct{}

func (OSExecutor) Run(ctx context.Context, name string, args ...string) (*Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := &Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
		return result, &RunError{
			ExitCode: result.ExitCode,
			Stderr:   result.Stderr,
			Err:      err,
		}
	}

	return result, nil
}

// FuncRunner adapts a function to the Runner interface for testing.
type FuncRunner func(ctx context.Context, name string, args ...string) (*Result, error)

func (f FuncRunner) Run(ctx context.Context, name string, args ...string) (*Result, error) {
	return f(ctx, name, args...)
}
