package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		runSafetyCheck(cmd, gitDir, "munsu watch arm", "agy")
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
		err := runSafetyCheck(cmd, gitDir, "echo hello", "agy")
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
	stdinPayload := `{"toolCall":{"name":"run_command","args":{"CommandLine":"munsu watch arm","Cwd":"/tmp"}},"conversationId":"test"}`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(stdinPayload))
	w.Close()
	os.Stdin = r

	stdout, _ := captureBoth(func() {
		runSafetyCheck(cmd, gitDir, "", "agy")
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

// TestAgySafetyCheckGateRefused verifies agy safety-check blocks
// when gate is active (gate_refused).
func TestAgySafetyCheckGateRefused(t *testing.T) {
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
		runSafetyCheck(cmd, tmpDir, "echo hello", "agy")
	})

	if exitCode != 0 {
		t.Errorf("expected exit 0 for gate-refused agy, got %d", exitCode)
	}

	stdoutStr := strings.TrimSpace(stdout)
	if stdoutStr == "" {
		t.Fatal("expected non-empty stdout for agy gate-refused")
	}

	var denyPayload map[string]interface{}
	if err := json.Unmarshal([]byte(stdoutStr), &denyPayload); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\nGot: %s", err, stdoutStr)
	}

	if denyPayload["decision"] != "deny" {
		t.Errorf("expected decision 'deny', got %v", denyPayload["decision"])
	}
}

// TestAgyReadStdinToolCallArgsCommandLine verifies readStdinForCommand
// handles agy's .toolCall.args.CommandLine (PascalCase nested) shape.
func TestAgyReadStdinToolCallArgsCommandLine(t *testing.T) {
	// Stdin with agy's .toolCall.args.CommandLine
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(`{"toolCall":{"name":"run_command","args":{"CommandLine":"munsu watch arm"}}}`))
	w.Close()
	os.Stdin = r

	cmd, err := readStdinForCommand()
	os.Stdin = oldStdin

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd != "munsu watch arm" {
		t.Errorf("expected 'munsu watch arm', got %q", cmd)
	}
}

// TestAgyReadStdinClaudeShapeAlsoWorks verifies that Claude's .tool_input.command
// still works after adding .toolCall.args.CommandLine support.
func TestAgyReadStdinClaudeShapeAlsoWorks(t *testing.T) {
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(`{"hookEventName":"PreToolUse","tool_input":{"command":"munsu watch arm"}}`))
	w.Close()
	os.Stdin = r

	cmd, err := readStdinForCommand()
	os.Stdin = oldStdin

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd != "munsu watch arm" {
		t.Errorf("expected 'munsu watch arm', got %q", cmd)
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

	// Make tmpDir a git repo so scope classifies it as Primary
	runGit(t, tmpDir, "init")

	// Create in-flight task meta
	metaDir := filepath.Join(tmpDir, "state")
	os.MkdirAll(metaDir, 0755)
	meta := "kind=ship\nwindow=test\n"
	if err := os.WriteFile(filepath.Join(metaDir, "test-task.meta"), []byte(meta), 0644); err != nil {
		t.Fatal(err)
	}

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

// TestAgyGuardPendingRelayContinues verifies guard --harness agy returns
// {"decision":"continue","reason":"..."} when a pending terminal receipt
// exists with material status.
func TestAgyGuardPendingRelayContinues(t *testing.T) {
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

	var stdout string
	captureBoth(func() {
		oldStdin := os.Stdin
		r, w, _ := os.Pipe()
		w.Write([]byte(`{}`))
		w.Close()
		os.Stdin = r

		sout, _ := captureBoth(func() {
			runGuardAgy(tmpDir)
		})
		stdout = sout

		os.Stdin = oldStdin
	})

	if exitCode != 0 {
		t.Errorf("expected exit 0 for agy continue, got %d", exitCode)
	}

	stdoutStr := strings.TrimSpace(stdout)
	if stdoutStr == "" {
		t.Fatal("expected non-empty stdout for pending relay")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(stdoutStr), &payload); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\nGot: %s", err, stdoutStr)
	}
	if payload["decision"] != "continue" {
		t.Errorf("expected decision 'continue' for pending relay, got %v", payload["decision"])
	}
	if reason, ok := payload["reason"].(string); !ok || reason == "" {
		t.Errorf("expected non-empty reason for pending relay, got %v", payload["reason"])
	}
	if !strings.Contains(stdoutStr, "material relay pending") {
		t.Errorf("reason must contain 'material relay pending', got: %s", stdoutStr)
	}
}

// TestAgyGuardNoPendingRelayAllows verifies guard --harness agy returns
// {"decision":"allow"} when no pending terminal receipts exist.
func TestAgyGuardNoPendingRelayAllows(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_PARENT_STATUS", "")

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	var stdout string
	captureBoth(func() {
		oldStdin := os.Stdin
		r, w, _ := os.Pipe()
		w.Write([]byte(`{}`))
		w.Close()
		os.Stdin = r

		sout, _ := captureBoth(func() {
			runGuardAgy(tmpDir)
		})
		stdout = sout

		os.Stdin = oldStdin
	})

	if exitCode != 0 {
		t.Errorf("expected exit 0 for agy allow, got %d", exitCode)
	}

	stdoutStr := strings.TrimSpace(stdout)
	if stdoutStr == "" {
		t.Fatal("expected non-empty stdout for no pending relay")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(stdoutStr), &payload); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\nGot: %s", err, stdoutStr)
	}
	if payload["decision"] != "allow" {
		t.Errorf("expected decision 'allow' when no pending obligations, got %v", payload["decision"])
	}
}

// TestAgyGuardParentHomeCleanAllows verifies guard --harness agy returns
// {"decision":"allow"} when MUNSU_PARENT_STATUS points at a clean parent home
// with no pending terminal receipts. This pins the allow path's independence
// from any ambient parent state: guard tests must pin MUNSU_PARENT_STATUS
// explicitly (see the "" pins above) instead of inheriting the environment.
func TestAgyGuardParentHomeCleanAllows(t *testing.T) {
	tmpDir := t.TempDir()
	parentHome := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	var stdout string
	captureBoth(func() {
		oldStdin := os.Stdin
		r, w, _ := os.Pipe()
		w.Write([]byte(`{"executionNum":1,"terminationReason":"model_stop","fullyIdle":true}`))
		w.Close()
		os.Stdin = r

		sout, _ := captureBoth(func() {
			runGuardAgy(tmpDir)
		})
		stdout = sout

		os.Stdin = oldStdin
	})

	if exitCode != 0 {
		t.Errorf("expected exit 0 for agy allow, got %d", exitCode)
	}

	stdoutStr := strings.TrimSpace(stdout)
	if stdoutStr == "" {
		t.Fatal("expected non-empty stdout for clean parent home")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(stdoutStr), &payload); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\nGot: %s", err, stdoutStr)
	}
	if payload["decision"] != "allow" {
		t.Errorf("expected decision 'allow' with clean parent home, got %v", payload["decision"])
	}
}

// TestAgyGuardParentHomeReceiptContinues verifies guard --harness agy returns
// {"decision":"continue"} when homeDir has NO receipt but MUNSU_PARENT_STATUS
// has a pending material receipt.
func TestAgyGuardParentHomeReceiptContinues(t *testing.T) {
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

	var stdout string
	captureBoth(func() {
		oldStdin := os.Stdin
		r, w, _ := os.Pipe()
		w.Write([]byte(`{}`))
		w.Close()
		os.Stdin = r

		sout, _ := captureBoth(func() {
			runGuardAgy(tmpDir)
		})
		stdout = sout
		os.Stdin = oldStdin
	})

	if exitCode != 0 {
		t.Errorf("expected exit 0 for agy continue, got %d", exitCode)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\nGot: %s", err, stdout)
	}
	if payload["decision"] != "continue" {
		t.Errorf("expected decision 'continue' from parent-home receipt, got %v", payload["decision"])
	}
	if !strings.Contains(stdout, "material relay pending") {
		t.Errorf("reason must contain 'material relay pending', got: %s", stdout)
	}
	if !strings.Contains(stdout, parentHome) {
		t.Errorf("reason must mention parent-home path, got: %s", stdout)
	}
}

// TestAgyGuardParentHomeAckedAllows verifies guard --harness agy returns
// {"decision":"allow"} after the parent-home receipt is acknowledged.
func TestAgyGuardParentHomeAckedAllows(t *testing.T) {
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

	var stdout string
	captureBoth(func() {
		oldStdin := os.Stdin
		r, w, _ := os.Pipe()
		w.Write([]byte(`{}`))
		w.Close()
		os.Stdin = r

		sout, _ := captureBoth(func() {
			runGuardAgy(tmpDir)
		})
		stdout = sout
		os.Stdin = oldStdin
	})

	if exitCode != 0 {
		t.Errorf("expected exit 0 for agy allow, got %d", exitCode)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\nGot: %s", err, stdout)
	}
	if payload["decision"] != "allow" {
		t.Errorf("expected decision 'allow' after ack, got %v", payload["decision"])
	}
}

// TestAgyGuardParentHomeUnreadableFailsClosed verifies guard --harness agy
// returns {"decision":"continue"} when parent receipt directory is unreadable.
func TestAgyGuardParentHomeUnreadableFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	parentHome := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	parentReceipts := filepath.Join(parentHome, "state", ".terminal-receipts")
	os.MkdirAll(parentReceipts, 0755)
	os.WriteFile(filepath.Join(parentReceipts, "task-1.uplink.receipt"), []byte("state=done\n"), 0644)
	os.Chmod(parentReceipts, 0000)

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() {
		exitWithCode = oldExit
		os.Chmod(parentReceipts, 0755)
	}()

	var stdout string
	captureBoth(func() {
		oldStdin := os.Stdin
		r, w, _ := os.Pipe()
		w.Write([]byte(`{}`))
		w.Close()
		os.Stdin = r

		sout, _ := captureBoth(func() {
			runGuardAgy(tmpDir)
		})
		stdout = sout
		os.Stdin = oldStdin
	})

	if exitCode != 0 {
		t.Errorf("expected exit 0 for agy fail-closed, got %d", exitCode)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\nGot: %s", err, stdout)
	}
	if payload["decision"] != "continue" {
		t.Errorf("expected decision 'continue' for fail-closed on unreadable dir, got %v", payload["decision"])
	}
}
