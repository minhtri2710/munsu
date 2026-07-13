// Package task manages task lifecycle data, including reading and writing
// task meta files (key=value lines stored at $MUNSU_HOME/state/<id>.meta).
package task

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/home"
)

// StateDir returns the path to the state directory under the given homeDir.
func StateDir(homeDir string) string {
	return filepath.Join(homeDir, "state")
}

// metaPath returns the path to the meta file for the given task ID.
func metaPath(id string) (string, error) {
	h, err := home.Resolve("")
	if err != nil {
		return "", err
	}
	return filepath.Join(StateDir(h), id+".meta"), nil
}

// statusPath returns the path to the status file for the given task ID.
func statusPath(id string) (string, error) {
	h, err := home.Resolve("")
	if err != nil {
		return "", err
	}
	return filepath.Join(StateDir(h), id+".status"), nil
}

// WriteMeta writes a task meta file at $MUNSU_HOME/state/<id>.meta.
// The map is serialized as key=value lines, one per field.
func WriteMeta(id string, meta map[string]string) error {
	p, err := metaPath(id)
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
func ReadMeta(id string) (map[string]string, error) {
	p, err := metaPath(id)
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
func AppendStatus(id, line string) error {
	p, err := statusPath(id)
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
func ReadStatus(id string) ([]string, error) {
	p, err := statusPath(id)
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

// ValidMetaFields lists the recognized fields in a task meta file.
var ValidMetaFields = []string{
	"window", "worktree", "project", "harness",
	"model", "effort", "kind", "mode", "yolo",
}
