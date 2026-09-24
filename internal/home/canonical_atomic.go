package home

import (
	"fmt"
	"os"
	"path/filepath"
)

// canonicalAtomicWrite is AtomicWrite into an owner-private directory with an
// owner-private file.
func canonicalAtomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("home: create write dir: %w", err)
	}
	if err := secureDir(dir); err != nil {
		return fmt.Errorf("home: secure write dir: %w", err)
	}
	return atomicWrite(path, data, secureFile)
}
