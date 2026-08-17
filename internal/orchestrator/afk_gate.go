package orchestrator

import (
	"encoding/json"
	"os"
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
// An absent or unparseable digest is treated as clean.
func IsClean(homeDir string) bool {
	path := filepath.Join(homeDir, digestFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return true // no digest = clean
	}

	var be BatchedEscalation
	if err := json.Unmarshal(data, &be); err != nil {
		return true // unparseable = treat as clean
	}

	// Check for wedge alarms.
	if be.WedgeAlarm != nil {
		return false
	}

	// Check for non-routine entries or blocked items.
	for _, entry := range be.Entries {
		if entry.Type != EscalationRoutine {
			return false
		}
		lower := strings.ToLower(entry.Payload)
		if strings.HasPrefix(lower, "blocked:") || strings.Contains(lower, "\nblocked:") {
			return false
		}
	}

	return true
}
