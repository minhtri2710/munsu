package home

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicWrite durably writes data to path via a unique temp file in the same
// directory, an fsync, and RenameDurable, whose durability is carried by a
// parent-directory fsync on unix and a write-through move on windows. On
// success readers observe either the old or the new content, never a partial
// write.
func canonicalAtomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("home: create write dir: %w", err)
	}
	if err := secureDir(dir); err != nil {
		return fmt.Errorf("home: secure write dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".home-write-*")
	if err != nil {
		return fmt.Errorf("home: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := secureFile(tmpPath); err != nil {
		tmp.Close()
		return fmt.Errorf("home: secure temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("home: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
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
