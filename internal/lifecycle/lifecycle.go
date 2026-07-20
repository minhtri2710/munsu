// Package lifecycle owns timing and lock invariants for the watcher,
// wake queue, and session lock — defined once, tested once.
// Consumers (supervision, waker, session) import lifecycle and never
// spell state/.wake-queue, state/.last-watcher-beat, or state/.lock
// themselves.
package lifecycle

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	wakeQueueFile   = "state/.wake-queue"
	watcherBeatFile = "state/.last-watcher-beat"
	lockFile        = "state/.lock"
	watchLockFile   = "state/.watch.lock"
	staleThreshold  = 300 * time.Second // 5 min watcher grace period
)

// WakeRecord represents a single wake queue entry.
type WakeRecord struct {
	Epoch   string
	Seq     string
	Kind    string
	Key     string
	Payload string
}

// BeatStatus summarizes the watcher liveness beat state.
type BeatStatus struct {
	Exists bool
	Stale  bool
	Age    time.Duration
}

// StaleThreshold returns the watcher stale grace period (300s).
func StaleThreshold() time.Duration { return staleThreshold }

// BeatPath returns the full path to the watcher liveness beat file.
func BeatPath(homeDir string) string { return filepath.Join(homeDir, watcherBeatFile) }

// QueuePath returns the full path to the wake queue file.
func QueuePath(homeDir string) string { return filepath.Join(homeDir, wakeQueueFile) }

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
		if !isProcessAlive(pid) {
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
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
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
func isProcessAlive(pid int) bool {
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
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
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
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true // someone else holds it
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
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
		if !isProcessAlive(pid) {
			fmt.Fprintf(os.Stderr, "WARNING: stale watch lock from dead PID %d — clearing\n", pid)
			os.Remove(path)
		}
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return false, fmt.Errorf("opening watch lock file %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
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
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
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
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true // someone else holds it
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
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

// --- Beat operations ---

// WriteBeat writes the current Unix timestamp (content-timestamp that drives
// staleness) and PID to the liveness beat file.
// Format: "<unix_epoch> <pid>" — mtime alone does NOT drive staleness.
func WriteBeat(homeDir string) {
	path := BeatPath(homeDir)
	os.MkdirAll(filepath.Dir(path), 0755)
	content := fmt.Sprintf("%d %d", time.Now().Unix(), os.Getpid())
	os.WriteFile(path, []byte(content), 0644)
}

// ReadBeat reads the content-timestamp and PID from the liveness beat file.
// The content-timestamp (unix epoch) drives staleness; mtime fallback
// is used only if content parse fails.
// Format: "<unix_epoch> <pid>" (or older single-value "<unix_epoch>").
// Returns false for ok if the file cannot be read or parsed.
func ReadBeat(homeDir string) (timestamp int64, pid int, ok bool) {
	data, err := os.ReadFile(BeatPath(homeDir))
	if err != nil {
		return 0, 0, false
	}
	_, err = fmt.Sscanf(strings.TrimSpace(string(data)), "%d %d", &timestamp, &pid)
	if err != nil {
		// Try just timestamp (older single-value format)
		_, err = fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &timestamp)
		if err != nil {
			return 0, 0, false
		}
	}
	return timestamp, pid, true
}

// ClearBeat removes the watcher liveness beat file.
func ClearBeat(homeDir string) {
	os.Remove(BeatPath(homeDir))
}

// ReadBeatStatus returns whether the beat exists and whether it is stale
// relative to the stale threshold and the given time.
// Staleness is driven entirely by the content-timestamp (unix epoch)
// written by WriteBeat. File mtime is NOT authoritative.
// When the beat file does not exist, Age is 0 (signaling never existed).
func ReadBeatStatus(homeDir string, now time.Time) BeatStatus {
	ts, _, ok := ReadBeat(homeDir)
	if !ok {
		return BeatStatus{Exists: false, Stale: true, Age: 0}
	}
	age := now.Sub(time.Unix(ts, 0))
	return BeatStatus{Exists: true, Stale: age > staleThreshold, Age: age}
}

// --- Queue operations ---

// EnqueueWake appends a wake record to the durable wake queue.
func EnqueueWake(homeDir, kind, key, payload string) error {
	qPath := QueuePath(homeDir)
	os.MkdirAll(filepath.Dir(qPath), 0755)
	f, err := os.OpenFile(qPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	line := fmt.Sprintf("%d\t%d\t%s\t%s\t%s\n", time.Now().Unix(), os.Getpid(), kind, key, payload)
	_, err = f.WriteString(line)
	return err
}

// DrainWakes reads all wake records from the queue, removes the queue file,
// and returns the records. Returns nil, nil if no queue file exists.
func DrainWakes(homeDir string) ([]WakeRecord, error) {
	qPath := QueuePath(homeDir)
	f, err := os.Open(qPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var records []WakeRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "\t", 5)
		if len(parts) < 5 {
			continue
		}
		records = append(records, WakeRecord{
			Epoch:   parts[0],
			Seq:     parts[1],
			Kind:    parts[2],
			Key:     parts[3],
			Payload: parts[4],
		})
	}
	os.Remove(qPath)
	return records, scanner.Err()
}

// HasQueuedWakes returns true if the wake queue file exists and has content.
func HasQueuedWakes(homeDir string) bool {
	fi, err := os.Stat(QueuePath(homeDir))
	return err == nil && fi.Size() > 0
}
