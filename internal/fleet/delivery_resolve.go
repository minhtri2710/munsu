package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// ResolveTaskHome finds which munsu home holds the current canonical record of
// the requested logical task ID. A handed-off task's source record is no
// longer current, so it never wins.
//
// Lookup order:
//  1. primary homeDir (general or current MUNSU_HOME)
//  2. each subdirectory of homeDir/captains/* (captain homes after handoff)
//
// This lets the general run delivery pr-merge for soldiers that were
// spawned only inside a captain home after task handoff.
func ResolveTaskHome(homeDir, id string) (string, error) {
	if homeDir == "" {
		return "", fmt.Errorf("home directory is empty")
	}
	if id == "" {
		return "", fmt.Errorf("task id is empty")
	}

	_, primaryErr := currentCanonical(homeDir, id)
	if primaryErr == nil {
		return homeDir, nil
	}

	searched := []string{homeDir}
	for _, ch := range captainHomes(homeDir) {
		if ch == homeDir {
			continue
		}
		searched = append(searched, ch)
		if _, err := currentCanonical(ch, id); err == nil {
			return ch, nil
		}
	}

	if errors.Is(primaryErr, taskauthority.ErrNotFound) {
		return "", fmt.Errorf("task %s not found in primary home or captain homes (searched: %v)", id, searched)
	}
	return "", fmt.Errorf("task %s not found in primary home or captain homes (searched: %v): %w", id, searched, primaryErr)
}

// captainHomes lists immediate child dirs of <home>/captains.
func captainHomes(homeDir string) []string {
	root := filepath.Join(homeDir, "captains")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "" || e.Name()[0] == '.' {
			continue
		}
		out = append(out, filepath.Join(root, e.Name()))
	}
	return out
}

// RequireShipTask resolves the task home and requires the canonical task to be
// kind=ship.
func RequireShipTask(homeDir, id string) (string, error) {
	taskHome, err := ResolveTaskHome(homeDir, id)
	if err != nil {
		return "", err
	}
	agg, err := currentCanonical(taskHome, id)
	if err != nil {
		return "", fmt.Errorf("resolving kind of task %s: %w", id, err)
	}
	if !taskauthority.DeliversKind(agg.Definition.Kind) {
		return "", fmt.Errorf("task %s has kind=%q, delivery requires kind=ship (promote scout tasks first)", id, agg.Definition.Kind)
	}
	return taskHome, nil
}
