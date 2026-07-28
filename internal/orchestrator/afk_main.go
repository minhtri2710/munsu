// Package afk provides away-mode supervision (sub-supervisor daemon).
package orchestrator

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/minhtri2710/munsu/internal/lifecycle"
)

const (
	afkFlagFile  = "state/.afk"
	pollInterval = 30 * time.Second
)

// seenSet tracks (taskID → lastLine) for deduplication across polls.
// Reset when a line changes; skip when it repeats.
var (
	seenMu    sync.Mutex
	seenLines = make(map[string]string)
)

// Start begins the afk daemon: sets the durable afk flag, then runs a
// supervision loop that batches routine wakes and escalates general-relevant
// events. Blocks until SIGTERM/SIGINT, then clears the flag.
func Start(homeDir string) error {
	flagPath := filepath.Join(homeDir, afkFlagFile)
	if err := os.WriteFile(flagPath, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0644); err != nil {
		return fmt.Errorf("setting afk flag: %w", err)
	}
	fmt.Println("AFK daemon started, flag set")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	stopCh := make(chan struct{})
	go runLoop(homeDir, stopCh)

	<-sigCh
	close(stopCh)
	os.Remove(flagPath)
	fmt.Println("AFK daemon stopped, flag cleared")
	return nil
}

// runLoop is the main supervision loop.
func runLoop(homeDir string, stopCh chan struct{}) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			scanStatusFiles(homeDir)
		}
	}
}

// scanStatusFiles checks for general-relevant events in status files.
func scanStatusFiles(homeDir string) {
	stateDir := filepath.Join(homeDir, "state")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".status") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(stateDir, entry.Name()))
		if err != nil {
			continue
		}

		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) == 0 {
			continue
		}
		taskID := strings.TrimSuffix(entry.Name(), ".status")

		lastLine := strings.TrimSpace(lines[len(lines)-1])
		if lastLine == "" {
			continue
		}

		// Escalate general-relevant states with dedup and wake-queue
		if strings.HasPrefix(lastLine, "done:") ||
			strings.HasPrefix(lastLine, "failed:") ||
			strings.HasPrefix(lastLine, "needs-decision:") {

			seenMu.Lock()
			prev, seen := seenLines[taskID]
			if seen && prev == lastLine {
				seenMu.Unlock()
				continue
			}
			seenLines[taskID] = lastLine
			seenMu.Unlock()

			fmt.Printf("[AFK] %s: %s\n", entry.Name(), lastLine)

			// Append to durable wake queue
			payload := strings.TrimPrefix(lastLine, "done:")
			payload = strings.TrimPrefix(payload, "failed:")
			payload = strings.TrimPrefix(payload, "needs-decision:")
			payload = strings.TrimSpace(payload)
			_ = lifecycle.EnqueueWake(homeDir, "afk", taskID, payload)
		}
	}
}

// IsActive checks if the afk daemon is running (flag file exists).
func IsActive(homeDir string) bool {
	_, err := os.Stat(filepath.Join(homeDir, afkFlagFile))
	return err == nil
}

// ShouldBatch reports whether the AFK daemon is active and should handle
// escalation batching. When true, direct parent pane injection should be skipped
// because the AFK daemon will batch and escalate via its own injection cycle.
func ShouldBatch(homeDir string) bool {
	return IsActive(homeDir)
}

// Status returns the current AFK state: whether the daemon is active,
// and the timestamp from the flag file if it exists.
func Status(homeDir string) (active bool, startedAt string, err error) {
	data, err := os.ReadFile(filepath.Join(homeDir, afkFlagFile))
	if err != nil {
		if os.IsNotExist(err) {
			return false, "", nil
		}
		return false, "", err
	}
	s := strings.TrimSpace(string(data))
	return true, s, nil
}

// Disable idempotently removes the AFK flag file. Returns nil if the flag
// was already absent (no-op). Unlike Start/stop via signal, this does not
// interact with a running daemon — it only clears the durable marker.
func Disable(homeDir string) error {
	err := os.Remove(filepath.Join(homeDir, afkFlagFile))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("disabling afk: %w", err)
	}
	return nil
}
