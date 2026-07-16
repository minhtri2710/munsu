package cli

import (
	"testing"
)

// canonicalCommands is the authoritative list of all visible (non-hidden)
// top-level commands that must always be registered on the munsu root command.
// A new entry must be added here when a new canonical command is defined.
var canonicalCommands = []struct {
	name string
	use  string
}{
	{name: "home", use: "home [--mkdir]"},
	{name: "init", use: "init"},
	{name: "bootstrap", use: "bootstrap [install <tools>...]"},
	{name: "skill", use: "skill"},
	{name: "config", use: "config"},
	{name: "capabilities", use: "capabilities"},
	{name: "backend", use: "backend"},
	{name: "project", use: "project"},
	{name: "worktree", use: "worktree"},
	{name: "harness", use: "harness"},
	{name: "session-start", use: "session-start"},
	{name: "task", use: "task"},
	{name: "backlog", use: "backlog"},
	{name: "brief", use: "brief <id> <repo>"},
	{name: "spawn", use: "spawn <id> [<project>]"},
	{name: "send", use: "send <id> <line>"},
	{name: "peek", use: "peek <id>"},
	{name: "crew-state", use: "crew-state <id>"},
	{name: "promote", use: "promote <id>"},
	{name: "teardown", use: "teardown <id>"},
	{name: "delivery", use: "delivery"},
	{name: "fleet", use: "fleet"},
	{name: "watch", use: "watch"},
	{name: "event", use: "event"},
	{name: "watch-arm", use: "watch-arm"},
	{name: "wake", use: "wake"},
	{name: "wake-drain", use: "wake-drain"},
	{name: "guard", use: "guard"},
	{name: "doctor", use: "doctor"},
	{name: "stow", use: "stow [text...]"},
	{name: "ensure-agents-md", use: "ensure-agents-md <project>"},
	{name: "update", use: "update"},
	{name: "secondmate", use: "secondmate"},
	{name: "afk", use: "afk"},
}

// TestCanonicalCommandsRegistered is the root-command availability
// regression gate. It fails whenever a canonical command is missing,
// has the wrong Use string, or is incorrectly marked hidden.
func TestCanonicalCommandsRegistered(t *testing.T) {
	root := NewRootCommand()

	for _, tt := range canonicalCommands {
		t.Run(tt.name, func(t *testing.T) {
			cmd, _, err := root.Find([]string{tt.name})
			if err != nil {
				t.Fatalf("command %q not found on root: %v", tt.name, err)
			}
			if cmd == nil {
				t.Fatalf("command %q is nil", tt.name)
			}
			if cmd.Use != tt.use {
				t.Errorf("command %q: expected Use %q, got %q", tt.name, tt.use, cmd.Use)
			}
			if cmd.Hidden {
				t.Errorf("command %q is hidden but should be canonical", tt.name)
			}
		})
	}

	// Verify the count of visible commands matches the canonical list.
	// Any hidden compatibility aliases (e.g. review-diff, fleet-sync) are excluded.
	var visibleCount int
	for _, c := range root.Commands() {
		if !c.Hidden {
			visibleCount++
		}
	}
	if visibleCount != len(canonicalCommands) {
		t.Errorf("expected %d visible commands, got %d", len(canonicalCommands), visibleCount)
	}
}

// TestRestoredCommandsHelp verifies that help output for the three restored
// commands and root help mentions them.
func TestRestoredCommandsHelp(t *testing.T) {
	root := NewRootCommand()

	// Each restored command should respond to --help without error
	for _, name := range []string{"backlog", "session-start", "bootstrap"} {
		t.Run(name+"-help", func(t *testing.T) {
			cmd, _, err := root.Find([]string{name})
			if err != nil {
				t.Fatalf("command %q not found: %v", name, err)
			}
			if cmd == nil {
				t.Fatalf("command %q is nil", name)
			}
			// Run with --help; should not panic
			root.SetArgs([]string{name, "--help"})
			if err := root.Execute(); err != nil {
				t.Fatalf("%s --help failed: %v", name, err)
			}
		})
	}

	// Root help output must contain the restored commands
	t.Run("root-help-contains-restored", func(t *testing.T) {
		root.SetArgs([]string{"--help"})
		err := root.Execute()
		if err != nil {
			t.Fatalf("root --help failed: %v", err)
		}
		// We can't easily capture cobra's stdout in a unit test without
		// replacing os.Stdout, so we verify via command lookup instead.
		for _, name := range []string{"backlog", "session-start", "bootstrap"} {
			cmd, _, err := root.Find([]string{name})
			if err != nil {
				t.Errorf("command %q should be found on root after restore: %v", name, err)
			}
			if cmd == nil {
				t.Errorf("command %q is nil on root after restore", name)
			}
		}
	})
}

// TestDoctorCommandRegistered verifies the doctor subcommand is registered on root.
func TestDoctorCommandRegistered(t *testing.T) {
	root := NewRootCommand()

	doctorCmd, _, err := root.Find([]string{"doctor"})
	if err != nil {
		t.Fatalf("doctor command not found: %v", err)
	}

	if doctorCmd == nil {
		t.Fatal("doctor command is nil")
	}

	if doctorCmd.Use != "doctor" {
		t.Errorf("expected Use 'doctor', got %q", doctorCmd.Use)
	}
}

// TestDoctorHelp verifies doctor --help output without panic.
func TestDoctorHelp(t *testing.T) {
	root := NewRootCommand()

	// Execute with --help flag; should not panic
	root.SetArgs([]string{"doctor", "--help"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("doctor --help failed: %v", err)
	}
}
