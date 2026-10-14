package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

// TestAgySafetyCheckDeny verifies safety-check --harness agy deny
// produces exit 0, stdout decision=deny JSON, empty stderr.
// CRITICAL REGRESSION GUARD: agy gates on the stdout decision field,
// NOT exit code. Getting this wrong is a security gap.
func TestAgySafetyCheckDeny(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	// Create a git repo to pass scope check
	gitDir := filepath.Join(tmpDir, "repo")
	os.MkdirAll(gitDir, 0755)
	runGit(t, gitDir, "init")

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	stdout, stderr := captureBoth(func() {
		runSafetyCheck(cmd, gitDir, "munsu watch", "", "agy")
	})

	if exitCode != 0 {
		t.Errorf("expected exit 0 for agy deny, got %d", exitCode)
	}

	// Verify stdout is the exact agy deny shape: {"decision":"deny","reason":"..."}
	stdoutStr := strings.TrimSpace(stdout)
	if stdoutStr == "" {
		t.Fatal("expected non-empty stdout for agy deny")
	}

	var denyPayload map[string]interface{}
	if err := json.Unmarshal([]byte(stdoutStr), &denyPayload); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\nGot: %s", err, stdoutStr)
	}

	// Check agy deny shape: {"decision":"deny","reason":"..."}
	if denyPayload["decision"] != "deny" {
		t.Errorf("expected decision 'deny', got %v", denyPayload["decision"])
	}
	reason, ok := denyPayload["reason"].(string)
	if !ok || reason == "" {
		t.Errorf("expected non-empty reason string, got %v", denyPayload["reason"])
	}
	if !strings.Contains(reason, "safety-block") {
		t.Errorf("expected reason to contain 'safety-block', got %q", reason)
	}

	// Stderr should be empty for agy
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("expected empty stderr for agy deny, got %q", stderr)
	}
}

// TestAgySafetyCheckAllow verifies safety-check --harness agy allow
// produces exit 0, stdout {"decision":"allow"}, empty stderr.
func TestAgySafetyCheckAllow(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	// Create a git repo to pass scope check
	gitDir := filepath.Join(tmpDir, "repo")
	os.MkdirAll(gitDir, 0755)
	runGit(t, gitDir, "init")

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	stdout, stderr := captureBoth(func() {
		err := runSafetyCheck(cmd, gitDir, "echo hello", "", "agy")
		if err != nil {
			exitCode = 1
		}
	})

	if exitCode != 0 {
		t.Errorf("expected exit 0 for agy allow, got %d", exitCode)
	}

	// Verify stdout is the agy allow shape: {"decision":"allow"}
	stdoutStr := strings.TrimSpace(stdout)
	if stdoutStr == "" {
		t.Fatal("expected non-empty stdout for agy allow")
	}

	var allowPayload map[string]interface{}
	if err := json.Unmarshal([]byte(stdoutStr), &allowPayload); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\nGot: %s", err, stdoutStr)
	}

	if allowPayload["decision"] != "allow" {
		t.Errorf("expected decision 'allow', got %v", allowPayload["decision"])
	}

	// Stderr should be empty for agy
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("expected empty stderr for agy allow, got %q", stderr)
	}
}

// TestAgySafetyCheckDenyViaStdin verifies agy safety-check deny works
// when the command is provided via stdin JSON with agy's toolCall.args.CommandLine shape.
func TestAgySafetyCheckDenyViaStdin(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	// Create a git repo to pass scope check
	gitDir := filepath.Join(tmpDir, "repo")
	os.MkdirAll(gitDir, 0755)
	runGit(t, gitDir, "init")

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	// Mock stdin with agy-shaped JSON (.toolCall.args.CommandLine PascalCase)
	stdinPayload := `{"toolCall":{"name":"run_command","args":{"CommandLine":"munsu watch","Cwd":"/tmp"}},"conversationId":"test"}`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(stdinPayload))
	w.Close()
	os.Stdin = r

	stdout, _ := captureBoth(func() {
		runSafetyCheck(cmd, gitDir, "", "", "agy")
	})

	os.Stdin = oldStdin

	if exitCode != 0 {
		t.Errorf("expected exit 0 for agy deny via stdin, got %d", exitCode)
	}

	stdoutStr := strings.TrimSpace(stdout)
	if stdoutStr == "" {
		t.Fatal("expected non-empty stdout for agy deny")
	}

	var denyPayload map[string]interface{}
	if err := json.Unmarshal([]byte(stdoutStr), &denyPayload); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\nGot: %s", err, stdoutStr)
	}

	if denyPayload["decision"] != "deny" {
		t.Errorf("expected decision 'deny', got %v", denyPayload["decision"])
	}
}

// TestAgyReadStdinToolCallArgsCommandLine verifies readStdinForToolPayload
// handles agy's .toolCall.args.CommandLine (PascalCase nested) shape.
func TestAgyReadStdinToolCallArgsCommandLine(t *testing.T) {
	// Stdin with agy's .toolCall.args.CommandLine
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(`{"toolCall":{"name":"run_command","args":{"CommandLine":"munsu watch"}}}`))
	w.Close()
	os.Stdin = r

	payload, err := readStdinForToolPayload()
	os.Stdin = oldStdin

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload.command != "munsu watch" {
		t.Errorf("expected 'munsu watch', got %q", payload.command)
	}
}

// TestAgyReadStdinClaudeShapeAlsoWorks verifies that Claude's .tool_input.command
// still works after adding .toolCall.args.CommandLine support.
func TestAgyReadStdinClaudeShapeAlsoWorks(t *testing.T) {
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(`{"hookEventName":"PreToolUse","tool_input":{"command":"munsu watch"}}`))
	w.Close()
	os.Stdin = r

	payload, err := readStdinForToolPayload()
	os.Stdin = oldStdin

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload.command != "munsu watch" {
		t.Errorf("expected 'munsu watch', got %q", payload.command)
	}
}

// TestAgyGuardStopHookFullyIdle verifies guard --harness agy exits 0
// with decision "allow" when fullyIdle is true.
func TestAgyGuardStopHookFullyIdle(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	// Guard reads ambient MUNSU_PARENT_STATUS; pin it so tests stay hermetic
	// (ambient captain homes fail the obligation gate closed).
	t.Setenv("MUNSU_PARENT_STATUS", "")

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	// Call runGuardAgy with stdin containing fullyIdle=true
	stdinPayload := `{"executionNum":1,"terminationReason":"model_stop","fullyIdle":true}`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(stdinPayload))
	w.Close()
	os.Stdin = r

	stdout, _ := captureBoth(func() {
		err := runGuardAgy(tmpDir)
		if err != nil {
			exitCode = 1
		}
	})

	os.Stdin = oldStdin

	if exitCode != 0 {
		t.Errorf("expected exit 0 for fullyIdle, got %d", exitCode)
	}

	// Verify stdout has decision "allow"
	stdoutStr := strings.TrimSpace(stdout)
	if stdoutStr == "" {
		t.Fatal("expected non-empty stdout for fullyIdle")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(stdoutStr), &payload); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\nGot: %s", err, stdoutStr)
	}
	if payload["decision"] != "allow" {
		t.Errorf("expected decision 'allow' for fullyIdle, got %v", payload["decision"])
	}
}

// TestAgyGuardBlindTurn verifies guard --harness agy returns continue
// when tasks are in-flight and watcher beat is stale.
func TestAgyGuardBlindTurn(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_PARENT_STATUS", "")

	// Initialize a canonical home so fleet state reads (fleet.Snapshot) work,
	// then make tmpDir a git repo so scope classifies it as Primary.
	if _, err := home.Init(tmpDir); err != nil {
		t.Fatal(err)
	}
	runGit(t, tmpDir, "init")

	// Create an in-flight canonical task (clean break: guard reads only
	// authoritative Task Authority records).
	metaDir := filepath.Join(tmpDir, "state")
	os.MkdirAll(metaDir, 0755)
	writeTaskMeta(t, tmpDir, "test-task", "ship")

	// Create stale beat (10 minutes ago)
	beat := "0 0"
	if err := os.WriteFile(filepath.Join(metaDir, ".last-watcher-beat"), []byte(beat), 0644); err != nil {
		t.Fatal(err)
	}

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	var stdout string
	captureBoth(func() {
		// Pipe stdin with fullyIdle false
		oldStdin := os.Stdin
		r, w, _ := os.Pipe()
		w.Write([]byte(`{"executionNum":1,"terminationReason":"model_stop","fullyIdle":false}`))
		w.Close()
		os.Stdin = r

		sout, _ := captureBoth(func() {
			runGuardAgy(tmpDir)
		})
		stdout = sout

		os.Stdin = oldStdin
	})

	if exitCode != 0 {
		t.Errorf("expected exit 0 for blind turn, got %d", exitCode)
	}

	// Verify stdout has decision "continue"
	stdoutStr := strings.TrimSpace(stdout)
	if stdoutStr == "" {
		t.Fatal("expected non-empty stdout for blind turn")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(stdoutStr), &payload); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\nGot: %s", err, stdoutStr)
	}
	if payload["decision"] != "continue" {
		t.Errorf("expected decision 'continue' for blind turn, got %v", payload["decision"])
	}
	if reason, ok := payload["reason"].(string); !ok || reason == "" {
		t.Errorf("expected non-empty reason for blind turn, got %v", payload["reason"])
	}
}
