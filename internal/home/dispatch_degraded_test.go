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

	// Without a lease, all actions should be allowed.
	for _, action := range []DispatchAction{DispatchActionHandoff, DispatchActionStart, DispatchActionSpawn} {
		if err := CheckWatcherHealthForDispatch(home, action); err != nil {
			t.Errorf("action %s: unexpected error: %v", action, err)
		}
	}
}

func TestCheckWatcherHealthForDispatch_HealthyLease(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	pid := os.Getpid()
	ClaimWatcherLease(home, pid)
	WriteWatcherBeat(home)

	// With a healthy lease, all actions should be allowed.
	for _, action := range []DispatchAction{DispatchActionHandoff, DispatchActionStart, DispatchActionSpawn} {
		if err := CheckWatcherHealthForDispatch(home, action); err != nil {
			t.Errorf("action %s with healthy lease: unexpected error: %v", action, err)
		}
	}
}

func TestCheckWatcherHealthForDispatch_UnhealthyLeaseBlocksDegraded(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	// Claim lease with a dead PID.
	ClaimWatcherLease(home, 9999999)

	// Degraded actions should be blocked.
	for _, action := range []DispatchAction{DispatchActionHandoff, DispatchActionStart, DispatchActionSpawn} {
		if err := CheckWatcherHealthForDispatch(home, action); err == nil {
			t.Errorf("action %s: expected error for unhealthy lease", action)
		} else if !errors.Is(err, ErrUnhealthyWatcher) {
			t.Errorf("action %s: expected ErrUnhealthyWatcher, got %v", action, err)
		}
	}
}

func TestCheckWatcherHealthForDispatch_NonDegradedActionsAllowed(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	// Claim lease with a dead PID.
	ClaimWatcherLease(home, 9999999)

	// Non-degraded actions (not handoff/start/spawn) should be allowed
	// even with an unhealthy watcher.
	// We test this by checking that CheckWatcherHealthForDispatch returns
	// nil for actions that aren't in the degraded set.
	// Since DispatchAction is a string type, we test with a made-up action.
	allowedAction := DispatchAction("diagnostics")
	if err := CheckWatcherHealthForDispatch(home, allowedAction); err != nil {
		t.Errorf("non-degraded action should be allowed: %v", err)
	}

	// Also test with empty string.
	if err := CheckWatcherHealthForDispatch(home, ""); err != nil {
		t.Errorf("empty action should be allowed: %v", err)
	}
}

func TestIsDegradedAction(t *testing.T) {
	tests := []struct {
		action DispatchAction
		want   bool
	}{
		{DispatchActionHandoff, true},
		{DispatchActionStart, true},
		{DispatchActionSpawn, true},
		{DispatchAction("diagnostics"), false},
		{DispatchAction("repair"), false},
		{DispatchAction("reconciliation"), false},
		{DispatchAction("delivery"), false},
		{DispatchAction("hold"), false},
		{DispatchAction("teardown"), false},
		{"", false},
	}
	for _, tt := range tests {
		got := isDegradedAction(tt.action)
		if got != tt.want {
			t.Errorf("isDegradedAction(%q) = %v, want %v", tt.action, got, tt.want)
		}
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

	// Stale beat with alive PID should block degraded actions.
	for _, action := range []DispatchAction{DispatchActionHandoff, DispatchActionStart, DispatchActionSpawn} {
		if err := CheckWatcherHealthForDispatch(home, action); err == nil {
			t.Errorf("action %s with stale beat: expected error", action)
		} else if !errors.Is(err, ErrUnhealthyWatcher) {
			t.Errorf("action %s: expected ErrUnhealthyWatcher, got %v", action, err)
		}
	}
}

func TestCheckWatcherHealthForDispatch_FreshBeatAlivePID(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	pid := os.Getpid()
	ClaimWatcherLease(home, pid)
	WriteWatcherBeat(home)

	// Fresh beat with alive PID should allow all actions.
	for _, action := range []DispatchAction{DispatchActionHandoff, DispatchActionStart, DispatchActionSpawn} {
		if err := CheckWatcherHealthForDispatch(home, action); err != nil {
			t.Errorf("action %s with fresh beat: unexpected error: %v", action, err)
		}
	}
}
