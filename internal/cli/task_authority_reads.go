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

// committedTaskAuthority is taskAuthorityForRead without journal recovery
// (home.OpenCommitted), for reads from a fenced soldier seat.
func committedTaskAuthority(homeDir string) (*taskauthority.Canonical, error) {
	h, err := home.OpenCommitted(homeDir)
	if err != nil {
		return nil, fmt.Errorf("opening task authority home %s: %w", homeDir, err)
	}
	return taskauthority.NewCanonical(h)
}

// taskDataDirReclaimer runs session-start reclamation under the task's Task
// Authority scope lock. Teardown retains the task data directory; reclamation
// removes it only after the retention grace period, and for an unknown task
// only when the directory holds no brief.
func taskDataDirReclaimer(homeDir string) bootstrap.ReclaimTaskDataDir {
	auth, err := taskAuthorityForRead(homeDir)
	if err != nil {
		return func(string, func() error) (bool, error) { return false, nil }
	}
	return func(id string, reclaim func() error) (bool, error) {
		return auth.ReclaimReleasedTaskArtifactsByID(id, reclaim)
	}
}
