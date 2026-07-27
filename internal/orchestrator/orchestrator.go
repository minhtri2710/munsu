// Package orchestrator provides supervision loops, wake queue management,
// AFK away-mode supervision daemon, mailbox outbox, and uplink reports.
package orchestrator

import (
	"time"

	"github.com/minhtri2710/munsu/internal/afk"
	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/supervision"
	"github.com/minhtri2710/munsu/internal/waker"
)

// Re-export lifecycle & waker helpers
type WakeRecord = lifecycle.WakeRecord
type BeatStatus = lifecycle.BeatStatus
type WakerRecord = waker.Record

func BeatPath(homeDir string) string  { return lifecycle.BeatPath(homeDir) }
func QueuePath(homeDir string) string { return lifecycle.QueuePath(homeDir) }
func LockPath(homeDir string) string  { return lifecycle.LockPath(homeDir) }

func EnqueueWake(homeDir, kind, key, payload string) error {
	return lifecycle.EnqueueWake(homeDir, kind, key, payload)
}

func DrainWakes(homeDir string) ([]waker.Record, error) {
	return waker.Drain(homeDir)
}

func HasQueuedWakes(homeDir string) bool {
	return lifecycle.HasQueuedWakes(homeDir)
}

func RunWatcher(homeDir string) (*supervision.WakeReason, error) {
	return supervision.Run(homeDir)
}

func RunWatcherWithProbe(homeDir string, probe supervision.TaskEndpointProbe) (*supervision.WakeReason, error) {
	return supervision.RunWithProbe(homeDir, probe)
}

// StartAFK starts the AFK away-mode daemon.
func StartAFK(homeDir string) error {
	_ = lifecycle.ReadBeatStatus(homeDir, time.Now())
	return afk.Start(homeDir)
}

func IsAFKActive(homeDir string) bool {
	return afk.IsActive(homeDir)
}

func DisableAFK(homeDir string) error {
	return afk.Disable(homeDir)
}
