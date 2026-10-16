package cli

import (
	"os"
	"path/filepath"
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
	// Snapshots probe the canonical endpoint bindings fixtures record; point
	// tmux at an empty socket directory so no probe reaches the host's server.
	// A child test binary may run with a TMPDIR that does not exist; a missing
	// directory isolates tmux just as well.
	tmuxDir, err := os.MkdirTemp("", "munsu-cli-tmux-")
	if err != nil {
		tmuxDir = filepath.Join(os.TempDir(), "munsu-cli-tmux-absent")
	}
	os.Unsetenv("TMUX")
	os.Setenv("TMUX_TMPDIR", tmuxDir)
	code := m.Run()
	if err == nil {
		os.RemoveAll(tmuxDir)
	}
	os.Exit(code)
}
