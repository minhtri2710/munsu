package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// currentTaskGeneration returns the current canonical generation of one task
// from the home's Task Authority, falling back to the caller-provided value
// when the task has no canonical record. It re-expresses the legacy
// home.CurrentTaskGeneration over the canonical authority: an uninitialized
// home fails closed instead of silently initializing state.
func currentTaskGeneration(homeDir, taskID, fallback string) (string, error) {
	auth, err := taskAuthorityForRead(homeDir)
	if err != nil {
		return "", err
	}
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return "", err
	}
	agg, err := auth.Get(tid)
	if errors.Is(err, taskauthority.ErrNotFound) {
		return fallback, nil
	}
	if err != nil {
		return "", err
	}
	return agg.Generation.String(), nil
}

// resolveCurrentTaskID resolves one requested task ID against canonical
// current ownership across the primary home and every captain sub-home
// (re-expression of the legacy home.ResolveCurrentTaskID over the canonical
// authority). Multiple canonical owners make the ID ambiguous and the caller
// surfaces correction commands; an uninitialized home fails closed.
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
// canonical authority names taskID as a current generation.
func currentTaskOwnerHomes(homeDir, taskID string) ([]string, error) {
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return nil, err
	}
	var owners []string
	add := func(dir string) error {
		auth, err := taskAuthorityForRead(dir)
		if err != nil {
			return err
		}
		if _, err := auth.Get(tid); err == nil {
			owners = append(owners, dir)
		} else if !errors.Is(err, taskauthority.ErrNotFound) {
			return err
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

// taskAuthorityForRead composes the canonical Task Authority over an opened
// home for one read-only authority query. Ordinary reads fail closed for an
// uninitialized Home rather than silently initializing state.
func taskAuthorityForRead(homeDir string) (*taskauthority.Canonical, error) {
	h, err := home.Open(homeDir)
	if err != nil {
		return nil, fmt.Errorf("opening task authority home %s: %w", homeDir, err)
	}
	return taskauthority.NewCanonical(h)
}

func taskDataDirReclaimer(homeDir string) bootstrap.ReclaimTaskDataDir {
	auth, err := taskAuthorityForRead(homeDir)
	if err != nil {
		return func(string, func() error) (bool, error) { return false, nil }
	}
	return func(id string, reclaim func() error) (bool, error) {
		return auth.ReclaimReleasedTaskArtifactsByID(id, reclaim)
	}
}
