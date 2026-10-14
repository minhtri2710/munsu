package home

import (
	"errors"
	"fmt"
	"os"
)

// ErrUnhealthyWatcher is returned when the watcher is unhealthy and the
// operation is blocked by degraded mode. An unhealthy watcher blocks
// new ownership, start, and spawn while allowing diagnostics, repair,
// reconciliation, authorized delivery, holds, and safe teardown.
var ErrUnhealthyWatcher = errors.New("watcher lease is unhealthy: dispatch blocked; run diagnostics, repair, or reconcile first")

// CheckWatcherHealthForDispatch checks whether the watcher lease is healthy
// enough to dispatch (handoff, task start, soldier spawn). Returns
// ErrUnhealthyWatcher when the watcher is unhealthy.
//
// Allowed during degraded mode (no check):
//   - Diagnostics (read-only)
//   - Repair (fix integration, restore state)
//   - Reconciliation (captain converge)
//   - Authorized delivery (wake delivery, report)
//   - Dispatch holds (create, release, check)
//   - Safe teardown (stop watcher, release lease)
func CheckWatcherHealthForDispatch(homeDir string) error {
	// Only check watcher health when the home has a watcher lease file.
	// Homes without a lease (e.g., fresh init) are not in degraded mode.
	leasePath := WatcherLeasePath(homeDir)
	if _, err := os.Stat(leasePath); os.IsNotExist(err) {
		return nil // no watcher lease — not a degraded scenario
	}
	if !IsWatcherLeaseHealthy(homeDir) {
		return fmt.Errorf("%w: home=%s", ErrUnhealthyWatcher, homeDir)
	}
	return nil
}
