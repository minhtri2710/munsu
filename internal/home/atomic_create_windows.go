//go:build windows

package home

import (
	"golang.org/x/sys/windows"
)

// atomicCreateNoReplace uses MOVEFILE_WRITE_THROUGH without
// MOVEFILE_REPLACE_EXISTING. Windows refuses an existing target, including a
// reparse-point entry, and the move does not return before reaching disk.
func atomicCreateNoReplace(tmpPath, target string) error {
	tmp, err := windows.UTF16PtrFromString(tmpPath)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(tmp, dst, windows.MOVEFILE_WRITE_THROUGH)
}

func syncAtomicCreateDirectory(string) error { return nil }
