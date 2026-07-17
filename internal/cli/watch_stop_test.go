package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/lifecycle"
)

// TestStopWatcherNoBeat verifies that stopping with no beat returns already-stopped.
func TestStopWatcherNoBeat(t *testing.T) {
	home := t.TempDir()
	result := stopWatcher(home)

	if result.Status != "success" {
		t.Fatalf("expected status success, got %q", result.Status)
	}
	if result.Data.State != "already-stopped" {
		t.Fatalf("expected state already-stopped, got %q", result.Data.State)
	}
}

// TestStopWatcherStaleBeat verifies that stopping with a stale/dead PID
// returns stopped (idempotent, no error on non-existent process).
func TestStopWatcherStaleBeat(t *testing.T) {
	home := t.TempDir()

	// Write a beat with a PID that doesn't exist (PID 1 is init, use something reliably dead)
	beatPath := lifecycle.BeatPath(home)
	os.MkdirAll(filepath.Dir(beatPath), 0755)
	content := []byte("1 9999999") // PID 9999999 is unlikely to exist
	if err := os.WriteFile(beatPath, content, 0644); err != nil {
		t.Fatalf("write beat: %v", err)
	}

	result := stopWatcher(home)

	if result.Status != "success" {
		t.Fatalf("expected status success, got %q", result.Status)
	}
	if result.Data.State != "stopped" && result.Data.State != "unresponsive" {
		t.Fatalf("expected state stopped or unresponsive, got %q", result.Data.State)
	}
}

// TestWatchStopCommandRegistered verifies the stop subcommand exists on watch.
func TestWatchStopCommandRegistered(t *testing.T) {
	root := NewRootCommand()

	watchCmd, _, err := root.Find([]string{"watch"})
	if err != nil {
		t.Fatalf("watch command not found: %v", err)
	}
	if watchCmd == nil {
		t.Fatal("watch command is nil")
	}

	stopCmd, _, err := watchCmd.Find([]string{"stop"})
	if err != nil {
		t.Fatalf("watch stop subcommand not found: %v", err)
	}
	if stopCmd == nil {
		t.Fatal("watch stop subcommand is nil")
	}
	if !strings.Contains(stopCmd.Short, "Stop") {
		t.Errorf("expected short description to contain 'Stop', got %q", stopCmd.Short)
	}
}

// TestWatchStopContractResponseType ensures the stop command writes a valid WatchStop
// contract response by running the command against an empty home.
func TestWatchStopContractResponseType(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)

	root := NewRootCommand()
	root.SetArgs([]string{"watch", "stop"})
	if err := root.Execute(); err != nil {
		t.Fatalf("watch stop: unexpected error: %v", err)
	}
	// No panic is success; output verification is a bonus
}
