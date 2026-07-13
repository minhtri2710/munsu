// Package stow provides knowledge-sweep functionality.
package stow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SweepResult describes what was stowed.
type SweepResult struct {
	DataLearnings string // path written, or empty
	BacklogNote   string // note written, or empty
}

// Run sweeps the current session for durable knowledge.
// It creates dated entries in data/learnings.md with any captured facts.
func Run(homeDir string, learnings []string) (*SweepResult, error) {
	res := &SweepResult{}

	if len(learnings) == 0 {
		return res, nil
	}

	// Write to data/learnings.md
	learningsPath := filepath.Join(homeDir, "data", "learnings.md")
	os.MkdirAll(filepath.Dir(learningsPath), 0755)

	// Append dated entries
	f, err := os.OpenFile(learningsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return res, fmt.Errorf("opening learnings: %w", err)
	}
	defer f.Close()

	date := time.Now().Format("2006-01-02")
	for _, l := range learnings {
		line := fmt.Sprintf("- %s: %s\n", date, strings.TrimSpace(l))
		if _, err := f.WriteString(line); err != nil {
			return res, err
		}
	}

	res.DataLearnings = learningsPath
	return res, nil
}
