// Package home resolves a munsu home and owns domain-neutral durable mechanics:
// scoped fenced locks, leases and atomic journaled commits.
//
// Layout under a home: state/ (task-authority/ documents, fleet-registry/
// registries, shim/bin, the .meta and .status projections, locks, wakes and
// receipts), data/<id>/brief.md, config/ (typed documents and the published
// snapshot), projects/ (project worktrees) and captains/ (Captain homes).
// Durable paths are logical keys that home resolves inside the root without
// following links.
package home

import (
	"fmt"
	"os"
	"path/filepath"
)

const DefaultDirName = ".munsu"

// Resolve determines the munsu home directory using the following precedence:
// 1. The override path (typically from --home flag)
// 2. MUNSU_HOME environment variable
// 3. Default ~/.munsu
func Resolve(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	if env := os.Getenv("MUNSU_HOME"); env != "" {
		return filepath.Abs(env)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(homeDir, DefaultDirName), nil
}
