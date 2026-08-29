package orchestrator

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// watcherChildPIDEnv names the file the re-exec'd watcher child records its PID
// in. EnsureWatcher copies os.Environ() into the child, so a t.Setenv in the
// parent test reaches it.
const watcherChildPIDEnv = "MUNSU_TEST_WATCHER_CHILD_PIDFILE"

// exitAsWatcherChild reports whether this process is the watcher child
// EnsureWatcher re-exec'd -- "<executable> watch --home <dir>" -- and, when it
// is, records the PID the parent test reaps by.
//
// The real munsu binary has a watch command; the test binary does not, and
// testing's flag.Parse stops at the non-flag argument "watch", so the child
// inherits no -test.run filter and re-runs the whole package. That re-enters
// TestEnsureWatcher_StartsWhenChildWorkInFlight, which starts a grandchild.
// Measured on darwin without this gate: one process became eight in three
// seconds, each holding cmd.Dir -- a t.TempDir -- as its working directory.
//
// The child cannot be selected with -test.run the way startAFKDaemonChild
// selects TestAFKDaemonChildProcess: production owns the argv, so TestMain is
// the only entry point available.
func exitAsWatcherChild() bool {
	if len(os.Args) < 2 || os.Args[1] != "watch" {
		return false
	}
	if path := os.Getenv(watcherChildPIDEnv); path != "" {
		_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0600)
	}
	return true
}

// armWatcherChild directs the next watcher child to record its PID and returns
// the path it records to.
func armWatcherChild(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "watcher-child.pid")
	t.Setenv(watcherChildPIDEnv, path)
	return path
}

// reapWatcherChild waits for the watcher child to announce itself and then for
// it to exit. EnsureWatcher discards its *exec.Cmd, so nothing else will ever
// wait on that process: on unix it stays a zombie for the lifetime of the test
// binary, and on windows it keeps both the captain home it runs in and the test
// executable open against deletion.
func reapWatcherChild(t *testing.T, pidPath string) {
	t.Helper()
	pid := waitForWatcherChildPID(t, pidPath)
	if pid == os.Getpid() {
		t.Fatalf("watcher child reported the test process PID %d", pid)
	}
	// os.FindProcess never fails on unix and opens the process on windows,
	// where a failure means it is already gone and holds nothing.
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_, _ = proc.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		_ = proc.Kill()
		<-done
		t.Fatalf("watcher child PID %d did not exit", pid)
	}
}

func waitForWatcherChildPID(t *testing.T, pidPath string) int {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if data, err := os.ReadFile(pidPath); err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("watcher child never recorded a PID at %s", pidPath)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
