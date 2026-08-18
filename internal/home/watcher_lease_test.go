package home

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcherLease_ClaimAndRelease(t *testing.T) {
	home := t.TempDir()
	pid := 12345

	// Claim the lease.
	claimed, err := ClaimWatcherLease(home, pid)
	if err != nil {
		t.Fatalf("ClaimWatcherLease: %v", err)
	}
	if !claimed {
		t.Fatal("expected lease to be claimed")
	}

	// Verify lease file exists.
	path := WatcherLeasePath(home)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lease file should exist: %v", err)
	}

	// Read the lease and verify fields.
	lease, err := ReadWatcherLease(home)
	if err != nil {
		t.Fatalf("ReadWatcherLease: %v", err)
	}
	if lease == nil {
		t.Fatal("lease should not be nil")
	}
	if lease.PID != pid {
		t.Errorf("PID = %d, want %d", lease.PID, pid)
	}
	if lease.Home == "" {
		t.Error("Home should not be empty")
	}
	if lease.StartedAt <= 0 {
		t.Error("StartedAt should be positive")
	}
	if lease.UpdatedAt <= 0 {
		t.Error("UpdatedAt should be positive")
	}

	// Release the lease.
	released, err := ReleaseWatcherLeaseIfMatches(home, pid)
	if err != nil {
		t.Fatalf("ReleaseWatcherLeaseIfMatches: %v", err)
	}
	if !released {
		t.Fatal("ReleaseWatcherLeaseIfMatches did not release the holder's own lease")
	}

	// Verify lease file is gone.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("lease file should be removed after release: %v", err)
	}
}

func TestWatcherLease_ReleaseIdempotent(t *testing.T) {
	home := t.TempDir()

	// Release on empty home should not error.
	released, err := ReleaseWatcherLeaseIfMatches(home, 12345)
	if err != nil {
		t.Fatalf("ReleaseWatcherLeaseIfMatches on empty home: %v", err)
	}
	if released {
		t.Fatal("released a lease that never existed")
	}

	// Claim then release twice.
	ClaimWatcherLease(home, 12345)
	released, err = ReleaseWatcherLeaseIfMatches(home, 12345)
	if err != nil {
		t.Fatalf("first release: %v", err)
	}
	if !released {
		t.Fatal("first release did not remove the lease")
	}
	// Second release should be idempotent.
	released, err = ReleaseWatcherLeaseIfMatches(home, 12345)
	if err != nil {
		t.Fatalf("second release: %v", err)
	}
	if released {
		t.Fatal("second release reported removing a lease that was already gone")
	}
}

// TestWatcherLease_LateReleaseByOldHolderPreservesSuccessorLease reproduces the
// stop-then-restart ordering rather than asserting that some lease exists: the
// old watcher has been signalled but has not finished exiting, the replacement
// claims the lease, and only then does the old watcher's deferred release run.
// The successor's lease must survive it.
func TestWatcherLease_LateReleaseByOldHolderPreservesSuccessorLease(t *testing.T) {
	home := t.TempDir()
	const oldPID, newPID = 9999998, 9999999

	if claimed, err := ClaimWatcherLease(home, oldPID); err != nil || !claimed {
		t.Fatalf("old watcher claim = (%v, %v), want (true, nil)", claimed, err)
	}

	// Replacement watcher claims while the old holder is still winding down.
	if claimed, err := ClaimWatcherLease(home, newPID); err != nil || !claimed {
		t.Fatalf("successor claim = (%v, %v), want (true, nil)", claimed, err)
	}

	// The old watcher finally exits and runs its deferred release.
	released, err := ReleaseWatcherLeaseIfMatches(home, oldPID)
	if err != nil {
		t.Fatalf("late release by old holder: %v", err)
	}
	if released {
		t.Fatal("old watcher released a lease it no longer held")
	}

	lease, err := ReadWatcherLease(home)
	if err != nil {
		t.Fatalf("ReadWatcherLease: %v", err)
	}
	if lease == nil {
		t.Fatal("old watcher deleted the successor's lease")
	}
	if lease.PID != newPID {
		t.Fatalf("lease PID = %d, want %d", lease.PID, newPID)
	}
}

func TestWatcherLease_ReadNonExistent(t *testing.T) {
	home := t.TempDir()
	lease, err := ReadWatcherLease(home)
	if err != nil {
		t.Fatalf("ReadWatcherLease on empty home: %v", err)
	}
	if lease != nil {
		t.Error("expected nil lease for empty home")
	}
}

func TestWatcherLease_ClaimLeaseUniqueness(t *testing.T) {
	home := t.TempDir()

	// Claim with PID 100.
	claimed, err := ClaimWatcherLease(home, 100)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !claimed {
		t.Fatal("expected first claim to succeed")
	}

	// Claim with a different PID 200 — should fail because PID 100 is not alive.
	// In tests, PID 100 is not a real process, so the lease should be reclaimable.
	claimed, err = ClaimWatcherLease(home, 200)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if !claimed {
		t.Fatal("expected second claim to succeed because PID 100 is not alive")
	}

	// Verify the lease is now held by PID 200.
	lease, err := ReadWatcherLease(home)
	if err != nil {
		t.Fatalf("ReadWatcherLease: %v", err)
	}
	if lease.PID != 200 {
		t.Errorf("lease PID = %d, want 200", lease.PID)
	}
}

func TestWatcherLease_ClaimSamePIDUpdatesTimestamp(t *testing.T) {
	home := t.TempDir()
	pid := 12345

	// Claim the lease.
	claimed, err := ClaimWatcherLease(home, pid)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !claimed {
		t.Fatal("expected first claim to succeed")
	}

	original, _ := ReadWatcherLease(home)
	originalUpdatedAt := original.UpdatedAt

	// Small delay to ensure timestamp changes.
	time.Sleep(2 * time.Millisecond)

	// Claim again with same PID.
	claimed, err = ClaimWatcherLease(home, pid)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if !claimed {
		t.Fatal("expected same PID claim to succeed")
	}

	updated, _ := ReadWatcherLease(home)
	if updated.UpdatedAt <= originalUpdatedAt {
		t.Error("expected UpdatedAt to increase on same-PID claim")
	}
}

func TestWatcherLease_IsHealthy(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	// No lease — not healthy.
	if IsWatcherLeaseHealthy(home) {
		t.Error("expected unhealthy when no lease exists")
	}

	// Claim lease with our own PID (which is alive).
	pid := os.Getpid()
	claimed, err := ClaimWatcherLease(home, pid)
	if err != nil {
		t.Fatalf("ClaimWatcherLease: %v", err)
	}
	if !claimed {
		t.Fatal("expected lease to be claimed")
	}

	// Write a fresh beat.
	WriteWatcherBeat(home)

	// With a fresh beat and alive PID, the lease should be healthy.
	if !IsWatcherLeaseHealthy(home) {
		t.Error("expected healthy lease with fresh beat and alive PID")
	}
}

func TestWatcherLease_IsHealthyStaleBeat(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	// Claim lease with our own PID.
	pid := os.Getpid()
	ClaimWatcherLease(home, pid)

	// Write a very old beat (beyond stale threshold).
	oldBeat := filepath.Join(home, "state", ".last-watcher-beat")
	oldTime := time.Now().Add(-2 * WatcherStaleThreshold()).Unix()
	os.WriteFile(oldBeat, []byte("0"), 0644)
	os.Chtimes(oldBeat, time.Unix(oldTime, 0), time.Unix(oldTime, 0))

	// With a stale beat, the lease should NOT be healthy.
	if IsWatcherLeaseHealthy(home) {
		t.Error("expected unhealthy lease with stale beat")
	}
}

func TestWatcherLease_ParseLeaseInvalid(t *testing.T) {
	home := t.TempDir()
	path := WatcherLeasePath(home)
	os.MkdirAll(filepath.Dir(path), 0755)

	// Write invalid JSON.
	os.WriteFile(path, []byte("not json"), 0644)

	_, err := ReadWatcherLease(home)
	if err == nil {
		t.Error("expected error for invalid lease JSON")
	}
}

func TestWatcherLease_ParseLeaseMissingFields(t *testing.T) {
	home := t.TempDir()
	path := WatcherLeasePath(home)
	os.MkdirAll(filepath.Dir(path), 0755)

	// Write JSON with missing fields.
	os.WriteFile(path, []byte(`{"home":"","pid":0}`), 0644)

	_, err := ReadWatcherLease(home)
	if err == nil {
		t.Error("expected error for lease with missing fields")
	}
}

func TestWatcherLease_CanonicalHome(t *testing.T) {
	home := t.TempDir()
	pid := 12345

	ClaimWatcherLease(home, pid)
	lease, _ := ReadWatcherLease(home)

	// The lease should store the canonical home path.
	canonical := Canonical(home)
	if lease.Home != canonical {
		t.Errorf("Home = %q, want canonical %q", lease.Home, canonical)
	}
}

func TestWatcherLease_ClaimDeadPIDReclaim(t *testing.T) {
	home := t.TempDir()

	// Claim with a dead PID.
	ClaimWatcherLease(home, 9999999)

	// Claim with a different PID — should reclaim because first PID is dead.
	claimed, err := ClaimWatcherLease(home, 100)
	if err != nil {
		t.Fatalf("reclaim claim: %v", err)
	}
	if !claimed {
		t.Fatal("expected reclaim to succeed")
	}

	lease, _ := ReadWatcherLease(home)
	if lease.PID != 100 {
		t.Errorf("lease PID = %d, want 100", lease.PID)
	}
}
