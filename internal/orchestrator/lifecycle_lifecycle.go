// Package lifecycle owns timing and lock invariants for the watcher,
// wake queue, and session lock — defined once, tested once.
// Consumers (supervision, waker, session) import lifecycle and never
// spell state/.wake-queue, state/.last-watcher-beat, or state/.lock
// themselves.
package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

type WakeRecord = home.WakeRecord
type BeatStatus = home.WatcherBeatStatus

func StaleThreshold() time.Duration   { return home.WatcherStaleThreshold() }
func BeatPath(homeDir string) string  { return home.WatcherBeatPath(homeDir) }
func QueuePath(homeDir string) string { return home.WakeQueuePath(homeDir) }
func LockPath(homeDir string) string  { return home.SessionLockPath(homeDir) }
func WatchPath(homeDir string) string { return home.WatchLockPath(homeDir) }
func lifecycleLockPolicy() home.WatcherLockPolicy {
	return home.WatcherLockPolicy{ProcessAlive: isLifecycleProcessAlive, IsWatcher: isWatchProcess}
}
func AcquireSession(homeDir string) (bool, error) {
	return home.AcquireSessionLock(homeDir, lifecycleLockPolicy())
}
func IsSessionLocked(homeDir string) bool { return home.IsSessionLockHeld(homeDir) }
func AcquireWatch(homeDir string) (bool, error) {
	return home.AcquireWatchLock(homeDir, lifecycleLockPolicy())
}
func ReleaseWatch(homeDir string) error { return home.ReleaseWatchLock(homeDir) }
func isLifecycleProcessAlive(pid int) bool {
	return exec.Command("kill", "-0", strconv.Itoa(pid)).Run() == nil
}

// isWatchProcess checks whether the given PID is a munsu watch process.
// Reads /proc/PID/cmdline on Linux (NUL-separated args) and falls back to
// `ps -o command=` on macOS/BSD.
func isWatchProcess(pid int) bool {
	// Linux: read /proc/PID/cmdline (NUL-separated args)
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err == nil {
		args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
		for _, arg := range args {
			if arg == "watch" {
				return true
			}
		}
		return false
	}

	// macOS/BSD fallback: ps shows the full command
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	line := strings.TrimSpace(string(out))
	return strings.Contains(line, "munsu watch") || strings.HasSuffix(line, " watch")
}

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
