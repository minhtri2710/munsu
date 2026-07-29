package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const afkLockFile = "state/.lock"

// Lock represents an identity-backed daemon lock for the AFK daemon.
// It stores the PID and start time, enabling idempotent acquire: if the
// lock names a live process, AcquireLock is a no-op. Stale locks (dead PID)
// are silently reclaimed.
type Lock struct {
	pid     int
	startAt time.Time
	path    string
}

// AcquireLock attempts to acquire the AFK daemon lock.
// Returns the lock and true if acquired. If the lock is already held by a
// running process, returns (nil, false, nil) — idempotent no-op.
func AcquireLock(homeDir string) (*Lock, bool, error) {
	lockPath := filepath.Join(homeDir, afkLockFile)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return nil, false, fmt.Errorf("creating lock directory: %w", err)
	}

	// Check existing lock: if it names a live PID, it's still held.
	if data, err := os.ReadFile(lockPath); err == nil {
		pid, startStr := parseLockContent(data)
		if pid > 0 {
			if isProcessAlive(pid) {
				return nil, false, nil
			}
			if startStr != "" {
				fmt.Fprintf(os.Stderr, "afk: stale lock from PID %d (%s) — reclaiming\n", pid, startStr)
			}
		}
	}

	now := time.Now().UTC()
	content := fmt.Sprintf("%d\t%s\n", os.Getpid(), now.Format(time.RFC3339))
	if err := os.WriteFile(lockPath, []byte(content), 0644); err != nil {
		return nil, false, fmt.Errorf("writing lock file: %w", err)
	}

	return &Lock{
		pid:     os.Getpid(),
		startAt: now,
		path:    lockPath,
	}, true, nil
}

// Release releases the lock by removing the lock file.
func (l *Lock) Release() error {
	return os.Remove(l.path)
}

// PID returns the process ID that holds this lock.
func (l *Lock) PID() int { return l.pid }

// StartAt returns the timestamp when this lock was acquired.
func (l *Lock) StartAt() time.Time { return l.startAt }

// parseLockContent extracts PID and start time from lock file content.
// Format: "<pid>\t<RFC3339>\n"
func parseLockContent(data []byte) (pid int, startStr string) {
	s := strings.TrimSpace(string(data))
	parts := strings.SplitN(s, "\t", 2)
	if len(parts) < 1 {
		return 0, ""
	}
	p, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || p <= 0 {
		return 0, ""
	}
	if len(parts) >= 2 {
		startStr = strings.TrimSpace(parts[1])
	}
	return p, startStr
}

// isProcessAlive checks whether a process with the given PID is running.
func isProcessAlive(pid int) bool {
	cmd := exec.Command("kill", "-0", strconv.Itoa(pid))
	return cmd.Run() == nil
}
