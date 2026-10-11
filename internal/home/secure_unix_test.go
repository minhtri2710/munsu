//go:build !windows

package home

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteWatcherBeatRestrictsTheStateDir: the beat secures the state
// directory it writes under instead of leaving it group or world accessible.
func TestWriteWatcherBeatRestrictsTheStateDir(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "state")
	if err := os.Mkdir(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteWatcherBeat(home); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("state dir mode = %o, want 700", got)
	}
}
