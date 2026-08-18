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
	d, err := os.Open(filepath.Dir(to))
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return err
	}
	return nil
}
