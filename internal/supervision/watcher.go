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

	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

const pollInterval = 5 * time.Second

// WakeReason describes why the watcher exited.
type WakeReason struct {
	Kind    string // signal, stale, check, heartbeat
	TaskIDs []string
	Message string
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

			reason := scanFleet(homeDir)
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

// scanFleet checks all live tasks for actionable events.
func scanFleet(homeDir string) *WakeReason {
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
		bk := session.Default()
		alive := bk.Alive(windowID)

		if !alive {
			return &WakeReason{
				Kind:    "stale",
				TaskIDs: []string{id},
				Message: fmt.Sprintf("pane %s is dead", windowID),
			}
		}

		// Check status log for recent activity
		statusPath := filepath.Join(homeDir, "state", id+".status")
		if fi, err := os.Stat(statusPath); err == nil {
			age := time.Since(fi.ModTime())
			if age > lifecycle.StaleThreshold() {
				return &WakeReason{
					Kind:    "stale",
					TaskIDs: []string{id},
					Message: fmt.Sprintf("pane %s idle for %v", windowID, age.Round(time.Second)),
				}
			}
		}

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
