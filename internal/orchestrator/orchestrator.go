// Package orchestrator provides supervision loops, wake queue management,
// AFK away-mode supervision daemon, mailbox outbox, and uplink reports.
package orchestrator

import (
	"time"

		"github.com/minhtri2710/munsu/internal/lifecycle"
)

// Re-export lifecycle & waker helpers
type WakeRecord = lifecycle.WakeRecord
type BeatStatus = lifecycle.BeatStatus

func BeatPath(homeDir string) string  { return lifecycle.BeatPath(homeDir) }
func QueuePath(homeDir string) string { return lifecycle.QueuePath(homeDir) }
func LockPath(homeDir string) string  { return lifecycle.LockPath(homeDir) }

func EnqueueWake(homeDir, kind, key, payload string) error {
	return lifecycle.EnqueueWake(homeDir, kind, key, payload)
}

func DrainWakes(homeDir string) ([]WakeRecord, error) {
	records, err := Drain(homeDir)
	if err != nil {
		return nil, err
	}
	out := make([]WakeRecord, len(records))
	for i, r := range records {
		out[i] = WakeRecord(r)
	}
	return out, nil
}

func HasQueuedWakes(homeDir string) bool {
	return lifecycle.HasQueuedWakes(homeDir)
}

// StartAFK starts the AFK away-mode daemon.
func StartAFK(homeDir string) error {
	_ = lifecycle.ReadBeatStatus(homeDir, time.Now())
	return Start(homeDir)
}

func IsAFKActive(homeDir string) bool {
	return IsActive(homeDir)
}

func DisableAFK(homeDir string) error {
	return Disable(homeDir)
}
