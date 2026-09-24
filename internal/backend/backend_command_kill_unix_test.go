//go:build !windows

package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// TestBackendCommandTimeoutKillsProcessGroup pins the group kill: a grandchild
// that stays in the command's process group must die with it on timeout.
func TestBackendCommandTimeoutKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")
	bin := filepath.Join(dir, "fakecli")
	grandchild := "echo $$ > " + strconv.Quote(pidFile+".tmp") + " && mv " + strconv.Quote(pidFile+".tmp") + " " + strconv.Quote(pidFile) + " && exec sleep 30"
	testutil.WriteFakeExecutable(t, bin, "#!/bin/sh\n/bin/sh -c '"+grandchild+"' &\nexec sleep 30\n")
	t.Cleanup(func() { killBackendTestPidFile(t, pidFile) })

	deadline := newEscapeeDeadline()
	result := make(chan error, 1)
	go func() {
		_, _, err := runBackendCommandWithTimeout(deadline, time.Hour, bin, nil, dir, nil)
		result <- err
	}()
	if !waitForBackendTestFile(pidFile, backendCommandTestStartupWatchdog) {
		t.Fatal("fake CLI did not start its grandchild")
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		t.Fatalf("grandchild pid file = %q", data)
	}
	deadline.arm(0)

	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want timeout wrapping context deadline", err)
		}
	case <-time.After(backendCommandWaitDelay + backendCommandTestWatchdogMargin):
		t.Fatal("command did not return after timeout")
	}

	gone := time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(gone) {
			t.Fatalf("grandchild %d in the command's process group survived the timeout kill", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
