//go:build !windows

package backend

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// escapePipeHolderScript returns a shell line that starts a descendant which
// leaves the command's process group while inheriting stdout, records its pid,
// and hangs; the timeout kill of the group cannot reach it.
func escapePipeHolderScript(t *testing.T, pidFile string) string {
	t.Helper()
	body := "echo $$ > " + strconv.Quote(pidFile+".tmp") + " && mv " + strconv.Quote(pidFile+".tmp") + " " + strconv.Quote(pidFile) + " && exec sleep 30"
	if _, err := exec.LookPath("perl"); err == nil {
		return "perl -e 'setpgrp(0,0); exec \"/bin/sh\", \"-c\", $ARGV[0]' " + "'" + body + "' &\n"
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return "python3 -c 'import os,sys; os.setsid(); os.execv(\"/bin/sh\", [\"sh\", \"-c\", sys.argv[1]])' " + "'" + body + "' &\n"
	}
	// Unreachable on CI: macOS and ubuntu runners ship both perl and python3.
	t.Skip("neither perl nor python3 is available to start a process-group escapee")
	return ""
}

func killBackendTestPidFile(t *testing.T, pidFile string) {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

// escapeeDeadline is a parent context that expires with DeadlineExceeded only
// once armed. The escapee must leave the process group before the timeout kill
// or it dies with the group, and interpreter startup under load can outlast any
// fixed bound, so the timeout is measured from the escape rather than from the
// command start.
type escapeeDeadline struct {
	context.Context
	done chan struct{}
}

func newEscapeeDeadline() *escapeeDeadline {
	return &escapeeDeadline{Context: context.Background(), done: make(chan struct{})}
}

func (d *escapeeDeadline) arm(timeout time.Duration) {
	time.AfterFunc(timeout, func() { close(d.done) })
}
func (d *escapeeDeadline) Done() <-chan struct{} { return d.done }
func (d *escapeeDeadline) Err() error {
	select {
	case <-d.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func TestBackendCommandTimeoutReturnsWhenEscapedDescendantHoldsPipes(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "escapee.pid")
	bin := filepath.Join(dir, "fakecli")
	testutil.WriteFakeExecutable(t, bin, "#!/bin/sh\n"+escapePipeHolderScript(t, pidFile)+"exec sleep 30\n")
	t.Cleanup(func() { killBackendTestPidFile(t, pidFile) })

	const timeout = 2 * time.Second
	deadline := newEscapeeDeadline()
	type commandResult struct{ err error }
	result := make(chan commandResult, 1)
	go func() {
		// The command's own bound is out of reach; the deadline armed below
		// is the timeout under test.
		_, _, err := runBackendCommandWithTimeout(deadline, time.Hour, bin, nil, dir, nil)
		result <- commandResult{err: err}
	}()
	// The escapee writes its pid file only after setpgrp/setsid, so from here
	// the group kill cannot reach it.
	if !waitForBackendTestFile(pidFile, backendCommandTestStartupWatchdog) {
		t.Fatal("fake CLI did not start its process-group escapee")
	}
	started := time.Now()
	deadline.arm(timeout)

	select {
	case got := <-result:
		if !errors.Is(got.err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want timeout wrapping context deadline", got.err)
		}
		if obs := ObservationFromProbeError(got.err); obs.State() == EndpointDead || obs.Absent() {
			t.Fatalf("timeout observation = %+v, must not be dead/absent", obs)
		}
		if elapsed := time.Since(started); elapsed > timeout+backendCommandWaitDelay+backendCommandTestWatchdogMargin {
			t.Fatalf("helper took %s after escapee started, want within timeout plus wait delay", elapsed)
		}
	case <-time.After(timeout + backendCommandWaitDelay + backendCommandTestWatchdogMargin):
		t.Fatalf("helper did not return after timeout kill while an escaped descendant held its pipes (elapsed %s)", time.Since(started))
	}
}

func TestBackendCommandCompletedReturnsWhenEscapedDescendantHoldsPipes(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "escapee.pid")
	bin := filepath.Join(dir, "fakecli")
	marker := filepath.Join(dir, "escapee.ready")
	testutil.WriteFakeExecutable(t, bin, "#!/bin/sh\n"+escapePipeHolderScript(t, pidFile)+
		"while [ ! -f "+strconv.Quote(pidFile)+" ]; do sleep 0.01; done\ntouch "+strconv.Quote(marker)+"\necho ok\nexit 0\n")
	t.Cleanup(func() { killBackendTestPidFile(t, pidFile) })

	const timeout = 30 * time.Second
	type commandResult struct {
		stdout []byte
		err    error
	}
	result := make(chan commandResult, 1)
	go func() {
		stdout, _, err := runBackendCommandWithTimeout(context.Background(), timeout, bin, nil, dir, nil)
		result <- commandResult{stdout: stdout, err: err}
	}()
	if !waitForBackendTestFile(marker, backendCommandTestStartupWatchdog) {
		t.Fatal("fake CLI did not start its process-group escapee")
	}
	started := time.Now()

	select {
	case got := <-result:
		if !errors.Is(got.err, exec.ErrWaitDelay) {
			t.Fatalf("error = %v, want %v so held pipes fail closed", got.err, exec.ErrWaitDelay)
		}
		if strings.TrimSpace(string(got.stdout)) != "ok" {
			t.Fatalf("stdout = %q, want output written before exit", got.stdout)
		}
	case <-time.After(backendCommandWaitDelay + backendCommandTestWatchdogMargin):
		t.Fatalf("completed command did not return while an escaped descendant held its pipes (elapsed %s)", time.Since(started))
	}
}
