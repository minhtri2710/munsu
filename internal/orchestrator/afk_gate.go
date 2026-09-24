package orchestrator

import (
	"path/filepath"
	"strings"
)

// IsClean checks whether any actionable AFK state remains in the
// durable digest queue. Returns true when nothing needs munsu
// attention and normal work can resume.
//
// Actionable state includes:
//   - Any non-routine escalation entries
//   - Any wedge alarm
//   - Any blocked item
//
// An absent digest is clean. An unreadable or unparseable digest is an
// error: it may hold escalations, so it never reads as clean.
func IsClean(homeDir string) (bool, error) {
	be, err := readDigestFile(filepath.Join(homeDir, digestFile))
	if err != nil {
		return false, err
	}
	if be == nil {
		return true, nil
	}

	// Check for wedge alarms.
	if be.WedgeAlarm != nil {
		return false, nil
	}

	// Check for non-routine entries or blocked items.
	for _, entry := range be.Entries {
		if entry.Type != EscalationRoutine {
			return false, nil
		}
		lower := strings.ToLower(entry.Payload)
		if strings.HasPrefix(lower, "blocked:") || strings.Contains(lower, "\nblocked:") {
			return false, nil
		}
	}

	return true, nil
}
