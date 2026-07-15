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

	// Stale lock recovery: if lock file contains a dead PID, clear it
	if pid := readLockPID(path); pid > 0 {
		if !isProcessAlive(pid) {
			fmt.Fprintf(os.Stderr, "WARNING: stale session lock from dead PID %d — clearing\n", pid)
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

// --- Beat operations ---

// WriteBeat writes the current Unix timestamp and PID to the liveness beat file.
func WriteBeat(homeDir string) {
	path := BeatPath(homeDir)
	os.MkdirAll(filepath.Dir(path), 0755)
	content := fmt.Sprintf("%d %d", time.Now().Unix(), os.Getpid())
	os.WriteFile(path, []byte(content), 0644)
}

// ReadBeat reads the timestamp and PID from the liveness beat file.
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

// ReadBeatStatus returns whether the beat exists and whether it is stale
// relative to the stale threshold and the given time.
func ReadBeatStatus(homeDir string, now time.Time) BeatStatus {
	ts, _, ok := ReadBeat(homeDir)
	if !ok {
		return BeatStatus{Exists: false, Stale: true, Age: staleThreshold}
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
