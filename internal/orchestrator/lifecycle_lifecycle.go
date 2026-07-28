// Package lifecycle owns timing and lock invariants for the watcher,
// wake queue, and session lock — defined once, tested once.
// Consumers (supervision, waker, session) import lifecycle and never
// spell state/.wake-queue, state/.last-watcher-beat, or state/.lock
// themselves.
package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

const (
	lockFile      = "state/.lock"
	watchLockFile = "state/.watch.lock"
)

type WakeRecord = home.WakeRecord
type BeatStatus = home.WatcherBeatStatus

// StaleThreshold returns the watcher stale grace period (300s).
func StaleThreshold() time.Duration { return home.WatcherStaleThreshold() }

// BeatPath returns the full path to the watcher liveness beat file.
func BeatPath(homeDir string) string { return home.WatcherBeatPath(homeDir) }

// QueuePath returns the full path to the wake queue file.
func QueuePath(homeDir string) string { return home.WakeQueuePath(homeDir) }

// LockPath returns the full path to the session lock file.
func LockPath(homeDir string) string { return filepath.Join(homeDir, lockFile) }

// WatchPath returns the full path to the watch lock file.
func WatchPath(homeDir string) string { return filepath.Join(homeDir, watchLockFile) }

// --- Lock operations ---

// AcquireSession attempts to acquire an exclusive file lock for the given home.
// Returns true if the lock was acquired, false if held by another process.
// If the lock file exists but the holding PID is no longer running, the lock
// is cleared (stale lock recovery).
func AcquireSession(homeDir string) (bool, error) {
	path := LockPath(homeDir)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, fmt.Errorf("creating lock directory %s: %w", dir, err)
	}

	// Stale lock recovery: if lock file contains a dead PID, or a PID
	// whose process is a munsu watch (legacy shared lock), clear it.
	if pid := readLockPID(path); pid > 0 {
		if !isLifecycleProcessAlive(pid) {
			fmt.Fprintf(os.Stderr, "WARNING: stale session lock from dead PID %d — clearing\n", pid)
			os.Remove(path)
		} else if isWatchProcess(pid) {
			fmt.Fprintf(os.Stderr, "WARNING: session lock held by watcher PID %d — clearing (legacy .lock, use .watch.lock)\n", pid)
			os.Remove(path)
		}
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return false, fmt.Errorf("opening lock file %s: %w", path, err)
	}
	if err := lockExclusive(f, true); err != nil {
		f.Close()
		return false, nil
	}
	fmt.Fprintf(f, "%d\n", os.Getpid())
	// Leak the FD intentionally — held for the lifetime.
	// ReleaseSession closes it via a separate OpenFile + unlock.
	return true, nil
}

// readLockPID reads the PID from the lock file if present.
// Returns 0 if the file cannot be read or parsed.
func readLockPID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil {
		return 0
	}
	return pid
}

// isProcessAlive checks whether a process with the given PID is running.
// Uses kill -0 which tests existence without sending a signal.
func isLifecycleProcessAlive(pid int) bool {
	cmd := exec.Command("kill", "-0", strconv.Itoa(pid))
	return cmd.Run() == nil
}

// ReleaseSession releases the exclusive file lock for the given home.
func ReleaseSession(homeDir string) error {
	path := LockPath(homeDir)
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("opening lock file %s: %w", path, err)
	}
	defer f.Close()
	if err := unlockFile(f); err != nil {
		return fmt.Errorf("unlocking %s: %w", path, err)
	}
	return nil
}

// IsSessionLocked checks whether the lock is held by another process.
func IsSessionLocked(homeDir string) bool {
	path := LockPath(homeDir)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := lockExclusive(f, true); err != nil {
		return true // someone else holds it
	}
	unlockFile(f)
	return false
}

// --- Watch lock operations ---

// AcquireWatch attempts to acquire an exclusive file lock (flock) on the
// watch lock file (state/.watch.lock). Returns true if acquired, false if
// another process holds the lock.
func AcquireWatch(homeDir string) (bool, error) {
	path := WatchPath(homeDir)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, fmt.Errorf("creating watch lock directory %s: %w", dir, err)
	}

	// Stale lock recovery: if lock file contains a dead PID, clear it
	if pid := readLockPID(path); pid > 0 {
		if !isLifecycleProcessAlive(pid) {
			fmt.Fprintf(os.Stderr, "WARNING: stale watch lock from dead PID %d — clearing\n", pid)
			os.Remove(path)
		}
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return false, fmt.Errorf("opening watch lock file %s: %w", path, err)
	}
	if err := lockExclusive(f, true); err != nil {
		f.Close()
		return false, nil
	}
	fmt.Fprintf(f, "%d\n", os.Getpid())
	// Leak the FD intentionally — held for the lifetime.
	// ReleaseWatch closes it via a separate OpenFile + unlock.
	return true, nil
}

// ReleaseWatch releases the exclusive file lock for the watch daemon.
func ReleaseWatch(homeDir string) error {
	path := WatchPath(homeDir)
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("opening watch lock file %s: %w", path, err)
	}
	defer f.Close()
	if err := unlockFile(f); err != nil {
		return fmt.Errorf("unlocking %s: %w", path, err)
	}
	return nil
}

// IsWatchLocked checks whether the watch lock is held by another process.
func IsWatchLocked(homeDir string) bool {
	path := WatchPath(homeDir)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := lockExclusive(f, true); err != nil {
		return true // someone else holds it
	}
	unlockFile(f)
	return false
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
