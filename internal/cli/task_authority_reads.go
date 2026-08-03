package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthorityfs"
)

// currentTaskGeneration returns the current canonical generation of one task
// from the task-authority Store view, falling back to the caller-provided
// value when the task has no canonical record. It re-expresses the legacy
// home.CurrentTaskGeneration over the v2 authority (Task 8.2): a v1 home
// fails closed with the typed migration-required error instead of reading
// the deleted v1 aggregate store.
func currentTaskGeneration(homeDir, taskID, fallback string) (string, error) {
	store, err := taskauthorityfs.NewStore(homeDir)
	if err != nil {
		return "", err
	}
	view, err := store.View()
	if err != nil {
		return "", err
	}
	if agg, ok := view.Current(taskID); ok {
		return agg.Generation.String(), nil
	}
	return fallback, nil
}

// resolveCurrentTaskID resolves one requested task ID against canonical
// current ownership across the primary home and every captain sub-home
// (Task 8.2 re-expression of the legacy home.ResolveCurrentTaskID over the
// v2 authority). Multiple canonical owners make the ID ambiguous and the
// caller surfaces correction commands; a v1 home fails closed through the
// Store view.
func resolveCurrentTaskID(homeDir, taskID string) (string, error) {
	owners, err := currentTaskOwnerHomes(homeDir, taskID)
	if err != nil {
		return "", err
	}
	if len(owners) > 1 {
		return "", &home.AmbiguousTaskIDError{Requested: taskID, Matches: owners}
	}
	return taskID, nil
}

// currentTaskOwnerHomes collects every home (primary plus captains) whose
// canonical authority view names taskID as a current generation.
func currentTaskOwnerHomes(homeDir, taskID string) ([]string, error) {
	var owners []string
	add := func(dir string) error {
		store, err := taskauthorityfs.NewStore(dir)
		if err != nil {
			return err
		}
		view, err := store.View()
		if err != nil {
			return err
		}
		if _, ok := view.Current(taskID); ok {
			owners = append(owners, dir)
		}
		return nil
	}
	if err := add(homeDir); err != nil {
		return nil, err
	}
	captainsRoot := filepath.Join(homeDir, "captains")
	entries, err := os.ReadDir(captainsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return owners, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := add(filepath.Join(captainsRoot, entry.Name())); err != nil {
			return nil, err
		}
	}
	return owners, nil
}

// currentTaskExists reports whether one task has a current canonical record
// in the home's authority view. It re-expresses the legacy
// home.ReadCurrentTaskAggregate presence check over the v2 authority (Task
// 8.2); v1 homes fail closed.
func currentTaskExists(homeDir, taskID string) (bool, error) {
	store, err := taskauthorityfs.NewStore(homeDir)
	if err != nil {
		return false, fmt.Errorf("composing task authority: %w", err)
	}
	view, err := store.View()
	if err != nil {
		return false, err
	}
	_, ok := view.Current(taskID)
	return ok, nil
}
