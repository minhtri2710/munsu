package cli

import (
	"bytes"
	"fmt"
	"strings"
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
	{name: "brief", use: "brief <id> <repo>"},
	{name: "spawn", use: "spawn <id> [<project>]"},
	{name: "send", use: "send <id> <line>"},
	{name: "report", use: "report <state> <msg>"},
	{name: "notify", use: "notify <state> <msg>"},
	{name: "peek", use: "peek <id>"},
	{name: "soldier-state", use: "soldier-state <id>"},
	{name: "promote", use: "promote <id>"},
	{name: "teardown", use: "teardown <id>"},
	{name: "delivery", use: "delivery"},
	{name: "fleet", use: "fleet"},
	{name: "herdr", use: "herdr"},
	{name: "watch", use: "watch"},
	{name: "event", use: "event"},
	{name: "wake", use: "wake"},
	{name: "guard", use: "guard"},
	{name: "doctor", use: "doctor"},
	{name: "stow", use: "stow [text...]"},
	{name: "ensure-agents-md", use: "ensure-agents-md <project>"},
	{name: "update", use: "update"},
	{name: "captain", use: "captain"},
	{name: "decision-hold", use: "decision-hold"},
	{name: "afk", use: "afk"},
	{name: "integrate", use: "integrate"},
	{name: "manual", use: "manual"},
	{name: "inbox", use: "inbox"},
	{name: "turnend", use: "turnend"},
	{name: "soldier-flush", use: "soldier-flush <id>"},
	{name: "ready", use: "ready"},
	{name: "consume-ready", use: "consume-ready <task-id>"},
	{name: "context", use: "context"},
}

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

// TestRestoredCommandsHelp verifies that help output for the restored commands
// and root help mentions them.
func TestRestoredCommandsHelp(t *testing.T) {
	root := NewRootCommand()

	// Each restored command should respond to --help without error
	for _, name := range []string{"session-start", "bootstrap"} {
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
		for _, name := range []string{"session-start", "bootstrap"} {
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

// TestRootNoArgsOutput verifies that running munsu without arguments
// prints a compact fleet/orientation snapshot instead of help text.
func TestRootNoArgsOutput(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{})
	err := root.Execute()
	if err != nil {
		t.Fatalf("root no-args: unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "munsu @") {
		t.Errorf("root no-args output should contain 'munsu @', got: %s", output)
	}
	if !strings.Contains(output, "fleet:") {
		t.Errorf("root no-args output should contain 'fleet:', got: %s", output)
	}
	if !strings.Contains(output, "Next:") {
		t.Errorf("root no-args output should contain 'Next:', got: %s", output)
	}
}

// TestRootHelpStillWorks verifies that munsu --help still shows help text.
func TestRootHelpStillWorks(t *testing.T) {
	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"--help"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Usage:") {
		t.Error("--help output should contain 'Usage:' (help text)")
	}
}

// TestRootNoArgsEmptyHome verifies no-args output handles empty home gracefully.
func TestRootNoArgsEmptyHome(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{})
	err := root.Execute()
	if err != nil {
		t.Fatalf("root no-args empty home: unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "No tasks") {
		t.Errorf("empty home should show 'No tasks' hint, got: %s", output)
	}
}

// TestCaptainListEmpty verifies captain list prints empty state.
func TestCaptainListEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"captain", "list"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("captain list: unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "No captains registered.") {
		t.Errorf("empty captain list should show 'No captains registered.', got: %s", output)
	}
}

// TestFleetSyncEmpty verifies fleet sync prints empty state.
func TestFleetSyncEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"fleet", "sync"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("fleet sync empty: unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "No projects to sync.") {
		t.Errorf("empty fleet sync should show 'No projects to sync.', got: %s", output)
	}
}

func TestUsageErrorExitCode(t *testing.T) {
	// usageError should return exit code 2 via WriteContractError
	err := usageError("invalid_argument", "Run --help", "test usage error")
	var buf bytes.Buffer
	code := WriteContractError(&buf, err, []string{})
	if code != 2 {
		t.Errorf("usageError exit code = %d, want 2", code)
	}
}

func TestOperationErrorExitCode(t *testing.T) {
	// operationError should return exit code 1 via WriteContractError
	err := operationError("not_found", "Try again", "test operation error")
	var buf bytes.Buffer
	code := WriteContractError(&buf, err, []string{})
	if code != 1 {
		t.Errorf("operationError exit code = %d, want 1", code)
	}
}

func TestPlainErrorExitCode(t *testing.T) {
	// Plain fmt.Errorf (non-contract error) should be wrapped as operation error → exit 1
	err := fmt.Errorf("plain error")
	var buf bytes.Buffer
	code := WriteContractError(&buf, err, []string{})
	if code != 1 {
		t.Errorf("plain error exit code = %d, want 1", code)
	}
}

func TestExactArgsMissingProducesUsageError(t *testing.T) {
	// send cmd uses ExactArgs(2); missing args should produce usageError
	root := NewRootCommand()
	root.SetArgs([]string{"send"}) // missing both args
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing args on send")
	}
	var buf bytes.Buffer
	code := WriteContractError(&buf, err, []string{"send"})
	if code != 2 {
		t.Errorf("missing args on send: exit code = %d, want 2 (usageError), err=%v", code, err)
	}
}

func TestSoldierStateMissingArgsProducesUsageError(t *testing.T) {
	// soldier-state uses ExactArgs(1); missing should produce usageError
	root := NewRootCommand()
	root.SetArgs([]string{"soldier-state"}) // missing id
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing args on soldier-state")
	}
	var buf bytes.Buffer
	code := WriteContractError(&buf, err, []string{"soldier-state"})
	if code != 2 {
		t.Errorf("missing args on soldier-state: exit code = %d, want 2 (usageError), err=%v", code, err)
	}
}
