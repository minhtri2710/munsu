// Package task manages task lifecycle data, including reading and writing
// task meta files (key=value lines stored at $MUNSU_HOME/state/<id>.meta).
package task

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StateDir returns the path to the state directory under the given homeDir.
func StateDir(homeDir string) string {
	return filepath.Join(homeDir, "state")
}

// metaPath returns the path to the meta file for the given task ID.
func metaPath(homeDir string, id string) (string, error) {
	return filepath.Join(StateDir(homeDir), id+".meta"), nil
}

// statusPath returns the path to the status file for the given task ID.
func statusPath(homeDir string, id string) (string, error) {
	return filepath.Join(StateDir(homeDir), id+".status"), nil
}

// WriteMeta writes a task meta file at $MUNSU_HOME/state/<id>.meta.
// The map is serialized as key=value lines, one per field.
func WriteMeta(homeDir string, id string, meta map[string]string) error {
	p, err := metaPath(homeDir, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	var b strings.Builder
	for k, v := range meta {
		b.WriteString(fmt.Sprintf("%s=%s\n", k, v))
	}
	return os.WriteFile(p, []byte(b.String()), 0644)
}

// ReadMeta reads a task meta file at $MUNSU_HOME/state/<id>.meta.
// Each key=value line is parsed into the returned map.
// Returns an error if the file does not exist.
func ReadMeta(homeDir string, id string) (map[string]string, error) {
	p, err := metaPath(homeDir, id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("reading task meta %s: %w", id, err)
	}
	defer f.Close()

	meta := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			meta[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning task meta %s: %w", id, err)
	}
	return meta, nil
}

// AppendStatus appends a status line to $MUNSU_HOME/state/<id>.status.
func AppendStatus(homeDir string, id, line string) error {
	p, err := statusPath(homeDir, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening status file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("writing status line: %w", err)
	}
	return nil
}

// ReadStatus reads all status lines from $MUNSU_HOME/state/<id>.status.
func ReadStatus(homeDir string, id string) ([]string, error) {
	p, err := statusPath(homeDir, id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no status yet
		}
		return nil, fmt.Errorf("reading status %s: %w", id, err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// ValidStatusStates lists the recognized status states.
var ValidStatusStates = []string{
	"working", "needs-decision", "blocked", "paused", 
	"awaiting_approval", "resolved", "done", "failed",
}

// IsValidStatusState checks whether the given state is recognized.
func IsValidStatusState(state string) bool {
	for _, s := range ValidStatusStates {
		if s == state {
			return true
		}
	}
	return false
}

// ParseStatusKey extracts an optional [key=<slug>] annotation from a status message.
// Returns the cleaned message and the key (empty if none).
func ParseStatusKey(line string) (message, key string) {
	// Format: state: message [key=<slug>]
	// Or just [key=<slug>] at the end.
	// Try with space prefix first: " [key="
	startMarker := " [key="
	idx := strings.LastIndex(line, startMarker)
	if idx < 0 {
		// Try without space: "[key=" at start
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

// RemoveStatusKey removes the [key=<slug>] suffix from a line if present.
func RemoveStatusKey(line string) string {
	msg, _ := ParseStatusKey(line)
	return msg
}

// PromoteMeta flips a task's kind from scout to ship in the meta file.
func PromoteMeta(homeDir string, id string) error {
	meta, err := ReadMeta(homeDir, id)
	if err != nil {
		return fmt.Errorf("reading meta for promote: %w", err)
	}
	if meta["kind"] != "scout" {
		return fmt.Errorf("task %s has kind=%q, can only promote kind=scout", id, meta["kind"])
	}
	meta["kind"] = "ship"
	return WriteMeta(homeDir, id, meta)
}

// ValidMetaFields lists the recognized fields in a task meta file.
var ValidMetaFields = []string{
	"window", "worktree", "project", "harness",
	"model", "effort", "kind", "mode", "yolo",
}
