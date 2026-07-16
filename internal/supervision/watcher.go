// Package supervision provides the event-driven watcher backbone.
package supervision

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/minhtri2710/munsu/internal/crewstate"
	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

const pollInterval = 5 * time.Second

// WakeReason describes why the watcher exited.
type WakeReason struct {
	Kind                 string // signal, stale, check, heartbeat
	TaskIDs              []string
	Message              string
	DemandDeepInspection bool // set after N consecutive stale polls for same task
}

// Run starts the watcher loop. It acquires the watcher lock and polls
// until an actionable wake is found, then exits with the reason.
func Run(homeDir string) (*WakeReason, error) {
	// Acquire watcher lock
	acquired, err := lifecycle.AcquireSession(homeDir)
	if err != nil {
		return nil, fmt.Errorf("watcher lock: %w", err)
	}
	if !acquired {
		return nil, fmt.Errorf("another watcher is already running")
	}
	defer lifecycle.ReleaseSession(homeDir)

	// Handle SIGTERM for graceful cleanup
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Touch liveness beacon
	lifecycle.WriteBeat(homeDir)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			return &WakeReason{Kind: "signal", Message: "watcher interrupted"}, nil

		case <-ticker.C:
			lifecycle.WriteBeat(homeDir)
			reason := ScanFleet(homeDir)

			if reason != nil {
				return reason, nil
			}
		}
	}
}

// ArmBackground launches the watcher as a background process.
// If restart is true, signals any existing watcher first.
func ArmBackground(homeDir string, restart bool) error {
	if restart {
		// Signal existing watcher via its beat file
		_, pid, ok := lifecycle.ReadBeat(homeDir)
		if ok && pid > 0 {
			proc, err := os.FindProcess(pid)
			if err == nil {
				proc.Signal(syscall.SIGTERM)
				time.Sleep(500 * time.Millisecond)
			}
		}
	}

	// Fork a child process
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding munsu binary: %w", err)
	}

	cmd := exec.Command(execPath, "watch")
	cmd.Dir = homeDir
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting watcher: %w", err)
	}

	fmt.Printf("Watcher armed (pid %d)\n", cmd.Process.Pid)
	return nil
}

var (
	// staleStreaks tracks consecutive stale polls per task ID.
	// Persists across scanFleet calls within the watcher loop.
	staleStreaks = map[string]int{}

	// consecutiveStaleThreshold is the number of consecutive stale polls
	// before demanding deep inspection (3 polls * 5s = ~15s).
	consecutiveStaleThreshold = 3
)

// ScanFleet checks all live tasks for actionable events.
// It absorbs stale signals for tasks with an active no-mistakes run
// and tracks per-task stale streaks for demand-deep-inspection.
func ScanFleet(homeDir string) *WakeReason {
	metasDir := filepath.Join(homeDir, "state")
	entries, err := os.ReadDir(metasDir)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".meta") {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".meta")

		// Read meta
		meta, err := task.ReadMeta(homeDir, id)
		if err != nil {
			continue
		}

		windowID, hasWindow := meta["window"]
		if !hasWindow {
			continue
		}

		// Check pane liveness
		bk, _, err := session.BackendForTask(homeDir, meta)
		if err != nil {
			continue
		}
		alive := bk.Alive(windowID)

		if !alive {
			// Before raising stale, check if no-mistakes is actively running.
			// The crewmate may be driving the no-mistakes pipeline even though
			// the session pane appears dead.
			if isNoMistakesActive(homeDir, id) {
				resetStreak(id)
				continue
			}
			return handleStale(id, fmt.Sprintf("pane %s is dead", windowID))
		}

		// Check status log for recent activity
		statusPath := filepath.Join(homeDir, "state", id+".status")
		if fi, err := os.Stat(statusPath); err == nil {
			age := time.Since(fi.ModTime())
			if age > lifecycle.StaleThreshold() {
				// Before raising stale, check absorb.
				if isNoMistakesActive(homeDir, id) {
					resetStreak(id)
					continue
				}
				return handleStale(id, fmt.Sprintf("pane %s idle for %v", windowID, age.Round(time.Second)))
			}
		}

		// Task is healthy or absorbed — reset streak
		resetStreak(id)

		// Check wake queue
		if lifecycle.HasQueuedWakes(homeDir) {
			return &WakeReason{
				Kind:    "signal",
				TaskIDs: []string{id},
				Message: "queued wake records present",
			}
		}
	}

	return nil
}

// handleStale creates a stale WakeReason with streak tracking.
// After consecutiveStaleThreshold consecutive stale polls for the same task,
// it marks the reason as demanding deep inspection.
func handleStale(id, msg string) *WakeReason {
	staleStreaks[id]++
	count := staleStreaks[id]

	reason := &WakeReason{
		Kind:    "stale",
		TaskIDs: []string{id},
		Message: msg,
	}

	if count >= consecutiveStaleThreshold {
		reason.DemandDeepInspection = true
		reason.Message += "; demand-deep-inspection"
	}

	return reason
}

// resetStreak clears the stale streak counter for a task.
// Called when a task is provably working or its status changes.
func resetStreak(id string) {
	delete(staleStreaks, id)
}

// isNoMistakesActive checks whether the task has an active no-mistakes
// run-step that indicates it is provably working. Tasks driving the
// no-mistakes pipeline (running, fixing, ci, fix_review, awaiting_approval)
// should not trigger stale wakes.
func isNoMistakesActive(homeDir, id string) bool {
	s, err := crewstate.Read(homeDir, id)
	if err != nil {
		return false
	}
	return absorbStaleSignal(s)
}

// absorbStaleSignal returns true when the crewmate state has an active
// no-mistakes run-step that should absorb a stale signal.
func absorbStaleSignal(s *crewstate.State) bool {
	if s == nil {
		return false
	}
	switch s.NoMistakesRunStep {
	case "running", "fixing", "ci", "fix_review", "awaiting_approval":
		return true
	}
	return false
}
