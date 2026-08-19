//go:build windows

package home

import "os"

// The windows half of the wake lock pair delegates to the package-local
// LockFileEx helpers (taskmeta_lock_windows.go) instead of hand-rolling a
// fourth copy: one windows locking implementation in this package, and no new
// guard sites for a lane that can never run this code.
//
// What this now gives ClaimWakes: lockExclusive requests a blocking exclusive
// LockFileEx (LOCKFILE_EXCLUSIVE_LOCK, no LOCKFILE_FAIL_IMMEDIATELY), the
// windows counterpart of the unix sibling's blocking Flock(LOCK_EX), so the
// wake-claim critical section in wake_lease.go is mutually excluded on windows
// per the Win32 contract -- a shared lock, which is what this requested before
// #532, would exclude nothing and deny even this process's own writes. Runtime
// behaviour on a real windows box remains the observation lane's job to confirm.
//
// Delegating is what made #532 a one-place fix: correcting lockExclusive
// corrected this caller too. A fourth hand-rolled copy would have needed its own.
func lockWakeFile(file *os.File) error   { return lockExclusive(file) }
func unlockWakeFile(file *os.File) error { return unlockFile(file) }
