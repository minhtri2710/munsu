// Package home resolves a munsu home and owns domain-neutral durable mechanics:
// scoped fenced locks, leases and atomic journaled commits.
//
// Layout under a home: state/ (task-authority/ documents, fleet-registry/
// registries, the .meta and .status projections with the <id>.meta.lock
// file, the .lock, .wake-claim.lock, .watch.lock and .afk.lock files, wakes and report cursors), data/<id>/
// brief.md, git-shim/bin (the git shim on every seat PATH), config/ (typed documents and the published snapshot), projects/
// (cloned project repositories), captains/ (Captain homes), .journal/ (the
// write-ahead journal) and .lock/ (scoped lock files). Durable paths are
// logical keys that home resolves inside the root; a link along the path that
// resolves outside the root is refused, and links that stay inside are followed.
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
