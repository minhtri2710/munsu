package cli

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// canonicalCommands is the exact set of visible (non-hidden) top-level
// commands on the munsu root command. Add a new canonical command here.
var canonicalCommands = []string{
	"home", "init", "bootstrap", "skill", "config", "capabilities", "backend",
	"project", "worktree", "harness", "session-start", "task", "brief", "spawn",
	"send", "report", "peek", "soldier-state", "promote", "teardown", "delivery",
	"fleet", "herdr", "watch", "event", "wake", "guard", "doctor", "stow", "ensure-agents-md",
	"update", "captain", "decision-hold", "afk", "integrate", "manual", "inbox",
	"turnend", "soldier-flush", "consume-ready", "context", "dashboard",
}

// TestCanonicalCommandsRegistered fails when a canonical command is missing,
// hidden, or when a visible top-level command is not listed above.
func TestCanonicalCommandsRegistered(t *testing.T) {
	var visible []string
	for _, c := range NewRootCommand().Commands() {
		if !c.Hidden {
			visible = append(visible, c.Name())
		}
	}
	slices.Sort(visible)
	want := slices.Sorted(slices.Values(canonicalCommands))
	if !slices.Equal(visible, want) {
		t.Errorf("visible top-level commands:\n got  %v\n want %v", visible, want)
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
