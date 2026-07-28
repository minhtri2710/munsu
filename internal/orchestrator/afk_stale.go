package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ClearStaleArtifacts removes session-scoped stale artifact files from the
// given home's state directory. It mirrors the
// fm_afk_clear_stale_artifacts pattern which runs on a fresh away-session entry.
//
// Removed artifacts:
//   - state/.seen-*             — watcher dedup markers from a prior session
//   - state/.subsuper-*         — subsupervisor escalation artifacts
//   - state/.afk-digest         — any prior digest that was never consumed
//   - state/.afk-wedge-alarm    — any prior wedge alarm marker
//
// Only operates within the given home directory — never touches sibling homes
// or paths outside stateDir.
func ClearStaleArtifacts(homeDir string) error {
	stateDir := filepath.Join(homeDir, "state")

	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no state dir is fine
		}
		return fmt.Errorf("reading state directory %s: %w", stateDir, err)
	}

	var removeErr error
	for _, entry := range entries {
		name := entry.Name()
		if isStaleArtifact(name) {
			path := filepath.Join(stateDir, name)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				removeErr = fmt.Errorf("removing stale artifact %s: %w", name, err)
			}
		}
	}

	return removeErr
}

// isStaleArtifact checks whether a filename matches a stale artifact pattern.
func isStaleArtifact(name string) bool {
	// .seen-<task> — watcher dedup markers
	if strings.HasPrefix(name, ".seen-") {
		return true
	}
	// .subsuper-* — subsupervisor escalation artifacts
	if strings.HasPrefix(name, ".subsuper-") {
		return true
	}
	// .afk-digest — prior batched escalation
	if name == ".afk-digest" {
		return true
	}
	// .afk-wedge-alarm — prior wedge marker
	if name == ".afk-wedge-alarm" {
		return true
	}
	return false
}

// ClearStaleCheckedMarkers removes only the .subsuper-* check markers.
// More targeted than ClearStaleArtifacts; used between triage cycles
// to clear stale check markers while preserving .seen-* markers that
// are still relevant within the same session.
func ClearStaleCheckedMarkers(homeDir string) error {
	stateDir := filepath.Join(homeDir, "state")

	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading state directory: %w", err)
	}

	var removeErr error
	for _, entry := range entries {
		name := entry.Name()
		// Only clear .subsuper-stale-* and .subsuper-seen-status-* markers,
		// which are per-cycle check markers that should not persist between
		// triage cycles.
		if strings.HasPrefix(name, ".subsuper-stale-") ||
			strings.HasPrefix(name, ".subsuper-seen-status-") {
			path := filepath.Join(stateDir, name)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				removeErr = fmt.Errorf("removing check marker %s: %w", name, err)
			}
		}
	}

	return removeErr
}
