package backend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// backendCommandTimeout bounds short backend operations such as probes,
// status/list queries, sends, and captures. It is deliberately package-owned
// rather than configurable: a stalled backend must not hold the watcher lock
// indefinitely.
const backendCommandTimeout = 5 * time.Second

// backendWorktreeCommandTimeout bounds worktree-mutating and prune operations,
// which may legitimately take minutes on a large repository. It remains a
// package constant so the bound cannot be changed per deployment.
const backendWorktreeCommandTimeout = 5 * time.Minute

type backendCommandClass uint8

const (
	backendCommandShort backendCommandClass = iota
	backendCommandWorktree
)

const backendEventWaitMargin = time.Second

// backendCommandWaitDelay bounds how long Wait may block after the direct
// child exits (on its own or by timeout kill) while a descendant that escaped
// the kill still holds the output pipes. The pipes are then closed and Wait
// returns, so no backend call outlives its bound by more than this delay.
const backendCommandWaitDelay = time.Second

func backendCommandTimeoutFor(class backendCommandClass) time.Duration {
	switch class {
	case backendCommandWorktree:
		return backendWorktreeCommandTimeout
	default:
		return backendCommandTimeout
	}
}

// runBackendCommand runs one short backend-owned external command under the
// package timeout. stdout and stderr are returned separately so adapters can
// preserve their CLI-specific error envelopes.
func runBackendCommand(bin string, args []string, dir string, env []string) ([]byte, []byte, error) {
	return runBackendCommandClass(context.Background(), backendCommandShort, bin, args, dir, env)
}

func runBackendCommandClass(parent context.Context, class backendCommandClass, bin string, args []string, dir string, env []string) ([]byte, []byte, error) {
	return runBackendCommandWithTimeout(parent, backendCommandTimeoutFor(class), bin, args, dir, env)
}

func backendEventWaitTimeout(requested time.Duration) time.Duration {
	if requested < 0 {
		requested = 0
	}
	return requested + backendEventWaitMargin
}

// eventCommandContext preserves explicit caller cancellation while allowing a
// caller deadline to be followed by the event-wait margin. The event source
// still returns its signal if herdr completes during that margin.
func eventCommandContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	stop := make(chan struct{})
	go func() {
		select {
		case <-parent.Done():
			if !errors.Is(parent.Err(), context.DeadlineExceeded) {
				cancel()
			}
		case <-stop:
		}
	}()
	return ctx, func() {
		close(stop)
		cancel()
	}
}

func runBackendEventCommand(parent context.Context, requested time.Duration, bin string, args []string, dir string, env []string) ([]byte, []byte, error) {
	commandCtx, cancel := eventCommandContext(parent)
	defer cancel()
	return runBackendCommandWithTimeout(commandCtx, backendEventWaitTimeout(requested), bin, args, dir, env)
}

func runBackendCommandWithTimeout(parent context.Context, timeout time.Duration, bin string, args []string, dir string, env []string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = backendCommandWaitDelay
	configureBackendCommand(cmd)

	if err := cmd.Start(); err != nil {
		return stdout.Bytes(), stderr.Bytes(), err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return stdout.Bytes(), stderr.Bytes(), err
	case <-ctx.Done():
		killBackendCommand(cmd)
		<-done
		if parent.Err() != nil {
			return stdout.Bytes(), stderr.Bytes(), parent.Err()
		}
		return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("%s command timed out after %s: %w", filepath.Base(bin), timeout, context.DeadlineExceeded)
	}
}

func commandOutput(stdout, stderr []byte) string {
	if trimmed := strings.TrimSpace(string(stderr)); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(string(stdout))
}

func isBackendCommandTimeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}

func wrapBackendCommandError(prefix string, stdout, stderr []byte, err error) error {
	if output := commandOutput(stdout, stderr); output != "" {
		return fmt.Errorf("%s: %s: %w", prefix, output, err)
	}
	return fmt.Errorf("%s: %w", prefix, err)
}
