//go:build windows

package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAcquireExclusiveLock_WindowsReleaseLeavesFileReusable pins the contract
// that IS true on Windows. The release closure can never reach os.Remove
// (see acquireExclusiveLock), so the lock file survives release; and a
// subsequent acquire on the same path still succeeds, which is what makes the
// litter bounded: one fixed-name file per home, reused on every converge,
// never accumulated.
func TestAcquireExclusiveLock_WindowsReleaseLeavesFileReusable(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	release, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("acquireExclusiveLock error: %v", err)
	}
	release()

	// Permanent by design: the remove is unreachable on Windows.
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file was removed after release on Windows: %v", err)
	}

	// The fixed-name file is reused, not accumulated: acquiring the same
	// path again succeeds even though the file is still there.
	release2, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("acquire after release on Windows: %v", err)
	}
	release2()
}
