//go:build windows

package home

import (
	"golang.org/x/sys/windows"
)

// RenameDurable renames from to to with the durability guarantee the unix
// half provides via a parent-directory fsync: MOVEFILE_WRITE_THROUGH does not
// return until the move has reached the disk. A directory handle cannot be
// fsynced on Windows, so the move itself carries the durability.
func RenameDurable(from, to string) error {
	fromPtr, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPtr, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(fromPtr, toPtr, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
