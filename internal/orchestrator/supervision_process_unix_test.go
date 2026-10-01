//go:build !windows

package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

// TestSignalWatcherProcessTerminatesRunningProcess measures the unix half of
// the signal split for real: the unix test lane runs it, so the SIGTERM path
// StopWatcher depends on is proven to end a live process here.
func TestSignalWatcherProcessTerminatesRunningProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting process: %v", err)
	}

	if err := signalWatcherProcess(cmd.Process); err != nil {
		t.Fatalf("signalWatcherProcess: %v", err)
	}

	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()

	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("process still running 5s after signalWatcherProcess")
	}
}

// publishWatcherChild starts a live child and publishes it as home's watcher:
// beat and identity both name its PID, and a watch lock file exists. The child
// is reaped as soon as it exits, so a zombie never reads as alive.
func publishWatcherChild(t *testing.T, home string) int {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting process: %v", err)
	}
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	pid := cmd.Process.Pid

	id := NewIdentity(home)
	executable, processStart, err := mhome.ProcessIdentity(pid)
	if err != nil {
		t.Fatalf("reading child identity: %v", err)
	}
	id.PID, id.Executable, id.ProcessStart = pid, executable, processStart
	if err := WriteIdentity(home, id); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mhome.WatcherBeatPath(home), []byte(fmt.Sprintf("%d %d", time.Now().Unix(), pid)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mhome.WatchLockPath(home), nil, 0644); err != nil {
		t.Fatal(err)
	}
	return pid
}

// TestStopWatcherRefusedSignalIsNotStopped pins the #580 fail-open the CLI
// copy of stop carried: a live watcher that refuses the signal with EPERM
// (another uid) was reported stopped and its beat and identity cleared. The
// seam refuses exactly as the kernel does for a foreign-uid process.
func TestStopWatcherRefusedSignalIsNotStopped(t *testing.T) {
	home := t.TempDir()
	pid := publishWatcherChild(t, home)
	saved := signalWatcher
	signalWatcher = func(*os.Process) error { return syscall.EPERM }
	t.Cleanup(func() { signalWatcher = saved })

	stop, err := StopWatcher(home)
	if err == nil || stop.State != StopUnresponsive || stop.PID != pid {
		t.Fatalf("StopWatcher = %+v, %v; want unresponsive with the EPERM error", stop, err)
	}
	if _, beatPID, ok := ReadBeat(home); !ok || beatPID != pid {
		t.Fatal("beat was cleared for a watcher that is still alive")
	}
	if ReadIdentity(home) == nil {
		t.Fatal("identity was cleared for a watcher that is still alive")
	}
}

// TestStopWatcherStopsLiveWatcherAndKeepsWatchLock proves the observed-exit
// path clears beat and identity and leaves the flocked watch lock file alone.
func TestStopWatcherStopsLiveWatcherAndKeepsWatchLock(t *testing.T) {
	home := t.TempDir()
	pid := publishWatcherChild(t, home)

	stop, err := StopWatcher(home)
	if err != nil || stop.State != StopExited || stop.PID != pid {
		t.Fatalf("StopWatcher = %+v, %v; want stopped", stop, err)
	}
	if _, _, ok := ReadBeat(home); ok {
		t.Error("beat survived an observed stop")
	}
	if ReadIdentity(home) != nil {
		t.Error("identity survived an observed stop")
	}
	if _, err := os.Stat(mhome.WatchLockPath(home)); err != nil {
		t.Errorf("watch lock file removed: %v", err)
	}
}

// TestStopWatcherOutlivingTheBoundIsUnresponsive pins the bounded wait: a
// watcher that accepts the signal but is still alive when the bound expires is
// reported unresponsive and keeps the beat and identity that target it.
func TestStopWatcherOutlivingTheBoundIsUnresponsive(t *testing.T) {
	home := t.TempDir()
	pid := publishWatcherChild(t, home)
	saved := signalWatcher
	signalWatcher = func(*os.Process) error { return nil }
	t.Cleanup(func() { signalWatcher = saved })

	stop, err := StopWatcher(home)
	if err != nil || stop.State != StopUnresponsive || stop.PID != pid {
		t.Fatalf("StopWatcher = %+v, %v; want unresponsive", stop, err)
	}
	if _, beatPID, ok := ReadBeat(home); !ok || beatPID != pid {
		t.Fatal("beat was cleared for a watcher that is still alive")
	}
	if ReadIdentity(home) == nil {
		t.Fatal("identity was cleared for a watcher that is still alive")
	}
}
