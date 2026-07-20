package delivery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/minhtri2710/munsu/internal/captain"
	"github.com/minhtri2710/munsu/internal/task"
)

// ResolveTaskHome finds which munsu home owns state/<id>.meta.
//
// Lookup order:
//  1. primary homeDir (general or current MUNSU_HOME)
//  2. each registered captain home under homeDir/data/captains.md
//
// This lets the general run delivery pr-check/pr-merge for soldiers that were
// spawned only inside a captain home after backlog handoff (meta never
// mirrored to the parent).
func ResolveTaskHome(homeDir, id string) (taskHome string, meta map[string]string, err error) {
	if homeDir == "" {
		return "", nil, fmt.Errorf("home directory is empty")
	}
	if id == "" {
		return "", nil, fmt.Errorf("task id is empty")
	}

	meta, primaryErr := task.ReadMeta(homeDir, id)
	if primaryErr == nil {
		return homeDir, meta, nil
	}

	registry := filepath.Join(homeDir, "data", "captains.md")
	mates, regErr := captain.ParseRegistry(registry)
	if regErr != nil {
		return "", nil, fmt.Errorf("reading captains registry: %w", regErr)
	}

	searched := []string{homeDir}
	for _, m := range mates {
		ch := m.Home
		if ch == "" || ch == homeDir {
			continue
		}
		searched = append(searched, ch)
		cm, cErr := task.ReadMeta(ch, id)
		if cErr == nil {
			return ch, cm, nil
		}
	}

	// Prefer a clear not-found when the primary miss is absence.
	if errors.Is(primaryErr, os.ErrNotExist) || isNotExist(primaryErr) {
		return "", nil, fmt.Errorf("task meta %s not found in primary home or captain homes (searched: %v)", id, searched)
	}
	return "", nil, fmt.Errorf("task meta %s not found in primary home or captain homes (searched: %v): %w", id, searched, primaryErr)
}

func isNotExist(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	// task.ReadMeta wraps: "reading task meta %s: %w"
	var pe *os.PathError
	if errors.As(err, &pe) {
		return errors.Is(pe.Err, os.ErrNotExist) || os.IsNotExist(pe)
	}
	return os.IsNotExist(err)
}

// RequireShipMeta resolves the task home and requires kind=ship.
func RequireShipMeta(homeDir, id string) (taskHome string, meta map[string]string, err error) {
	taskHome, meta, err = ResolveTaskHome(homeDir, id)
	if err != nil {
		return "", nil, err
	}
	if meta["kind"] != "ship" {
		return "", nil, fmt.Errorf("task %s has kind=%q, delivery requires kind=ship (promote scout tasks first)", id, meta["kind"])
	}
	return taskHome, meta, nil
}
