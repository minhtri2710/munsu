package home

import (
	"fmt"
	"os"
	"path/filepath"
)

// syncTemp flushes the temp file before it is renamed into place; tests
// replace it to prove a write that cannot be synced is never installed.
var syncTemp = (*os.File).Sync

// AtomicWrite durably writes data to path with mode perm via a unique temp
// file in the same directory, an fsync, and RenameDurable, whose durability is
// carried by a parent-directory fsync on unix and a write-through move on
// windows. On success readers observe either the old or the new content, never
// a partial write. The parent directory must already exist; callers own its
// mode.
func AtomicWrite(path string, data []byte, perm os.FileMode) error {
	return atomicWrite(path, data, func(p string) error { return os.Chmod(p, perm) })
}

// atomicWrite is AtomicWrite with the temp file's protection supplied by
// protect, which runs before any data is written.
func atomicWrite(path string, data []byte, protect func(string) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".home-write-*")
	if err != nil {
		return fmt.Errorf("home: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := protect(tmpPath); err != nil {
		tmp.Close()
		return fmt.Errorf("home: secure temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("home: write temp: %w", err)
	}
	if err := syncTemp(tmp); err != nil {
		tmp.Close()
		return fmt.Errorf("home: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("home: close temp: %w", err)
	}
	if err := RenameDurable(tmpPath, path); err != nil {
		return fmt.Errorf("home: rename into place: %w", err)
	}
	return nil
}
