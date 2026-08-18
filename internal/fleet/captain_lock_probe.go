package fleet

import "os"

// LockHeld reports whether another process currently holds the lock file at
// lockPath. It takes a non-blocking lock and releases it immediately — it
// never blocks, and a holder is never disturbed because the attempt fails
// fast and the probe never waits on the lock. It never removes the file, and
// an absent file is reported free without being created: the doctor that
// calls this is read-only. The handle is closed on every path, and a lock
// taken for the probe is released on every path, error paths included —
// closing the handle releases it too, so an unlock failure cannot leak a
// held lock.
func LockHeld(lockPath string) (bool, error) {
	if _, err := os.Stat(lockPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return false, err
	}
	defer f.Close()

	acquired, err := tryLockFile(f)
	if err != nil {
		return false, err
	}
	if !acquired {
		// Held by someone else; the lock is not ours to release.
		return true, nil
	}
	if err := unlockFile(f); err != nil {
		return false, err
	}
	return false, nil
}
