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

const (
	backendCommandTestStartupWatchdog = 10 * time.Second
	backendCommandTestWatchdogMargin  = 5 * time.Second
)

func waitForBackendTestFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func stopBackendTestProcess(pidFile string) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return
	}
	process, err := os.FindProcess(pid)
	if err == nil {
		_ = process.Kill()
	}
}

func TestHerdrPromptTimeoutIsBackendFailedNotDead(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "herdr.pid")
	testutil.WriteFakeExecutable(t, filepath.Join(dir, "herdr"), "#!/bin/sh\nif [ \"$1\" = \"--session\" ]; then shift 2; fi\ncase \"$1 $2\" in\n  \"api schema\") echo 'protocol: 17'; exit 0 ;;\n  \"agent get\") echo '{\"result\":{\"agent\":{\"agent_status\":\"idle\"}}}'; exit 0 ;;\n  \"agent prompt\") echo $$ > "+strconv.Quote(pidFile)+"; exec sleep 30 ;;\nesac\nexit 1\n")
	testutil.PrependPath(t, dir)

	result := make(chan PromptResult, 1)
	go func() { result <- NewHerdrBackend("test").AgentPrompt("test:pane", "hello") }()
	if !waitForBackendTestFile(pidFile, backendCommandTestStartupWatchdog) {
		stopBackendTestProcess(pidFile)
		t.Fatal("hung herdr prompt did not reach its blocking command")
	}
	started := time.Now()
	select {
	case got := <-result:
		if got.Status != PromptBackendFailed {
			t.Fatalf("status = %q, want %q (detail: %s)", got.Status, PromptBackendFailed, got.Detail)
		}
		if got.Status == PromptEndpointDead {
			t.Fatal("timeout must not be classified as endpoint-dead")
		}
		if got.Err == nil || !errors.Is(got.Err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want context deadline exceeded from the command bound", got.Err)
		}
	case <-time.After(backendCommandTimeout + backendCommandTestWatchdogMargin):
		stopBackendTestProcess(pidFile)
		t.Fatalf("hung herdr backend did not return within timeout bound after blocking command started (elapsed %s)", time.Since(started))
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
	go func() {
		alive, err := (&TmuxBackend{}).CheckAlive("@hung")
		result <- probeResult{alive: alive, err: err}
	}()
	if !waitForBackendTestFile(pidFile, backendCommandTestStartupWatchdog) {
		stopBackendTestProcess(pidFile)
		t.Fatal("hung tmux probe did not reach its blocking command")
	}
	started := time.Now()

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
		if elapsed > backendCommandTimeout+backendCommandTestWatchdogMargin {
			t.Fatalf("hung backend took %s after blocking command started, want completion within test bound", elapsed)
		}

		obs := ObservationFromProbeError(got.err)
		if obs.Lifecycle != LifecycleUnknown {
			t.Fatalf("timeout lifecycle = %v, want unknown", obs.Lifecycle)
		}
		if obs.State() == EndpointDead || obs.Absent() {
			t.Fatalf("timeout observation = %+v, must not be dead/absent", obs)
		}
	case <-time.After(backendCommandTimeout + backendCommandTestWatchdogMargin):
		stopBackendTestProcess(pidFile)
		t.Fatalf("hung backend did not return within timeout bound after blocking command started (elapsed %s)", time.Since(started))
	}
}

func TestLookBackendBin(t *testing.T) {
	names := []string{"herdr", "tmux", "zellij", "cmux", "orca"}
	testutil.SetPath(t, t.TempDir())
	for _, name := range names {
		if _, err := lookBackendBin(name); err == nil || err.Error() != name+": not found on PATH" {
			t.Errorf("lookBackendBin(%q) absent error = %v, want %q", name, err, name+": not found on PATH")
		}
	}
	fakeBin := fakeExecutables(t, names...)
	testutil.SetPath(t, fakeBin)
	for _, name := range names {
		path, err := lookBackendBin(name)
		if err != nil || filepath.Dir(path) != fakeBin {
			t.Errorf("lookBackendBin(%q) = %q, %v; want a path in %s", name, path, err, fakeBin)
		}
	}
}

func TestParsePipeHandle(t *testing.T) {
	tests := []struct{ handle, container, pane string }{
		{"workspace:1|surface:1", "workspace:1", "surface:1"},
		{"ctn_abc|term_def", "ctn_abc", "term_def"},
		{"a|b|c", "a|b", "c"},
		{"|surface:1", "", "surface:1"},
		{"bare", "", "bare"},
		{"", "", ""},
	}
	for _, tt := range tests {
		container, pane := parsePipeHandle(tt.handle)
		if container != tt.container || pane != tt.pane {
			t.Errorf("parsePipeHandle(%q) = (%q, %q), want (%q, %q)", tt.handle, container, pane, tt.container, tt.pane)
		}
	}
}
