package captain

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/supervision"
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
	beatStatus := lifecycle.ReadBeatStatus(captainHome, time.Now())
	if !beatStatus.Exists {
		// No beat — check identity as secondary signal (leftover from crash).
		if id := supervision.ReadIdentity(captainHome); id != nil {
			return WatcherStopped
		}
		return WatcherAbsent
	}
	if beatStatus.Stale {
		return WatcherStopped
	}

	// Beat is fresh. Validate PID ownership to confirm it's our watcher.
	_, pid, ok := lifecycle.ReadBeat(captainHome)
	if ok && pid > 0 && supervision.ValidatePIDOwnership(captainHome, pid) {
		return WatcherRunning
	}

	// Beat exists but ownership verification failed — treat as stopped.
	return WatcherStopped
}

// EnsureWatcher starts or stops the per-captain watcher based on whether child
// work is in flight. When hasChildWork is true and the watcher is not running,
// it starts one. When hasChildWork is false and the watcher is running, it
// stops the watcher (idle policy).
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
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("starting watcher for captain home %s: %w", captainHome, err)
		}
		return nil
	}

	// No child work — idle policy: stop watcher if running.
	if status == WatcherRunning {
		if err := supervision.Stop(captainHome); err != nil {
			return fmt.Errorf("stopping watcher for captain home %s: %w", captainHome, err)
		}
		lifecycle.ClearBeat(captainHome)
		supervision.ClearIdentity(captainHome)
	}
	return nil
}

// inFlightSoldierPath returns true if the captain home has in-flight child work
// (state/*.meta with kind ship|scout).
func inFlightSoldierPath(captainHome string) bool {
	ids, err := inFlightSoldierIDs(captainHome)
	return err == nil && len(ids) > 0
}

// WatcherInfo holds the watcher status for a captain during converge reporting.
type WatcherInfo struct {
	Status      WatcherStatus
	CaptainID   string
	CaptainHome string
}

// ConvergeWatcherStatus checks per-captain watcher status during converge.
// Returns WatcherInfo for each captain, including beat staleness details.
func ConvergeWatcherStatus(registered []Info) []WatcherInfo {
	var results []WatcherInfo
	for _, sm := range registered {
		if sm.Home == "" {
			continue
		}
		status := WatcherStatusSummary(sm.Home)
		results = append(results, WatcherInfo{
			Status:      status,
			CaptainID:   sm.ID,
			CaptainHome: sm.Home,
		})
	}
	return results
}
