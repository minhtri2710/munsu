package backend

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestHerdrPaneProbePreservesCommandExit(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFakeExecutable(t, filepath.Join(dir, "herdr"), "#!/bin/sh\nif [ \"$1\" = \"--session\" ]; then shift 2; fi\necho '{\"result\":{\"pane_id\":\"w1:p1\"}}'\nexit 1\n")
	testutil.PrependPath(t, dir)

	_, err := NewHerdrBackend("test").herdrForWindow("test:w1:p1", "pane", "get", "w1:p1")
	if err == nil {
		t.Fatal("pane probe returned nil error")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("pane probe error = %T %v, want wrapped command exit", err, err)
	}
}

func TestHerdrPromptTimeoutIsBackendFailedNotDead(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "herdr.pid")
	testutil.WriteFakeExecutable(t, filepath.Join(dir, "herdr"), "#!/bin/sh\nif [ \"$1\" = \"--session\" ]; then shift 2; fi\ncase \"$1 $2\" in\n  \"api schema\") echo 'protocol: 17'; exit 0 ;;\n  \"agent get\") echo '{\"result\":{\"agent\":{\"agent_status\":\"idle\"}}}'; exit 0 ;;\n  \"agent prompt\") echo $$ > "+strconv.Quote(pidFile)+"; exec sleep 30 ;;\nesac\nexit 1\n")
	testutil.PrependPath(t, dir)

	result := make(chan PromptResult, 1)
	go func() { result <- NewHerdrBackend("test").AgentPrompt("test:pane", "hello") }()
	select {
	case got := <-result:
		if got.Status != PromptBackendFailed {
			t.Fatalf("status = %q, want %q (detail: %s)", got.Status, PromptBackendFailed, got.Detail)
		}
		if got.Status == PromptEndpointDead {
			t.Fatal("timeout must not be classified as endpoint-dead")
		}
	case <-time.After(7 * time.Second):
		data, readErr := os.ReadFile(pidFile)
		if readErr == nil {
			if pid, parseErr := strconv.Atoi(string(data)); parseErr == nil {
				if process, findErr := os.FindProcess(pid); findErr == nil {
					_ = process.Kill()
				}
			}
		}
		t.Fatal("hung herdr backend did not return within timeout bound")
	}
}

func TestBackendCommandClasses(t *testing.T) {
	if got := backendCommandTimeoutFor(backendCommandShort); got != backendCommandTimeout {
		t.Fatalf("short command timeout = %s, want %s", got, backendCommandTimeout)
	}
	if got := backendCommandTimeoutFor(backendCommandWorktree); got != backendWorktreeCommandTimeout {
		t.Fatalf("worktree command timeout = %s, want %s", got, backendWorktreeCommandTimeout)
	}
	if backendWorktreeCommandTimeout <= backendCommandTimeout {
		t.Fatalf("worktree command timeout = %s, must exceed short timeout %s", backendWorktreeCommandTimeout, backendCommandTimeout)
	}
}

func TestBackendEventWaitTimeoutIncludesMargin(t *testing.T) {
	requested := 5 * time.Second
	got := backendEventWaitTimeout(requested)
	if got != requested+backendEventWaitMargin {
		t.Fatalf("event wait timeout = %s, want requested %s plus margin %s", got, requested, backendEventWaitMargin)
	}
	if got <= requested {
		t.Fatalf("event wait timeout = %s, must exceed requested wait %s", got, requested)
	}
}

func TestBackendCommandTimeoutIsUnknownNotDead(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "tmux.pid")
	testutil.WriteFakeExecutable(t, filepath.Join(dir, "tmux"), "#!/bin/sh\necho $$ > "+strconv.Quote(pidFile)+"\nexec sleep 30\n")
	testutil.PrependPath(t, dir)

	type probeResult struct {
		alive bool
		err   error
	}
	result := make(chan probeResult, 1)
	started := time.Now()
	go func() {
		alive, err := (&TmuxBackend{}).CheckAlive("@hung")
		result <- probeResult{alive: alive, err: err}
	}()

	select {
	case got := <-result:
		elapsed := time.Since(started)
		if got.alive {
			t.Fatal("hung backend reported alive")
		}
		if got.err == nil {
			t.Fatal("hung backend returned nil error")
		}
		if !errors.Is(got.err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want context deadline", got.err)
		}
		if errors.Is(got.err, ErrPaneNotFound) {
			t.Fatalf("timeout error = %v, must not authorize pane absence", got.err)
		}
		if elapsed >= 7*time.Second {
			t.Fatalf("hung backend took %s, want completion within timeout bound", elapsed)
		}

		obs := ObservationFromProbeError(got.err)
		if obs.Lifecycle != LifecycleUnknown {
			t.Fatalf("timeout lifecycle = %v, want unknown", obs.Lifecycle)
		}
		if obs.State() == EndpointDead || obs.Absent() {
			t.Fatalf("timeout observation = %+v, must not be dead/absent", obs)
		}
	case <-time.After(7 * time.Second):
		data, readErr := os.ReadFile(pidFile)
		if readErr == nil {
			if pid, parseErr := strconv.Atoi(string(data)); parseErr == nil {
				if process, findErr := os.FindProcess(pid); findErr == nil {
					_ = process.Kill()
				}
			}
		}
		t.Fatal("hung backend did not return within timeout bound")
	}
}
