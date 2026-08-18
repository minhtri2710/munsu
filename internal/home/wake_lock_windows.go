//go:build windows

package home

import "os"

// The windows half of the wake lock pair delegates to the package-local
// LockFileEx helpers (taskmeta_lock_windows.go) instead of hand-rolling a
// fourth copy: one exclusive-locking implementation in this package, and no new
// guard sites for a lane that can never run this code. lockExclusive blocks
// until the lock is granted, which is exactly the Flock(LOCK_EX) semantics the
// unix sibling (wake_lock_unix.go) provides and what ClaimWakes relies on.
func lockWakeFile(file *os.File) error   { return lockExclusive(file) }
func unlockWakeFile(file *os.File) error { return unlockFile(file) }
