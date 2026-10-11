// Package event provides a durable, append-only typed event log.
// Events carry a monotonic ID, timestamp, type, producer, optional
// correlation/idempotency key, and a JSON payload.
package orchestrator

import (
	"bufio"
	"fmt"
	"io"
	"math"
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
// is taken as written, with no trimming; the four text fields are decoded with
// home.UnescapeLine, the inverse of what Append writes.
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
	return Record{
		ID:        id,
		Timestamp: ts,
		Type:      home.UnescapeLine(parts[2]),
		Producer:  home.UnescapeLine(parts[3]),
		Key:       home.UnescapeLine(parts[4]),
		Payload:   home.UnescapeLine(parts[5]),
	}, true
}

// highWaterPath names the sidecar that lets nextID skip the part of the log it
// has already scanned.
func highWaterPath(homeDir string) string { return LogPath(homeDir) + ".hwm" }

// readHighWater returns the log offset and largest ID the last Append recorded.
// An absent or unparsable sidecar reads as 0, 0: a full scan.
func readHighWater(homeDir string) (offset int64, maxID uint64) {
	data, err := os.ReadFile(highWaterPath(homeDir))
	if err != nil {
		return 0, 0
	}
	if _, err := fmt.Sscanf(string(data), "%d %d", &offset, &maxID); err != nil || offset < 0 {
		return 0, 0
	}
	return offset, maxID
}

// nextID returns the next monotonic ID (1-based): one past the largest ID in
// the log, and the log size it scanned to. It refuses when the largest ID is already
// the maximum, rather than wrap to 0. Only the bytes past the recorded
// high-water offset are read; the maximum is taken over the whole log, so a log
// whose order is not ID order still yields its true maximum. A log shorter than
// the recorded offset was replaced and is scanned in full. The caller must hold
// the event log lock.
func nextID(homeDir string) (uint64, int64, error) {
	f, err := os.Open(LogPath(homeDir))
	if err != nil {
		if os.IsNotExist(err) {
			return 1, 0, nil
		}
		return 0, 0, fmt.Errorf("reading event log: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, 0, fmt.Errorf("reading event log: %w", err)
	}
	size := info.Size()
	offset, maxID := readHighWater(homeDir)
	if offset > size {
		offset, maxID = 0, 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, 0, fmt.Errorf("reading event log: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(f, size-offset))
	if err != nil {
		return 0, 0, fmt.Errorf("reading event log: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if r, ok := parseRecord(line); ok && r.ID > maxID {
			maxID = r.ID
		}
	}
	if maxID == math.MaxUint64 {
		return 0, 0, fmt.Errorf("event log %s: largest event ID is already %d, no ID is left to assign", LogPath(homeDir), uint64(math.MaxUint64))
	}
	return maxID + 1, size, nil
}

// Append writes a typed event to the event log and returns its ID. The four
// text fields are stored home.EscapeLine-encoded, so a tab or newline in any of
// them stays inside its field. The ID is assigned and written under one
// exclusive lock, so concurrent writers in any process never share an ID. The
// lock is a sibling file, not the log itself: on Windows a LockFileEx lock is
// mandatory and would fail concurrent readers of the log.
func Append(homeDir, eventType, producer, key, payload string) (uint64, error) {
	path := LogPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return 0, fmt.Errorf("creating event log directory: %w", err)
	}

	var id uint64
	err := home.WithExclusiveFileLock(path+".lock", func() error {
		next, scanned, err := nextID(homeDir)
		if err != nil {
			return err
		}
		line := fmt.Sprintf("%d\t%d\t%s\t%s\t%s\t%s\n", next, time.Now().UnixNano(),
			home.EscapeLine(eventType), home.EscapeLine(producer), home.EscapeLine(key), home.EscapeLine(payload))

		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("opening event log: %w", err)
		}
		defer f.Close()
		if _, err := f.WriteString(line); err != nil {
			return fmt.Errorf("writing event: %w", err)
		}
		id = next
		// The sidecar is only a scan shortcut: a failed write costs the next
		// Append a full scan and never the event.
		_ = os.WriteFile(highWaterPath(homeDir), []byte(fmt.Sprintf("%d %d\n", scanned+int64(len(line)), next)), 0644)
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
