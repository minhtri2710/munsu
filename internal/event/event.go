// Package event provides a durable, append-only typed event log.
// Events carry a monotonic ID, timestamp, type, producer, optional
// correlation/idempotency key, and a JSON payload.
package event

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
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

// nextID reads the event log and returns the next monotonic ID (1-based).
// Scans the last line for the current max ID.
func nextID(homeDir string) uint64 {
	path := LogPath(homeDir)
	f, err := os.Open(path)
	if err != nil {
		return 1
	}
	defer f.Close()

	var maxID uint64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 6)
		if len(parts) < 6 {
			continue
		}
		id, err := strconv.ParseUint(parts[0], 10, 64)
		if err == nil && id > maxID {
			maxID = id
		}
	}
	return maxID + 1
}

// Append writes a typed event to the event log with a monotonic ID.
// Returns the assigned event ID.
func Append(homeDir, eventType, producer, key, payload string) (uint64, error) {
	path := LogPath(homeDir)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, fmt.Errorf("creating event log directory: %w", err)
	}

	id := nextID(homeDir)
	ts := time.Now().UnixNano()

	line := fmt.Sprintf("%d\t%d\t%s\t%s\t%s\t%s\n", id, ts, eventType, producer, key, payload)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return 0, fmt.Errorf("opening event log: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(line); err != nil {
		return 0, fmt.Errorf("writing event: %w", err)
	}
	return id, nil
}

// AppendWithID writes a typed event with an explicit ID (for synthetic/replay events).
func AppendWithID(homeDir string, id uint64, eventType, producer, key, payload string) error {
	path := LogPath(homeDir)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating event log directory: %w", err)
	}

	ts := time.Now().UnixNano()
	line := fmt.Sprintf("%d\t%d\t%s\t%s\t%s\t%s\n", id, ts, eventType, producer, key, payload)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening event log: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("writing event: %w", err)
	}
	return nil
}

// ReadAll reads all events from the log, ordered by ID.
func ReadAll(homeDir string) ([]Record, error) {
	path := LogPath(homeDir)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening event log: %w", err)
	}
	defer f.Close()

	var records []Record
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 6)
		if len(parts) < 6 {
			continue
		}
		id, _ := strconv.ParseUint(parts[0], 10, 64)
		ts, _ := strconv.ParseInt(parts[1], 10, 64)
		records = append(records, Record{
			ID:        id,
			Timestamp: ts,
			Type:      parts[2],
			Producer:  parts[3],
			Key:       parts[4],
			Payload:   parts[5],
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning event log: %w", err)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].ID < records[j].ID
	})
	return records, nil
}

// ReadFrom reads events with ID >= fromID.
func ReadFrom(homeDir string, fromID uint64) ([]Record, error) {
	all, err := ReadAll(homeDir)
	if err != nil {
		return nil, err
	}
	var result []Record
	for _, r := range all {
		if r.ID >= fromID {
			result = append(result, r)
		}
	}
	return result, nil
}

var syntheticID atomic.Uint64

// SyntheticEventID generates a synthetic event ID for events derived
// from legacy task status lines. The ID is distinct from on-disk IDs
// by using a high offset (1<<48).
func SyntheticEventID() uint64 {
	const syntheticBase uint64 = 1 << 48
	return syntheticBase + syntheticID.Add(1)
}

// FromTaskStatus converts a legacy task status line into an event record.
// Format: "state: message [key=<slug>]"
// Derived events use synthetic IDs and "task.status" type.
func FromTaskStatus(homeDir, taskID, statusLine string) (Record, error) {
	msg, key := parseStatusLine(statusLine)
	return Record{
		ID:        SyntheticEventID(),
		Timestamp: time.Now().UnixNano(),
		Type:      "task.status",
		Producer:  taskID,
		Key:       key,
		Payload:   msg,
	}, nil
}

// parseStatusLine extracts the message and optional [key=<slug>] from a status line.
func parseStatusLine(line string) (message, key string) {
	startMarker := " [key="
	idx := strings.LastIndex(line, startMarker)
	if idx < 0 {
		startMarker = "[key="
		idx = strings.LastIndex(line, startMarker)
	}
	if idx >= 0 {
		end := strings.Index(line[idx+len(startMarker):], "]")
		if end >= 0 {
			keyVal := line[idx+len(startMarker) : idx+len(startMarker)+end]
			if keyVal != "" {
				key = keyVal
				message = strings.TrimSpace(line[:idx])
				return
			}
		}
	}
	return line, ""
}
