package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// ResolveTaskHome finds which munsu home owns the durable-stem projection
// for the requested logical task ID (`state/<durable-stem>.meta`).
//
// Lookup order:
//  1. primary homeDir (general or current MUNSU_HOME)
//  2. each subdirectory of homeDir/captains/* (captain homes after handoff)
//
// This lets the general run delivery pr-merge for soldiers that were
// spawned only inside a captain home after task handoff (meta never
// mirrored to the parent).
func ResolveTaskHome(homeDir, id string) (taskHome string, meta map[string]string, err error) {
	if homeDir == "" {
		return "", nil, fmt.Errorf("home directory is empty")
	}
	if id == "" {
		return "", nil, fmt.Errorf("task id is empty")
	}

	meta, primaryErr := home.ReadMeta(homeDir, id)
	if primaryErr == nil {
		return homeDir, meta, nil
	}

	searched := []string{homeDir}
	for _, ch := range captainHomes(homeDir) {
		if ch == homeDir {
			continue
		}
		searched = append(searched, ch)
		cm, cErr := home.ReadMeta(ch, id)
		if cErr == nil {
			return ch, cm, nil
		}
	}

	if errors.Is(primaryErr, os.ErrNotExist) {
		return "", nil, fmt.Errorf("task meta %s not found in primary home or captain homes (searched: %v)", id, searched)
	}
	return "", nil, fmt.Errorf("task meta %s not found in primary home or captain homes (searched: %v): %w", id, searched, primaryErr)
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

// RequireShipMeta resolves the task home and requires the canonical task to be
// kind=ship.
func RequireShipMeta(homeDir, id string) (taskHome string, meta map[string]string, err error) {
	taskHome, meta, err = ResolveTaskHome(homeDir, id)
	if err != nil {
		return "", nil, err
	}
	agg, err := currentCanonical(taskHome, id)
	if err != nil {
		return "", nil, fmt.Errorf("resolving kind of task %s: %w", id, err)
	}
	if agg.Definition.Kind != taskauthority.KindShip {
		return "", nil, fmt.Errorf("task %s has kind=%q, delivery requires kind=ship (promote scout tasks first)", id, agg.Definition.Kind)
	}
	return taskHome, meta, nil
}
