//go:build windows

package home

import "os"

// The windows half of the wake lock pair delegates to the package-local
// LockFileEx helpers (taskmeta_lock_windows.go) instead of hand-rolling a
// fourth copy: one windows locking implementation in this package, and no new
// guard sites for a lane that can never run this code.
//
// What this does NOT yet give ClaimWakes, stated plainly rather than left to be
// discovered: lockExclusive currently passes no LOCKFILE_EXCLUSIVE_LOCK bit, so
// it requests a SHARED lock, and a shared lock excludes nothing. Until #532
// lands, the wake-claim critical section in wake_lease.go is still not mutually
// excluded on windows -- it is wired correctly, not yet locked correctly. The
// unix sibling (wake_lock_unix.go) does take a real Flock(LOCK_EX).
//
// Delegating is what makes #532 a one-place fix: correcting lockExclusive
// corrects this caller too. A fourth hand-rolled copy would have needed its own.
func lockWakeFile(file *os.File) error   { return lockExclusive(file) }
func unlockWakeFile(file *os.File) error { return unlockFile(file) }
