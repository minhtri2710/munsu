// Package afk provides away-mode supervision (sub-supervisor daemon).
package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	afkFlagFile  = "state/.afk"
	pollInterval = 30 * time.Second
)

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
