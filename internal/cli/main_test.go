package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/testutil"
)

// TestMain disables the os.Exit seam for every test that does not exercise it,
// and isolates tmux and the user home.
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
	cleanupHome, homeErr := testutil.IsolateUserHome()
	if homeErr != nil {
		fmt.Fprintf(os.Stderr, "cli TestMain: isolate user home: %v\n", homeErr)
		os.Exit(1)
	}
	code := m.Run()
	cleanupHome()
	if err == nil {
		os.RemoveAll(tmuxDir)
	}
	os.Exit(code)
}

func TestPackageRunsInAnIsolatedUserHome(t *testing.T) {
	pkgHome := os.Getenv(testutil.IsolatedHomeEnv)
	if pkgHome == "" {
		t.Fatal("TestMain did not isolate the user home")
	}
	pi, err := harness.HumanPiAgentDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(pkgHome, ".pi", "agent"); pi != want {
		t.Errorf("HumanPiAgentDir = %q, want %q", pi, want)
	}
	claude, err := harness.HumanClaudeConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(pkgHome, ".claude"); claude.Dir != want {
		t.Errorf("HumanClaudeConfig().Dir = %q, want %q", claude.Dir, want)
	}
}
