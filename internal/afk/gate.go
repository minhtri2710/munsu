package afk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// IsReturnSignal checks whether a line is an unmarked, non-/afk message
// that triggers a captain-return event.
//
// Contract: A marked message (begins with FM_INJECT_MARK U+2063) is a
// daemon escalation, NOT a captain return. An /afk command is explicitly
// about away-mode, not a return. Only unmarked, non-/afk messages qualify
// as return signals.
func IsReturnSignal(line string) bool {
	if line == "" {
		return false
	}
	// Marked messages are daemon escalations, not returns.
	if Marked(line) {
		return false
	}
	// /afk command is about away-mode, not a return.
	if strings.HasPrefix(strings.TrimSpace(line), "/afk") {
		return false
	}
	return true
}

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
