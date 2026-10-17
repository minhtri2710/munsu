package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

const (
	afkFlagFile  = "state/.afk"
	pollInterval = 30 * time.Second
)

// IsActive reports whether the consent flag is set and a daemon holds the AFK
// lock. A flag left by a daemon that died without cleanup is ignored; an
// unanswerable lock probe counts as active, keeping wakes queued.
func IsActive(homeDir string) bool {
	if _, err := os.Stat(filepath.Join(homeDir, afkFlagFile)); err != nil {
		return false
	}
	pid, err := home.ReadAFKLockPID(homeDir)
	return err != nil || pid > 0
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
