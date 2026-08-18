//go:build !windows

package orchestrator

import (
	"os/exec"
	"testing"
	"time"
)

// TestSignalWatcherProcessTerminatesRunningProcess measures the unix half of
// the signal split for real: the unix test lane runs it, so the SIGTERM path
// stopRunningWatcher depends on is proven to end a live process here.
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
