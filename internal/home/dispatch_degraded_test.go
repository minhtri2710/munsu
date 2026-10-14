package home

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckWatcherHealthForDispatch_NoLease(t *testing.T) {
	home := t.TempDir()

	// Without a lease, dispatch should be allowed.
	if err := CheckWatcherHealthForDispatch(home); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCheckWatcherHealthForDispatch_HealthyLease(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	pid := os.Getpid()
	ClaimWatcherLease(home, pid)
	WriteWatcherBeat(home)

	// With a healthy lease, dispatch should be allowed.
	if err := CheckWatcherHealthForDispatch(home); err != nil {
		t.Errorf("with healthy lease: unexpected error: %v", err)
	}
}

func TestCheckWatcherHealthForDispatch_UnhealthyLeaseBlocksDegraded(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	// Claim lease with a dead PID.
	ClaimWatcherLease(home, 9999999)

	// Dispatch should be blocked.
	if err := CheckWatcherHealthForDispatch(home); err == nil {
		t.Errorf("expected error for unhealthy lease")
	} else if !errors.Is(err, ErrUnhealthyWatcher) {
		t.Errorf("expected ErrUnhealthyWatcher, got %v", err)
	}
}

func TestCheckWatcherHealthForDispatch_StaleBeat(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	pid := os.Getpid()
	ClaimWatcherLease(home, pid)

	// Write a stale beat.
	oldBeat := filepath.Join(home, "state", ".last-watcher-beat")
	os.WriteFile(oldBeat, []byte("0"), 0644)
	oldTime := time.Now().Add(-2 * WatcherStaleThreshold()).Unix()
	os.Chtimes(oldBeat, time.Unix(oldTime, 0), time.Unix(oldTime, 0))

	// Stale beat with alive PID should block dispatch.
	if err := CheckWatcherHealthForDispatch(home); err == nil {
		t.Errorf("with stale beat: expected error")
	} else if !errors.Is(err, ErrUnhealthyWatcher) {
		t.Errorf("expected ErrUnhealthyWatcher, got %v", err)
	}
}

func TestCheckWatcherHealthForDispatch_FreshBeatAlivePID(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	pid := os.Getpid()
	ClaimWatcherLease(home, pid)
	WriteWatcherBeat(home)

	// Fresh beat with alive PID should allow dispatch.
	if err := CheckWatcherHealthForDispatch(home); err != nil {
		t.Errorf("with fresh beat: unexpected error: %v", err)
	}
}
