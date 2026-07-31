package home

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrUnhealthyWatcher is returned when the watcher is unhealthy and the
// operation is blocked by degraded mode. An unhealthy watcher blocks
// new ownership, start, and spawn while allowing diagnostics, repair,
// reconciliation, authorized delivery, holds, and safe teardown.
var ErrUnhealthyWatcher = errors.New("watcher lease is unhealthy: dispatch blocked; run diagnostics, repair, or reconcile first")

// isDegradedAction returns true when the given action is blocked during
// degraded mode (unhealthy watcher). Blocked actions are those that
// require a healthy watcher to safely progress: ownership transfer,
// task start, and soldier spawn.
func isDegradedAction(action DispatchAction) bool {
	switch action {
	case DispatchActionHandoff, DispatchActionStart, DispatchActionSpawn:
		return true
	}
	return false
}

// CheckWatcherHealthForDispatch checks whether the watcher lease is healthy
// enough for the given dispatch action. Returns ErrUnhealthyWatcher when
// the watcher is unhealthy and the action is blocked.
//
// Blocked actions (require healthy watcher):
//   - DispatchActionHandoff (ownership transfer)
//   - DispatchActionStart (task start)
//   - DispatchActionSpawn (soldier spawn)
//
// Allowed during degraded mode (no check):
//   - Diagnostics (read-only)
//   - Repair (fix integration, restore state)
//   - Reconciliation (captain converge, terminal receipt relay)
//   - Authorized delivery (wake delivery, report)
//   - Dispatch holds (create, release, check)
//   - Safe teardown (stop watcher, release lease)
func CheckWatcherHealthForDispatch(homeDir string, action DispatchAction) error {
	if !isDegradedAction(action) {
		return nil
	}
	// Only check watcher health when the home has a watcher lease file.
	// Homes without a lease (e.g., fresh init) are not in degraded mode.
	leasePath := WatcherLeasePath(homeDir)
	if _, err := os.Stat(leasePath); os.IsNotExist(err) {
		return nil // no watcher lease — not a degraded scenario
	}
	if !IsWatcherLeaseHealthy(homeDir) {
		return fmt.Errorf("%w: home=%s action=%s", ErrUnhealthyWatcher, homeDir, action)
	}
	return nil
}

// CheckWatcherHealthOwnership checks whether the watcher lease is healthy
// for the owning home and returns a structured result. The owning home is
// the home whose watcher would supervise the new work.
func CheckWatcherHealthOwnership(homeDir string) error {
	return CheckWatcherHealthForDispatch(homeDir, DispatchActionHandoff)
}

// RequireHealthyWatcherOrRepair checks watcher health and returns a
// structured error that includes the observed lease summary. Useful for
// CLI commands that want to show the user what's wrong.
func RequireHealthyWatcherOrRepair(homeDir string) error {
	summary := ObserveWatcherLease(homeDir)
	if summary == nil || summary.Absent {
		return nil // no watcher — no degraded mode
	}
	if summary.Healthy {
		return nil
	}
	if summary.Stale {
		watcherStaleMessage := fmt.Sprintf("watcher beat is stale (age %s); run 'munsu watch run --home %s' to repair", summary.Age, homeDir)
		return fmt.Errorf("%w: %s", ErrUnhealthyWatcher, watcherStaleMessage)
	}
	return fmt.Errorf("%w: watcher PID %d is not alive; run 'munsu watch ensure --home %s' to repair", ErrUnhealthyWatcher, summary.PID, homeDir)
}

// degradedModeLockPath returns the path to the degraded mode lock file.
// When present, it indicates that the home was in degraded mode and
// recovery has not yet completed.
func degradedModeLockPath(homeDir string) string {
	return filepath.Join(homeDir, "state", ".degraded-mode")
}

// MarkDegradedMode marks the home as being in degraded mode by writing a
// lock file. This is used to track that recovery must complete before
// dispatch reopens.
func MarkDegradedMode(homeDir string) error {
	path := degradedModeLockPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	return atomicWrite(path, []byte(fmt.Sprintf("%d\n", time.Now().Unix())))
}

// ClearDegradedMode removes the degraded mode lock file, indicating that
// recovery has completed and dispatch can reopen.
func ClearDegradedMode(homeDir string) error {
	return os.Remove(degradedModeLockPath(homeDir))
}

// IsDegradedMode returns true when the home is currently in degraded mode.
func IsDegradedMode(homeDir string) bool {
	_, err := os.Stat(degradedModeLockPath(homeDir))
	return err == nil
}

// RecoverAndClearDegradedMode runs recovery steps and clears degraded mode
// if the recovery succeeds. Recovery drains pending receipts and converges
// projections before dispatch reopens.
func RecoverAndClearDegradedMode(homeDir string, reconcile func(string) error) error {
	if !IsDegradedMode(homeDir) {
		return nil
	}
	// Run reconciliation (drains pending receipts, converges projections).
	if reconcile != nil {
		if err := reconcile(homeDir); err != nil {
			return fmt.Errorf("recovery reconciliation failed: %w", err)
		}
	}
	// Clear degraded mode.
	return ClearDegradedMode(homeDir)
}