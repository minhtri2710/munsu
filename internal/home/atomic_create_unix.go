//go:build !windows

package home

import (
	"os"
	"path/filepath"
)

// atomicCreateNoReplace publishes a same-directory temp file by hard link,
// whose creation is atomic and fails if target already exists (including a
// symlink). Removing the temp name leaves the published inode at target.
func atomicCreateNoReplace(tmpPath, target string) error {
	if err := os.Link(tmpPath, target); err != nil {
		return err
	}
	if err := os.Remove(tmpPath); err != nil {
		return err
	}
	return nil
}

func syncAtomicCreateDirectory(path string) error {
	dir, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
