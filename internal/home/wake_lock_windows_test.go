//go:build windows

package home

import (
	"os"
	"testing"
)

// TestWakeLockMatchesClaimContract pins the windows half of the wake lock split
// at the only level this repo measures it: the goos-vet lane compiles this
// file, so the binding proves lockWakeFile and unlockWakeFile exist on windows
// with the shape ClaimWakes (wake_lease.go) calls. No lane runs it — whether
// the delegated LockFileEx actually excludes a second claimer on a real Windows
// box stays unproven here.
func TestWakeLockMatchesClaimContract(t *testing.T) {
	var _ func(*os.File) error = lockWakeFile
	var _ func(*os.File) error = unlockWakeFile
}
