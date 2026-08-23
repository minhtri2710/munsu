//go:build !windows

package home

import (
	"os"
	"path/filepath"
)

// RenameDurable renames from to to and fsyncs the parent directory so the
// rename survives a crash. It is the unix half of the durability pair;
// rename_durable_windows.go provides the MOVEFILE_WRITE_THROUGH equivalent.
func RenameDurable(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	toDir := filepath.Dir(to)
	if err := syncDir(toDir); err != nil {
		return err
	}
	fromDir := filepath.Dir(from)
	if fromDir != toDir {
		if err := syncDir(fromDir); err != nil {
			return err
		}
	}
	return nil
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
