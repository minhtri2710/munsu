// Package event provides a durable, append-only typed event log.
// Events carry a monotonic ID, timestamp, type, producer, optional
// correlation/idempotency key, and a JSON payload.
package orchestrator

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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

// parseRecord parses one log line, without its newline, into a Record. The line
// is taken as written, with no trimming.
// It reports false when the line has fewer than six tab-separated fields or an
// ID that is not an unsigned integer. An unparsable timestamp reads as 0.
func parseRecord(line string) (Record, bool) {
	parts := strings.SplitN(line, "\t", 6)
	if len(parts) < 6 {
		return Record{}, false
	}
	id, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return Record{}, false
	}
	ts, _ := strconv.ParseInt(parts[1], 10, 64)
	return Record{ID: id, Timestamp: ts, Type: parts[2], Producer: parts[3], Key: parts[4], Payload: parts[5]}, true
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
		if r, ok := parseRecord(line); ok && r.ID > maxID {
			maxID = r.ID
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

// scanEvents reads complete records in ID order while retaining at most limit
// records. A missing log is empty. Text after the last newline is a writer
// mid-append and is neither a record nor skipped.
func scanEvents(homeDir string, cursor uint64, limit int, latest bool) ([]Record, int, error) {
	f, err := os.Open(LogPath(homeDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("reading event log: %w", err)
	}
	defer f.Close()
	var records []Record
	skipped := 0
	reader := bufio.NewReader(f)
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return nil, 0, fmt.Errorf("reading event log: %w", readErr)
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			continue
		}
		r, ok := parseRecord(line)
		if !ok {
			skipped++
			continue
		}
		if !latest && r.ID <= cursor {
			continue
		}
		// records stays in stable ID order: an in-order record appends, an
		// out-of-order one goes after the retained records with an equal ID.
		i := len(records)
		if i > 0 && r.ID < records[i-1].ID {
			i = sort.Search(i, func(j int) bool { return records[j].ID > r.ID })
		}
		if !latest && i >= limit {
			continue
		}
		records = append(records, Record{})
		copy(records[i+1:], records[i:])
		records[i] = r
		if len(records) > limit {
			if latest {
				records = records[1:]
			} else {
				records = records[:limit]
			}
		}
	}
	return records, skipped, nil
}

// LatestEvents returns up to n of the newest records in id order, and how many
// malformed lines the whole log holds. A missing log yields no records and no
// error; an unreadable log is an error. A torn trailing line (a writer
// mid-append) is never returned and never counted. The log is re-read in full
// on every call.
func LatestEvents(homeDir string, n int) (records []Record, skipped int, err error) {
	return scanEvents(homeDir, 0, n, true)
}

// EventsAfter returns up to limit records with ID greater than cursor, in id
// order, and how many malformed lines the whole log holds. Pass the last
// returned ID as the next cursor. Missing, unreadable and torn-line handling
// is as for LatestEvents.
func EventsAfter(homeDir string, cursor uint64, limit int) (records []Record, skipped int, err error) {
	return scanEvents(homeDir, cursor, limit, false)
}
