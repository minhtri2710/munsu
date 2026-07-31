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

func TestCheckWatcherHealthOwnership(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	// No lease — should be allowed.
	if err := CheckWatcherHealthOwnership(home); err != nil {
		t.Errorf("no lease: unexpected error: %v", err)
	}

	// Healthy lease — should be allowed.
	pid := os.Getpid()
	ClaimWatcherLease(home, pid)
	WriteWatcherBeat(home)
	if err := CheckWatcherHealthOwnership(home); err != nil {
		t.Errorf("healthy lease: unexpected error: %v", err)
	}

	// Unhealthy lease — should be blocked.
	ReleaseWatcherLease(home)
	ClaimWatcherLease(home, 9999999)
	if err := CheckWatcherHealthOwnership(home); err == nil {
		t.Error("unhealthy lease: expected error")
	} else if !errors.Is(err, ErrUnhealthyWatcher) {
		t.Errorf("expected ErrUnhealthyWatcher, got %v", err)
	}
}

func TestRequireHealthyWatcherOrRepair(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	// No lease — no error.
	if err := RequireHealthyWatcherOrRepair(home); err != nil {
		t.Errorf("no lease: unexpected error: %v", err)
	}

	// Healthy lease — no error.
	pid := os.Getpid()
	ClaimWatcherLease(home, pid)
	WriteWatcherBeat(home)
	if err := RequireHealthyWatcherOrRepair(home); err != nil {
		t.Errorf("healthy lease: unexpected error: %v", err)
	}

	// Unhealthy lease with dead PID — should return error with repair message.
	ReleaseWatcherLease(home)
	ClaimWatcherLease(home, 9999999)
	WriteWatcherBeat(home)
	if err := RequireHealthyWatcherOrRepair(home); err == nil {
		t.Error("unhealthy lease: expected error")
	} else if !errors.Is(err, ErrUnhealthyWatcher) {
		t.Errorf("expected ErrUnhealthyWatcher, got %v", err)
	}
}

func TestDegradedModeLifecycle(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	// Initially not in degraded mode.
	if IsDegradedMode(home) {
		t.Error("expected not in degraded mode initially")
	}

	// Mark degraded mode.
	if err := MarkDegradedMode(home); err != nil {
		t.Fatalf("MarkDegradedMode: %v", err)
	}
	if !IsDegradedMode(home) {
		t.Error("expected in degraded mode after marking")
	}

	// Clear degraded mode.
	if err := ClearDegradedMode(home); err != nil {
		t.Fatalf("ClearDegradedMode: %v", err)
	}
	if IsDegradedMode(home) {
		t.Error("expected not in degraded mode after clearing")
	}
}

func TestDegradedModeLockFile(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	path := degradedModeLockPath(home)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("degraded mode lock should not exist yet")
	}

	MarkDegradedMode(home)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("degraded mode lock should exist: %v", err)
	}

	ClearDegradedMode(home)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("degraded mode lock should be removed after clear")
	}
}

func TestRecoverAndClearDegradedMode(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	reconciled := false

	// Mark degraded mode.
	MarkDegradedMode(home)

	// Recover with a reconcile function.
	reconcile := func(dir string) error {
		if dir != home {
			t.Errorf("reconcile called with %q, want %q", dir, home)
		}
		reconciled = true
		return nil
	}

	if err := RecoverAndClearDegradedMode(home, reconcile); err != nil {
		t.Fatalf("RecoverAndClearDegradedMode: %v", err)
	}

	if !reconciled {
		t.Error("reconcile function was not called")
	}

	if IsDegradedMode(home) {
		t.Error("expected degraded mode to be cleared after recovery")
	}
}

func TestRecoverAndClearDegradedMode_NotDegraded(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	called := false
	reconcile := func(string) error {
		called = true
		return nil
	}

	// Not in degraded mode — reconcile should not be called.
	if err := RecoverAndClearDegradedMode(home, reconcile); err != nil {
		t.Fatalf("RecoverAndClearDegradedMode: %v", err)
	}

	if called {
		t.Error("reconcile should not be called when not in degraded mode")
	}
}

func TestRecoverAndClearDegradedMode_NilReconcile(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	MarkDegradedMode(home)

	// Nil reconcile should still clear degraded mode.
	if err := RecoverAndClearDegradedMode(home, nil); err != nil {
		t.Fatalf("RecoverAndClearDegradedMode with nil reconcile: %v", err)
	}

	if IsDegradedMode(home) {
		t.Error("expected degraded mode to be cleared")
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