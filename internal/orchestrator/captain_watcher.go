package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

// WatcherStatus describes the disposition of a per-captain watcher.
type WatcherStatus string

const (
	// WatcherRunning means the watcher is alive and its beat is fresh.
	WatcherRunning WatcherStatus = "running"
	// WatcherStopped means the watcher beat exists but is stale, or identity shows
	// a stopped watcher — recovery should consider starting it.
	WatcherStopped WatcherStatus = "stopped"
	// WatcherAbsent means no watcher beat or identity file exists.
	WatcherAbsent WatcherStatus = "absent"
)

// WatcherStatusSummary returns a human-readable summary for the captain home.
// Reads the watcher identity and beat to determine status, without starting or
// stopping the watcher.
func WatcherStatusSummary(captainHome string) WatcherStatus {
	// Check beat first — authoritative liveness signal.
	beatStatus := ReadBeatStatus(captainHome, time.Now())
	if !beatStatus.Exists {
		// No beat — check identity as secondary signal (leftover from crash).
		if id := ReadIdentity(captainHome); id != nil {
			return WatcherStopped
		}
		return WatcherAbsent
	}
	if beatStatus.Stale {
		return WatcherStopped
	}

	// Beat is fresh. Validate PID ownership to confirm it's our watcher.
	_, pid, ok := ReadBeat(captainHome)
	if ok && pid > 0 && ValidatePIDOwnership(captainHome, pid) {
		return WatcherRunning
	}

	// Beat exists but ownership verification failed — treat as stopped.
	return WatcherStopped
}

// LeaseStatusSummary returns a watcher status based on the watcher lease.
// This is used by the General to observe Captain watcher health without
// reading Captain task state files. The lease file is the authoritative
// source of watcher identity for a home.
func LeaseStatusSummary(captainHome string) WatcherStatus {
	summary := home.ObserveWatcherLease(captainHome)
	if summary == nil || summary.Absent {
		return WatcherAbsent
	}
	if summary.Healthy {
		return WatcherRunning
	}
	// Lease exists but is unhealthy — beat is stale or PID is dead.
	return WatcherStopped
}

// EnsureWatcher starts or stops the per-captain watcher based on whether child
// work is in flight. When hasChildWork is true and the watcher is not running,
// starts the watcher. When hasChildWork is false and the watcher is
// running, it stops the watcher (idle policy).
//
// parent-home is not required: the watcher is recovery-only for mailbox
// delivery and does not route terminal receipts. General never requires
// parent-home. Captain→General pending remains durable and health-visible
// through the mailbox system.
func EnsureWatcher(captainHome string, hasChildWork bool) error {
	status := WatcherStatusSummary(captainHome)

	if hasChildWork {
		if status == WatcherRunning {
			return nil // already running
		}

		// Start the watcher for this captain home.
		execPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("cannot resolve executable: %w", err)
		}
		cmd := exec.Command(execPath, "watch", "--home", captainHome)
		cmd.Dir = captainHome
		cmd.Stdout = nil
		cmd.Stderr = nil
		cmd.Env = append(os.Environ(), "MUNSU_HOME="+captainHome)

		configureWatcherProcess(cmd)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("starting watcher for captain home %s: %w", captainHome, err)
		}
		return nil
	}

	// No child work — idle policy: stop watcher if running.
	if status == WatcherRunning {
		if err := Stop(captainHome); err != nil {
			return fmt.Errorf("stopping watcher for captain home %s: %w", captainHome, err)
		}
		ClearBeat(captainHome)
		ClearIdentity(captainHome)
		home.ReleaseWatcherLease(captainHome)
	}
	return nil
}
