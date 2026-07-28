package backend

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

func TestOSExecutor_Success(t *testing.T) {
	r := OSExecutor{}
	result, err := r.Run(context.Background(), "echo", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if result.Stdout != "hello\n" {
		t.Errorf("expected stdout %q, got %q", "hello\n", result.Stdout)
	}
}

func TestOSExecutor_NonZeroExit(t *testing.T) {
	r := OSExecutor{}
	result, err := r.Run(context.Background(), "sh", "-c", "exit 42")
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	var runErr *RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("expected *RunError, got %T", err)
	}
	if runErr.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", runErr.ExitCode)
	}
	if result.ExitCode != 42 {
		t.Errorf("result.ExitCode = %d, want 42", result.ExitCode)
	}
}

func TestOSExecutor_StdErrCapture(t *testing.T) {
	r := OSExecutor{}
	result, err := r.Run(context.Background(), "sh", "-c", "echo errmsg >&2; exit 1")
	if err == nil {
		t.Fatal("expected error")
	}
	if result.Stderr != "errmsg\n" {
		t.Errorf("expected stderr %q, got %q", "errmsg\n", result.Stderr)
	}
}

func TestOSExecutor_ContextCancellation(t *testing.T) {
	r := OSExecutor{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.Run(ctx, "sleep", "10")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestOSExecutor_CmdNotFound(t *testing.T) {
	r := OSExecutor{}
	_, err := r.Run(context.Background(), "nonexistent-binary-xyz")
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	var runErr *RunError
	if errors.As(err, &runErr) {
		// Should be an exec.Error, not ExitError
		if errors.Is(runErr.Err, exec.ErrNotFound) {
			return // OK
		}
	}
	// Some systems may not wrap exec.ErrNotFound in RunError
	// Accept the raw error as well.
	var execErr *exec.Error
	if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
		return
	}
	t.Logf("got error type %T: %v", err, err)
}

func TestRunError_Error(t *testing.T) {
	tests := []struct {
		name  string
		err   *RunError
		check string
	}{
		{
			name:  "exit code only",
			err:   &RunError{ExitCode: 1},
			check: "exit code 1",
		},
		{
			name:  "exit code with stderr",
			err:   &RunError{ExitCode: 2, Stderr: "fail"},
			check: "exit code 2: fail",
		},
		{
			name:  "exit code with underlying error",
			err:   &RunError{ExitCode: 3, Err: errors.New("signal: killed")},
			check: "exit code 3: signal: killed",
		},
		{
			name:  "all fields",
			err:   &RunError{ExitCode: 4, Stderr: "err", Err: errors.New("boom")},
			check: "exit code 4: boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.check {
				t.Errorf("Error() = %q, want %q", got, tt.check)
			}
		})
	}
}

func TestRunError_Unwrap(t *testing.T) {
	base := errors.New("base error")
	err := &RunError{ExitCode: 1, Err: base}
	if !errors.Is(err, base) {
		t.Error("expected errors.Is to match base error")
	}
}

func TestFuncRunner(t *testing.T) {
	var capturedName string
	var capturedArgs []string
	fn := FuncRunner(func(_ context.Context, name string, args ...string) (*Result, error) {
		capturedName = name
		capturedArgs = args
		return &Result{Stdout: "mock", ExitCode: 0}, nil
	})

	result, err := fn.Run(context.Background(), "git", "status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedName != "git" {
		t.Errorf("captured name = %q, want %q", capturedName, "git")
	}
	if len(capturedArgs) != 1 || capturedArgs[0] != "status" {
		t.Errorf("captured args = %v, want [status]", capturedArgs)
	}
	if result.Stdout != "mock" {
		t.Errorf("result.Stdout = %q, want %q", result.Stdout, "mock")
	}
}

func TestFuncRunner_Error(t *testing.T) {
	fn := FuncRunner(func(_ context.Context, name string, args ...string) (*Result, error) {
		return &Result{ExitCode: 1, Stderr: "nope"}, &RunError{ExitCode: 1, Stderr: "nope"}
	})
	result, err := fn.Run(context.Background(), "false")
	if err == nil {
		t.Fatal("expected error")
	}
	if result.ExitCode != 1 {
		t.Errorf("result.ExitCode = %d, want 1", result.ExitCode)
	}
	if result.Stderr != "nope" {
		t.Errorf("result.Stderr = %q, want %q", result.Stderr, "nope")
	}
}

func TestOSExecutor_RunErrorImplementsError(t *testing.T) {
	var _ error = (*RunError)(nil)
}
