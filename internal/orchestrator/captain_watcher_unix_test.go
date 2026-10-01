//go:build !windows

package orchestrator

import (
	"os"
	"strings"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

// TestEnsureWatcherKeepsEvidenceOfAnUnresponsiveWatcher pins O1: the idle
// policy's stop must act on what StopWatcher observed. A watcher that accepts
// the signal but outlives the bound is still running, so its beat, identity
// and lease stay and EnsureWatcher reports the failure instead of clearing
// the evidence a later stop needs to target it.
func TestEnsureWatcherKeepsEvidenceOfAnUnresponsiveWatcher(t *testing.T) {
	home := t.TempDir()
	pid := publishWatcherChild(t, home)
	if claimed, err := mhome.ClaimWatcherLease(home, pid); err != nil || !claimed {
		t.Fatalf("claiming watcher lease for child: claimed=%v err=%v", claimed, err)
	}
	if status := WatcherStatusSummary(home); status != WatcherRunning {
		t.Fatalf("precondition: watcher status = %s, want running", status)
	}
	saved := signalWatcher
	signalWatcher = func(*os.Process) error { return nil }
	t.Cleanup(func() { signalWatcher = saved })

	err := EnsureWatcher(home, false)
	if err == nil || !strings.Contains(err.Error(), "did not exit") {
		t.Fatalf("EnsureWatcher(false) over an unresponsive watcher = %v, want the did-not-exit refusal", err)
	}
	if _, beatPID, ok := ReadBeat(home); !ok || beatPID != pid {
		t.Error("beat was cleared for a watcher that is still alive")
	}
	if ReadIdentity(home) == nil {
		t.Error("identity was cleared for a watcher that is still alive")
	}
	if lease, err := mhome.ReadWatcherLease(home); err != nil || lease == nil || lease.PID != pid {
		t.Errorf("watcher lease = %+v, err=%v; want it kept for PID %d", lease, err, pid)
	}
}
