// Package event provides a durable, append-only typed event log.
// Events carry a monotonic ID, timestamp, type, producer, optional
// correlation/idempotency key, and a JSON payload.
package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

// Record is a single typed event log entry.
type Record struct {
	ID        uint64 `json:"id"`
	Timestamp int64  `json:"timestamp"` // unix nanos
	Type      string `json:"type"`
	Producer  string `json:"producer"`
	Key       string `json:"key,omitempty"`
	Payload   string `json:"payload"`
}

const eventLogFile = "state/.event-log"

// LogPath returns the full path to the event log file.
func LogPath(homeDir string) string {
	return filepath.Join(homeDir, eventLogFile)
}

// nextID returns the next monotonic ID (1-based): one past the largest ID in
// the log. The caller must hold the event log lock.
func nextID(homeDir string) (uint64, error) {
	data, err := os.ReadFile(LogPath(homeDir))
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, fmt.Errorf("reading event log: %w", err)
	}
	var maxID uint64
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "\t", 6)
		if len(parts) < 6 {
			continue
		}
		id, err := strconv.ParseUint(parts[0], 10, 64)
		if err == nil && id > maxID {
			maxID = id
		}
	}
	return maxID + 1, nil
}

// Append writes a typed event to the event log and returns its ID. The ID is
// assigned and written under one exclusive lock, so concurrent writers in any
// process never share an ID. The lock is a sibling file, not the log itself:
// on Windows a LockFileEx lock is mandatory and would fail concurrent readers
// of the log.
func Append(homeDir, eventType, producer, key, payload string) (uint64, error) {
	path := LogPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return 0, fmt.Errorf("creating event log directory: %w", err)
	}

	var id uint64
	err := home.WithExclusiveFileLock(path+".lock", func() error {
		next, err := nextID(homeDir)
		if err != nil {
			return err
		}
		line := fmt.Sprintf("%d\t%d\t%s\t%s\t%s\t%s\n", next, time.Now().UnixNano(), eventType, producer, key, payload)

		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("opening event log: %w", err)
		}
		defer f.Close()
		if _, err := f.WriteString(line); err != nil {
			return fmt.Errorf("writing event: %w", err)
		}
		id = next
		return nil
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}
