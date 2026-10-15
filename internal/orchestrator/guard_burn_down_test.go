package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

func TestGuardBurnDownDaemonStartRefusesHeldLock(t *testing.T) {
	home := t.TempDir()
	held, acquired, err := mhome.AcquireAFKLock(home)
	if err != nil || !acquired {
		t.Fatalf("AcquireAFKLock = %v, %v; want held", acquired, err)
	}
	defer held.Release()

	ready := make(chan struct{})
	err = (&Daemon{ready: ready}).Start(home)
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("Daemon.Start error = %v, want held-lock refusal", err)
	}
	t.Logf("Daemon.Start refusal: %v", err)
	select {
	case <-ready:
		t.Log("Daemon.Start readiness seam closed before refusal")
	default:
		t.Fatal("Daemon.Start did not close readiness seam before refusal")
	}
}

func TestGuardBurnDownValidateTargetOwnershipRejectsNil(t *testing.T) {
	if err := ValidateTargetOwnership(nil); err == nil || !strings.Contains(err.Error(), "target is nil") {
		t.Fatalf("ValidateTargetOwnership error = %v, want nil-target refusal", err)
	} else {
		t.Logf("ValidateTargetOwnership refusal: %v", err)
	}
}

func TestGuardBurnDownWatcherRunRefusesHeldWatchLock(t *testing.T) {
	home := t.TempDir()
	acquired, err := AcquireWatch(home)
	if err != nil || !acquired {
		t.Fatalf("AcquireWatch = (%v, %v), want held lock", acquired, err)
	}
	defer ReleaseWatch(home)

	_, err = run(home, nil, nil, nil, nil, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "another watcher is already running") {
		t.Fatalf("run error = %v, want held-watch-lock refusal", err)
	}
	t.Logf("run held-watch-lock refusal: %v", err)
}

func TestWatcherRunPropagatesLiveLeaseConflict(t *testing.T) {
	home := t.TempDir()
	lease := mhome.WatcherLease{
		Home:      mhome.Canonical(home),
		PID:       os.Getppid(),
		StartedAt: time.Now().Unix(),
		UpdatedAt: time.Now().UnixNano(),
	}
	data, err := json.Marshal(lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(mhome.WatcherLeasePath(home)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mhome.WatcherLeasePath(home), data, 0644); err != nil {
		t.Fatal(err)
	}

	_, err = run(home, nil, nil, nil, nil, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "claiming watcher lease: watcher lease held by pid") {
		t.Fatalf("run error = %v, want live-lease conflict propagation", err)
	}
	t.Logf("run live-lease conflict: %v", err)
}

func TestGuardBurnDownStopWatcherRefusesUnownedPID(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteBeat(home); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mhome.WriterIdentityPath(home, "watcher"), []byte("schema_version=1\nkind=watcher\npid=9999999\n"), 0644); err != nil {
		t.Fatal(err)
	}

	stop, err := StopWatcher(home)
	if err == nil || !strings.Contains(err.Error(), "ownership could not be verified") || stop.State != StopIdentityMismatch {
		t.Fatalf("StopWatcher = %+v, %v, want ownership refusal", stop, err)
	}
	t.Logf("StopWatcher refusal: %v", err)
}
