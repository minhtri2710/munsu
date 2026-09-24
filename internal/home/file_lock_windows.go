//go:build windows

package home

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockRangeOffsetHigh places the locked byte far past any content a lock file
// carries. A LockFileEx range denies reads to every other handle, so locking
// from offset 0 would make a holder's pid (the session and AFK locks record
// one) unreadable to the processes that need it. Locking past EOF is allowed.
const lockRangeOffsetHigh = 0x7fffffff

// lockFile takes an exclusive LockFileEx lock on f, the counterpart of the unix
// flock(LOCK_EX). Without LOCKFILE_EXCLUSIVE_LOCK the lock is shared and
// excludes nothing. With nonblock, LOCKFILE_FAIL_IMMEDIATELY is the counterpart
// of LOCK_NB, and ERROR_LOCK_VIOLATION, the counterpart of EWOULDBLOCK, is the
// only status reported as errLockBusy; every other status is returned as
// itself.
func lockFile(f *os.File, nonblock bool) error {
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if nonblock {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &windows.Overlapped{OffsetHigh: lockRangeOffsetHigh})
	if err == windows.ERROR_LOCK_VIOLATION {
		return errLockBusy
	}
	return err
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{OffsetHigh: lockRangeOffsetHigh})
}
