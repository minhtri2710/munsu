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

func TestBackendCommandTimeoutReturnsWhenEscapedDescendantHoldsPipes(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "escapee.pid")
	bin := filepath.Join(dir, "fakecli")
	testutil.WriteFakeExecutable(t, bin, "#!/bin/sh\n"+escapePipeHolderScript(t, pidFile)+"exec sleep 30\n")
	t.Cleanup(func() { killBackendTestPidFile(t, pidFile) })

	const timeout = 2 * time.Second
	type commandResult struct{ err error }
	result := make(chan commandResult, 1)
	go func() {
		_, _, err := runBackendCommandWithTimeout(context.Background(), timeout, bin, nil, dir, nil)
		result <- commandResult{err: err}
	}()
	if !waitForBackendTestFile(pidFile, backendCommandTestStartupWatchdog) {
		t.Fatal("fake CLI did not start its process-group escapee")
	}
	started := time.Now()

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
