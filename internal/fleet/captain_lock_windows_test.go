//go:build windows

package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAcquireExclusiveLock_WindowsReleaseLeavesFileReusable pins the Windows
// contract: the lock file survives release and a subsequent acquire reuses the
// same fixed pathname.
func TestAcquireExclusiveLock_WindowsReleaseLeavesFileReusable(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	release, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("acquireExclusiveLock error: %v", err)
	}
	release()

	// The flock pathname remains in place across release.
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
