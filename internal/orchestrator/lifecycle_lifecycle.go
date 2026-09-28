// Package lifecycle owns timing and lock invariants for the watcher,
// wake queue, and session lock — defined once, tested once.
// Consumers (supervision, waker, session) import lifecycle and never
// spell state/.wake-queue, state/.last-watcher-beat, or state/.lock
// themselves.
package orchestrator

import (
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

type WakeRecord = home.WakeRecord
type BeatStatus = home.WatcherBeatStatus

func StaleThreshold() time.Duration   { return home.WatcherStaleThreshold() }
func QueuePath(homeDir string) string { return home.WakeQueuePath(homeDir) }
func AcquireSession(homeDir string) (bool, error) {
	return home.AcquireSessionLock(homeDir)
}
func IsSessionLocked(homeDir string) bool { return home.IsSessionLockHeld(homeDir) }
func AcquireWatch(homeDir string) (bool, error) {
	return home.AcquireWatchLock(homeDir)
}
func ReleaseWatch(homeDir string) error   { return home.ReleaseWatchLock(homeDir) }
func ReleaseSession(homeDir string) error { return home.ReleaseSessionLock(homeDir) }

// --- Durable beat and queue operations (owned by home) ---
func WriteBeat(homeDir string)                   { home.WriteWatcherBeat(homeDir) }
func ReadBeat(homeDir string) (int64, int, bool) { return home.ReadWatcherBeat(homeDir) }
func ClearBeat(homeDir string)                   { home.ClearWatcherBeat(homeDir) }
func ReadBeatStatus(homeDir string, now time.Time) BeatStatus {
	return home.ReadWatcherBeatStatus(homeDir, now)
}
func EnqueueWake(homeDir, kind, key, payload string) error {
	return home.EnqueueWake(homeDir, kind, key, payload)
}
func DrainWakes(homeDir string) ([]WakeRecord, error) { return home.DrainWakes(homeDir) }
func HasQueuedWakes(homeDir string) bool              { return home.HasQueuedWakes(homeDir) }
