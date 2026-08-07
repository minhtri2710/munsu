package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// ResolveTaskHome finds which munsu home owns state/<id>.meta.
//
// Lookup order:
//  1. primary homeDir (general or current MUNSU_HOME)
//  2. each subdirectory of homeDir/captains/* (captain homes after handoff)
//
// This lets the general run delivery pr-check/pr-merge for soldiers that were
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

	if errors.Is(primaryErr, os.ErrNotExist) || isNotExist(primaryErr) {
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

func isNotExist(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
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

// RequireIdentity resolves a complete, valid delivery identity from the task
// meta projection. It is the read-only identity resolution used by the
// retained provider-neutral status seam; delivery execution itself never
// derives identity truth from .meta (the journal pins the exact typed
// identity and the canonical authorization binds it).
func RequireIdentity(homeDir, id string) (*domain.DeliveryIdentity, error) {
	meta, err := home.ReadMeta(homeDir, id)
	if err != nil {
		return nil, fmt.Errorf("reading task meta for identity: %w", err)
	}

	ident, err := domain.IdentityFromMeta(meta)
	if err != nil {
		return nil, fmt.Errorf("parsing delivery identity: %w", err)
	}
	if ident == nil {
		return nil, fmt.Errorf("no delivery identity found for task %s: PR URL not set in meta; use pr-check to capture identity before destructive actions", id)
	}

	if err := domain.ValidateIdentity(ident); err != nil {
		return nil, fmt.Errorf("incomplete delivery identity for task %s: %w; re-run pr-check to recapture", id, err)
	}

	return ident, nil
}
