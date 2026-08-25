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

// currentTaskExists reports whether one task has a current canonical record
// in the home's authority. It re-expresses the legacy
// home.ReadCurrentTaskAggregate presence check over the canonical authority;
// uninitialized homes fail closed.
func currentTaskExists(homeDir, taskID string) (bool, error) {
	auth, err := taskAuthorityForRead(homeDir)
	if err != nil {
		return false, fmt.Errorf("composing task authority: %w", err)
	}
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return false, err
	}
	_, err = auth.Get(tid)
	if errors.Is(err, taskauthority.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
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

// taskDataDirOwnership answers, for the session-start orphan sweep, whether a
// task still owns its data directory. The sweep cannot answer it from files:
// teardown removes both the .meta and the .status projection, so a directory
// left by a retired task looks exactly like one holding a brief written for a
// task that has not been spawned yet. Only the canonical phase separates them,
// so the composition root resolves it here (ADR-0007 §9).
//
// It fails closed. Only a task the Authority does not know, or a retired task
// whose cleanup claim has reconciled, has released its data directory;
// everything else — including every error — still owns it.
func taskDataDirOwnership(homeDir string) bootstrap.TaskOwnsDataDir {
	auth, err := taskAuthorityForRead(homeDir)
	if err != nil {
		return func(string) bool { return true }
	}
	return func(id string) bool {
		tid, err := domain.NewTaskID(id)
		if err != nil {
			return true
		}
		agg, err := auth.Get(tid)
		if err != nil {
			return !errors.Is(err, taskauthority.ErrNotFound)
		}
		if agg.Phase != taskauthority.PhaseRetired {
			return true
		}
		if claim := agg.CleanupClaim; claim != nil {
			switch claim.Status {
			case taskauthority.CleanupActive:
				return true
			case taskauthority.CleanupCompleted, taskauthority.CleanupAborted:
				return false
			default:
				return true
			}
		}
		return false
	}
}
