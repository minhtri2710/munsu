package cli

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/orchestrator"
)

// TestLockPIDInAncestryFindsSessionHolder pins that the session-start nudge is
// silenced by the process holding the session lock. This test process takes
// the lock, so its own pid is the holder and the first ancestry step matches.
func TestLockPIDInAncestryFindsSessionHolder(t *testing.T) {
	homeDir := t.TempDir()
	acquired, err := orchestrator.AcquireSession(homeDir)
	if err != nil || !acquired {
		t.Fatalf("AcquireSession = %v, %v; want held", acquired, err)
	}
	t.Cleanup(func() { _ = orchestrator.ReleaseSession(homeDir) })

	if !lockPIDInAncestry(homeDir) {
		t.Fatal("lockPIDInAncestry = false while this process holds the session lock")
	}
}
