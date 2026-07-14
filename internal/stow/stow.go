// Package stow provides knowledge-sweep functionality.
package stow

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	KindLearning = "learning"
	KindCaptain  = "captain"
)

// SweepResult describes what was stowed.
type SweepResult struct {
	DataLearnings string // path written, or empty
	DataCaptain   string // path written, or empty
	BacklogNote   string // note written, or empty
}

// Run is shorthand for RunKinded with kind=learning.
func Run(homeDir string, learnings []string) (*SweepResult, error) {
	return RunKinded(homeDir, KindLearning, learnings)
}

// RunKinded stows items of the given kind (learning or captain).
// It reads the target file, merges each new item into existing entries
// (replacing substring matches), then writes the file back.
func RunKinded(homeDir string, kind string, items []string) (*SweepResult, error) {
	res := &SweepResult{}

	if len(items) == 0 {
		return res, nil
	}

	switch kind {
	case KindLearning:
		path := filepath.Join(homeDir, "data", "learnings.md")
		if err := stowFile(path, items); err != nil {
			return res, err
		}
		res.DataLearnings = path

	case KindCaptain:
		path := filepath.Join(homeDir, "data", "captain.md")
		if err := stowFile(path, items); err != nil {
			return res, err
		}
		res.DataCaptain = path

	default:
		return res, fmt.Errorf("unknown stow kind: %q (use %q or %q)", kind, KindLearning, KindCaptain)
	}

	return res, nil
}

// stowFile reads the given markdown file, merges new dated items into
// existing entries, and writes the file back. Entries are created as
// "- DATE: text" lines.
func stowFile(path string, items []string) error {
	entries, err := readEntries(path)
	if err != nil && !errors.Is(err, errMissingFile) {
		return err
	}

	date := time.Now().Format("2006-01-02")

	for _, item := range items {
		text := strings.TrimSpace(item)
		if text == "" {
			continue
		}
		newLine := fmt.Sprintf("- %s: %s", date, text)

		// Try to find and replace an existing entry that this supersedes.
		if !replaceMatching(entries, text, newLine) {
			entries = append(entries, newLine)
		}
	}

	return writeEntries(path, entries)
}

// errMissingFile is returned by readEntries when the file does not exist.
// Callers treat this as an empty file, not a fatal error.
var errMissingFile = errors.New("file does not exist")

// readEntries reads a markdown file and returns non-empty lines.
// Returns (nil, errMissingFile) if the file doesn't exist (lazy creation).
func readEntries(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errMissingFile
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close()

	var entries []string
	scanner := bufio.NewScanner(f)
	// Bump scan buffer to handle long lines (default 64KB).
	scanner.Buffer(make([]byte, 0, 512*1024), 512*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			entries = append(entries, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return entries, nil
}

// writeEntries writes lines to a markdown file, ensuring the parent
// directory exists. Each line gets a trailing newline.
func writeEntries(path string, entries []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", path, err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	defer f.Close()

	for _, line := range entries {
		if _, err := f.WriteString(line + "\n"); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return nil
}

// replaceMatching scans entries for one that contains text as a substring.
// If found, it replaces that entry with newLine and returns true.
// If not found, it returns false (caller should append).
func replaceMatching(entries []string, text string, newLine string) bool {
	lowerText := strings.ToLower(text)
	for i, entry := range entries {
		if strings.Contains(strings.ToLower(entry), lowerText) {
			entries[i] = newLine
			return true
		}
	}
	return false
}

