package home

import (
	"errors"
	"fmt"
	"os"
)

// DispatchAction names a dispatch action gated by supervision. The legacy
// v1 dispatch-control record shapes were deleted; the enum survives because
// the supervision gates (fleet/CLI) classify actions as degraded or not.
type DispatchAction string

const (
	DispatchActionHandoff DispatchAction = "handoff"
	DispatchActionStart   DispatchAction = "start"
	DispatchActionSpawn   DispatchAction = "spawn"
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
