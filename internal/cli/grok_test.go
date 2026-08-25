package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/testutil/fsaccess"
	"github.com/spf13/cobra"
)

// TestGrokSafetyCheckAllow verifies safety-check --harness grok allow
// produces exit 0, empty stdout, empty stderr.
func TestGrokSafetyCheckAllow(t *testing.T) {
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
		err := runSafetyCheck(cmd, gitDir, "echo hello", "", "grok")
		if err != nil {
			exitCode = 1
		}
	})

	if exitCode != 0 {
		t.Errorf("expected exit 0 for allow, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected empty stdout for grok allow, got %q", stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("expected empty stderr for grok allow, got %q", stderr)
	}
}

// TestGrokSafetyCheckDeny verifies safety-check --harness grok deny
// produces exit 2, stdout decision=deny JSON, empty stderr.
func TestGrokSafetyCheckDeny(t *testing.T) {
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
		runSafetyCheck(cmd, gitDir, "munsu watch arm", "", "grok")
	})

	if exitCode != 2 {
		t.Errorf("expected exit 2 for grok deny, got %d", exitCode)
	}

	// Verify stdout is the exact Grok deny shape: {"decision":"deny","reason":"..."}
	stdoutStr := strings.TrimSpace(stdout)
	if stdoutStr == "" {
		t.Fatal("expected non-empty stdout for grok deny")
	}

	var denyPayload map[string]interface{}
	if err := json.Unmarshal([]byte(stdoutStr), &denyPayload); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\nGot: %s", err, stdoutStr)
	}

	// Check Grok deny shape: {"decision":"deny","reason":"..."}
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

	// Stderr should be empty for grok
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("expected empty stderr for grok deny, got %q", stderr)
	}
}

// TestGrokSafetyCheckDenyViaStdin verifies grok safety-check deny works
// when the command is provided via stdin JSON with Grok's .toolInput.command shape.
func TestGrokSafetyCheckDenyViaStdin(t *testing.T) {
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

	// Mock stdin with Grok-shaped JSON (.toolInput.command camelCase)
	stdinPayload := `{"hookEventName":"PreToolUse","toolInput":{"command":"munsu watch arm"}}`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(stdinPayload))
	w.Close()
	os.Stdin = r

	stdout, _ := captureBoth(func() {
		runSafetyCheck(cmd, gitDir, "", "", "grok")
	})

	os.Stdin = oldStdin

	if exitCode != 2 {
		t.Errorf("expected exit 2 for grok deny via stdin, got %d", exitCode)
	}

	stdoutStr := strings.TrimSpace(stdout)
	if stdoutStr == "" {
		t.Fatal("expected non-empty stdout for grok deny")
	}

	var denyPayload map[string]interface{}
	if err := json.Unmarshal([]byte(stdoutStr), &denyPayload); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\nGot: %s", err, stdoutStr)
	}

	if denyPayload["decision"] != "deny" {
		t.Errorf("expected decision 'deny', got %v", denyPayload["decision"])
	}
}

// TestGrokSafetyCheckGateRefused verifies grok safety-check blocks
// when gate is active (gate_refused).
func TestGrokSafetyCheckGateRefused(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("NO_MISTAKES_GATE", "1")

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	stdout, _ := captureBoth(func() {
		runSafetyCheck(cmd, tmpDir, "echo hello", "", "grok")
	})

	if exitCode != 2 {
		t.Errorf("expected exit 2 for gate-refused grok, got %d", exitCode)
	}

	stdoutStr := strings.TrimSpace(stdout)
	if stdoutStr == "" {
		t.Fatal("expected non-empty stdout for grok gate-refused")
	}

	var denyPayload map[string]interface{}
	if err := json.Unmarshal([]byte(stdoutStr), &denyPayload); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\nGot: %s", err, stdoutStr)
	}

	if denyPayload["decision"] != "deny" {
		t.Errorf("expected decision 'deny', got %v", denyPayload["decision"])
	}
}

// TestGrokGuardStopHookActive verifies guard --harness grok exits 0
// when stop_hook_active is true (loop guard).
func TestGrokGuardStopHookActive(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_PARENT_STATUS", "")

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	// Call runGuardGrok with stdin containing stop_hook_active=true
	stdinPayload := `{"hookEventName":"Stop","stop_hook_active":true}`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(stdinPayload))
	w.Close()
	os.Stdin = r

	err := runGuardGrok(tmpDir)

	os.Stdin = oldStdin

	if err != nil {
		t.Errorf("expected no error for stop_hook_active, got: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit 0 for stop_hook_active, got %d", exitCode)
	}
}

// TestGrokGuardBlindTurn verifies guard --harness grok blocks when
// tasks are in-flight and watcher beat is stale.
func TestGrokGuardBlindTurn(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_PARENT_STATUS", "")

	// Initialize a canonical home so fleet state reads (fleet.Snapshot) work,
	// then make tmpDir a git repo so scope classifies it as Primary.
	if _, err := home.Init(tmpDir); err != nil {
		t.Fatal(err)
	}
	runGit(t, tmpDir, "init")

	// Create in-flight task meta
	metaDir := filepath.Join(tmpDir, "state")
	os.MkdirAll(metaDir, 0755)
	meta := "kind=ship\nwindow=test\n"
	if err := os.WriteFile(filepath.Join(metaDir, "test-task.meta"), []byte(meta), 0644); err != nil {
		t.Fatal(err)
	}

	// Create stale beat (10 minutes ago)
	beat := fmt.Sprintf("%d %d", time.Now().Add(-10*time.Minute).Unix(), os.Getpid())
	if err := os.WriteFile(filepath.Join(metaDir, ".last-watcher-beat"), []byte(beat), 0644); err != nil {
		t.Fatal(err)
	}

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	captureBoth(func() {
		// Pipe stdin with stop_hook_active false
		oldStdin := os.Stdin
		r, w, _ := os.Pipe()
		w.Write([]byte(`{"hookEventName":"Stop","stop_hook_active":false}`))
		w.Close()
		os.Stdin = r

		runGuardGrok(tmpDir)

		os.Stdin = oldStdin
	})

	if exitCode != 2 {
		t.Errorf("expected exit 2 for blind turn, got %d", exitCode)
	}
}

// TestGrokGuardHealthyExit verifies guard --harness grok exits 0
// when in-flight tasks exist but watcher is healthy.
func TestGrokGuardHealthyExit(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_PARENT_STATUS", "")

	// Create in-flight task meta
	metaDir := filepath.Join(tmpDir, "state")
	os.MkdirAll(metaDir, 0755)
	meta := "kind=ship\nwindow=test\n"
	if err := os.WriteFile(filepath.Join(metaDir, "test-task.meta"), []byte(meta), 0644); err != nil {
		t.Fatal(err)
	}

	// Create fresh beat
	beat := fmt.Sprintf("%d %d", time.Now().Unix(), os.Getpid())
	if err := os.WriteFile(filepath.Join(metaDir, ".last-watcher-beat"), []byte(beat), 0644); err != nil {
		t.Fatal(err)
	}

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(`{}`))
	w.Close()
	os.Stdin = r

	err := runGuardGrok(tmpDir)

	os.Stdin = oldStdin

	if err != nil {
		t.Errorf("expected no error for healthy guard, got: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit 0 for healthy guard, got %d", exitCode)
	}
}

// TestGrokReadStdinToolInputCommand verifies readStdinForToolPayload
// handles Grok's .toolInput.command (camelCase) shape.
func TestGrokReadStdinToolInputCommand(t *testing.T) {
	// Stdin with Grok's .toolInput.command
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(`{"hookEventName":"PreToolUse","toolInput":{"command":"munsu watch arm"}}`))
	w.Close()
	os.Stdin = r

	payload, err := readStdinForToolPayload()
	os.Stdin = oldStdin

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload.command != "munsu watch arm" {
		t.Errorf("expected 'munsu watch arm', got %q", payload.command)
	}
}

// TestGrokReadStdinClaudeShapeAlsoWorks verifies that Claude's .tool_input.command
// still works after adding .toolInput.command support.
func TestGrokReadStdinClaudeShapeAlsoWorks(t *testing.T) {
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(`{"hookEventName":"PreToolUse","tool_input":{"command":"munsu watch arm"}}`))
	w.Close()
	os.Stdin = r

	payload, err := readStdinForToolPayload()
	os.Stdin = oldStdin

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload.command != "munsu watch arm" {
		t.Errorf("expected 'munsu watch arm', got %q", payload.command)
	}
}

// TestGrokGuardPendingRelayBlocks verifies guard --harness grok blocks
// (exit 2) when a pending terminal receipt exists with material status.
func TestGrokGuardPendingRelayBlocks(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_PARENT_STATUS", "")

	receiptsDir := filepath.Join(tmpDir, "state", ".terminal-receipts")
	os.MkdirAll(receiptsDir, 0755)
	receiptPath := filepath.Join(receiptsDir, "test-task.uplink.receipt")
	os.WriteFile(receiptPath, []byte("state=done\n"), 0644)

	stateDir := filepath.Join(tmpDir, "state")
	os.WriteFile(filepath.Join(stateDir, "test-task.status"), []byte("done: task complete\n"), 0644)

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	stderr := ""
	captureBoth(func() {
		oldStdin := os.Stdin
		r, w, _ := os.Pipe()
		w.Write([]byte(`{}`))
		w.Close()
		os.Stdin = r

		sout, serr := captureBoth(func() {
			runGuardGrok(tmpDir)
		})
		stderr = serr
		_ = sout

		os.Stdin = oldStdin
	})

	if exitCode != 2 {
		t.Errorf("expected exit 2 for pending relay obligation, got %d", exitCode)
	}
	if !strings.Contains(stderr, "material relay pending") {
		t.Errorf("stderr must contain 'material relay pending', got: %s", stderr)
	}
}

// TestGrokGuardNoPendingRelayAllows verifies guard --harness grok allows
// (exit 0) when no pending terminal receipts exist.
func TestGrokGuardNoPendingRelayAllows(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_PARENT_STATUS", "")

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(`{}`))
	w.Close()
	os.Stdin = r

	err := runGuardGrok(tmpDir)
	os.Stdin = oldStdin

	if err != nil {
		t.Errorf("expected no error when no pending obligations, got: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit 0 when no pending obligations, got %d", exitCode)
	}
}

// TestGrokGuardParentHomeReceiptBlocks verifies guard --harness grok blocks
// when homeDir has NO receipt but MUNSU_PARENT_STATUS has a pending material receipt.
func TestGrokGuardParentHomeReceiptBlocks(t *testing.T) {
	tmpDir := t.TempDir()
	parentHome := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	parentReceipts := filepath.Join(parentHome, "state", ".terminal-receipts")
	os.MkdirAll(parentReceipts, 0755)
	os.WriteFile(filepath.Join(parentReceipts, "task-1.uplink.receipt"), []byte("state=done\n"), 0644)
	parentState := filepath.Join(parentHome, "state")
	os.WriteFile(filepath.Join(parentState, "task-1.status"), []byte("done: task complete\n"), 0644)

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	stderr := ""
	captureBoth(func() {
		oldStdin := os.Stdin
		r, w, _ := os.Pipe()
		w.Write([]byte(`{}`))
		w.Close()
		os.Stdin = r

		_, serr := captureBoth(func() {
			runGuardGrok(tmpDir)
		})
		stderr = serr
		os.Stdin = oldStdin
	})

	if exitCode != 2 {
		t.Errorf("expected exit 2 from parent-home receipt, got %d", exitCode)
	}
	if !strings.Contains(stderr, "material relay pending") {
		t.Errorf("stderr must mention 'material relay pending', got: %s", stderr)
	}
	if !strings.Contains(stderr, parentHome) {
		t.Errorf("stderr must mention parent-home path, got: %s", stderr)
	}
}

// TestGrokGuardParentHomeAckedAllows verifies guard --harness grok allows
// after the parent-home receipt is acknowledged.
func TestGrokGuardParentHomeAckedAllows(t *testing.T) {
	tmpDir := t.TempDir()
	parentHome := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	parentReceipts := filepath.Join(parentHome, "state", ".terminal-receipts")
	os.MkdirAll(parentReceipts, 0755)
	os.WriteFile(filepath.Join(parentReceipts, "task-1.uplink.receipt"), []byte("state=done\n"), 0644)
	os.WriteFile(filepath.Join(parentReceipts, "task-1.uplink.ack"), []byte("acked_at=1\n"), 0644)

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(`{}`))
	w.Close()
	os.Stdin = r

	err := runGuardGrok(tmpDir)
	os.Stdin = oldStdin

	if err != nil {
		t.Errorf("expected no error after ack, got: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit 0 after ack, got %d", exitCode)
	}
}

// TestGrokGuardParentHomeUnreadableFailsClosed verifies guard fails closed
// when parent receipt directory exists but is unreadable.
func TestGrokGuardParentHomeUnreadableFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	parentHome := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	parentReceipts := filepath.Join(parentHome, "state", ".terminal-receipts")
	if err := os.MkdirAll(parentReceipts, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentReceipts, "task-1.uplink.receipt"), []byte("state=done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fsaccess.MakeUnreadable(t, parentReceipts)

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() {
		exitWithCode = oldExit
	}()

	captureBoth(func() {
		oldStdin := os.Stdin
		r, w, _ := os.Pipe()
		w.Write([]byte(`{}`))
		w.Close()
		os.Stdin = r

		runGuardGrok(tmpDir)
		os.Stdin = oldStdin
	})

	if exitCode != 2 {
		t.Errorf("expected exit 2 for fail-closed on unreadable dir, got %d", exitCode)
	}
}
