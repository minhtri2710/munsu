package cli

import (
	"os"
	"testing"
)

// TestMain disables the os.Exit seam for every test that does not exercise it.
func TestMain(m *testing.M) {
	if os.Getenv("MUNSU_GUARD_CHILD") == "" {
		// An os.Exit reached from Cobra RunE terminates the test binary and
		// skips every test queued behind it; ambient tool availability can
		// enter that path accidentally on hosts without tmux. Guard children
		// explicitly test the exit contract, so retain the real seam there.
		exitWithCode = func(int) {}
	}
	os.Exit(m.Run())
}
