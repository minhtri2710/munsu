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
	"github.com/spf13/cobra"
)

// TestCodexSafetyCheckAllow verifies safety-check --harness codex allow
// produces exit 0, no output.
func TestCodexSafetyCheckAllow(t *testing.T) {
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
		err := runSafetyCheck(cmd, gitDir, "echo hello", "codex")
		if err != nil {
			exitCode = 1
		}
	})

	if exitCode != 0 {
		t.Errorf("expected exit 0 for allow, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected empty stdout for codex allow, got %q", stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("expected empty stderr for codex allow, got %q", stderr)
	}
}

// TestCodexSafetyCheckDeny verifies safety-check --harness codex deny
// produces exit 2 and stderr text (NOT JSON).
func TestCodexSafetyCheckDeny(t *testing.T) {
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
		runSafetyCheck(cmd, gitDir, "munsu watch arm", "codex")
	})

	if exitCode != 2 {
		t.Errorf("expected exit 2 for codex deny, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected empty stdout for codex deny, got %q", stdout)
	}

	// Verify stderr is PLAIN TEXT (not JSON)
	stderrStr := strings.TrimSpace(stderr)
	if stderrStr == "" {
		t.Fatal("expected non-empty stderr for codex deny")
	}
	if !strings.Contains(stderrStr, "[safety-block]") {
		t.Errorf("expected stderr to contain '[safety-block]', got: %s", stderrStr)
	}

	// Verify stderr is NOT valid JSON (Codex deny is plain text, not JSON)
	var parsed map[string]interface{}
	if json.Unmarshal([]byte(stderrStr), &parsed) == nil {
		t.Error("codex deny stderr must NOT be valid JSON; Codex expects plain text")
	}
}

// TestCodexSafetyCheckDenyViaStdin verifies codex safety-check deny works
// when the command is provided via stdin JSON.
func TestCodexSafetyCheckDenyViaStdin(t *testing.T) {
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

	// Mock stdin with Codex-shaped JSON
	stdinPayload := `{"hookEventName":"PreToolUse","tool_input":{"command":"munsu watch arm"}}`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(stdinPayload))
	w.Close()
	os.Stdin = r

	stdout, stderr := captureBoth(func() {
		runSafetyCheck(cmd, gitDir, "", "codex")
	})

	os.Stdin = oldStdin

	if exitCode != 2 {
		t.Errorf("expected exit 2 for codex deny via stdin, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected empty stdout for codex deny, got %q", stdout)
	}

	stderrStr := strings.TrimSpace(stderr)
	if stderrStr == "" {
		t.Fatal("expected non-empty stderr for codex deny")
	}
	if !strings.Contains(stderrStr, "[safety-block]") {
		t.Errorf("expected stderr to contain '[safety-block]', got: %s", stderrStr)
	}
}

// TestCodexSafetyCheckGateRefused verifies codex safety-check blocks
// when gate is active (gate_refused).
func TestCodexSafetyCheckGateRefused(t *testing.T) {
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

	stdout, stderr := captureBoth(func() {
		runSafetyCheck(cmd, tmpDir, "echo hello", "codex")
	})

	if exitCode != 2 {
		t.Errorf("expected exit 2 for gate-refused codex, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected empty stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "safety-block") {
		t.Errorf("expected safety-block in stderr for gate-refused codex, got: %s", stderr)
	}
}

// TestCodexGuardStopHookActive verifies guard --harness codex exits 0
// when stop_hook_active is true (loop guard).
func TestCodexGuardStopHookActive(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_PARENT_STATUS", "")

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	// Call runGuardCodexLike with stdin containing stop_hook_active=true
	stdinPayload := `{"hookEventName":"Stop","stop_hook_active":true}`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(stdinPayload))
	w.Close()
	os.Stdin = r

	err := runGuardCodexLike(tmpDir)

	os.Stdin = oldStdin

	if err != nil {
		t.Errorf("expected no error for stop_hook_active, got: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit 0 for stop_hook_active, got %d", exitCode)
	}
}

// TestCodexGuardBlindTurn verifies guard --harness codex blocks when
// tasks are in-flight and watcher beat is stale.
func TestCodexGuardBlindTurn(t *testing.T) {
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

	stdout, stderr := captureBoth(func() {
		// Pipe stdin with stop_hook_active false
		oldStdin := os.Stdin
		r, w, _ := os.Pipe()
		w.Write([]byte(`{"hookEventName":"Stop","stop_hook_active":false}`))
		w.Close()
		os.Stdin = r

		runGuardCodexLike(tmpDir)

		os.Stdin = oldStdin
	})

	if exitCode != 2 {
		t.Errorf("expected exit 2 for blind turn, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected empty stdout for codex guard, got %q", stdout)
	}
	if !strings.Contains(stderr, "TURN WOULD END BLIND") {
		t.Errorf("stderr must contain 'TURN WOULD END BLIND', got: %s", stderr)
	}
	if !strings.Contains(stderr, "in-flight") {
		t.Errorf("stderr must mention in-flight tasks, got: %s", stderr)
	}
}

// TestCodexGuardHealthyExit verifies guard --harness codex exits 0
// when in-flight tasks exist but watcher is healthy.
func TestCodexGuardHealthyExit(t *testing.T) {
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

	err := runGuardCodexLike(tmpDir)

	os.Stdin = oldStdin

	if err != nil {
		t.Errorf("expected no error for healthy guard, got: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit 0 for healthy guard, got %d", exitCode)
	}
}

// TestCodexGuardPendingRelayBlocks verifies guard --harness codex blocks
// (exit 2) when a pending terminal receipt exists with material status.
func TestCodexGuardPendingRelayBlocks(t *testing.T) {
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
			runGuardCodexLike(tmpDir)
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

// TestCodexGuardNoPendingRelayAllows verifies guard --harness codex allows
// (exit 0) when no pending terminal receipts exist.
func TestCodexGuardNoPendingRelayAllows(t *testing.T) {
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

	err := runGuardCodexLike(tmpDir)
	os.Stdin = oldStdin

	if err != nil {
		t.Errorf("expected no error when no pending obligations, got: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit 0 when no pending obligations, got %d", exitCode)
	}
}

// TestCodexGuardParentHomeReceiptBlocks verifies guard --harness codex blocks
// when homeDir has NO receipt but MUNSU_PARENT_STATUS has a pending material receipt.
func TestCodexGuardParentHomeReceiptBlocks(t *testing.T) {
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
			runGuardCodexLike(tmpDir)
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

// TestCodexGuardParentHomeAckedAllows verifies guard --harness codex allows
// after the parent-home receipt is acknowledged.
func TestCodexGuardParentHomeAckedAllows(t *testing.T) {
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

	err := runGuardCodexLike(tmpDir)
	os.Stdin = oldStdin

	if err != nil {
		t.Errorf("expected no error after ack, got: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit 0 after ack, got %d", exitCode)
	}
}

// TestCodexGuardParentHomeUnreadableFailsClosed verifies guard fails closed
// when parent receipt directory exists but is unreadable.
func TestCodexGuardParentHomeUnreadableFailsClosed(t *testing.T) {
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

	captureBoth(func() {
		oldStdin := os.Stdin
		r, w, _ := os.Pipe()
		w.Write([]byte(`{}`))
		w.Close()
		os.Stdin = r

		runGuardCodexLike(tmpDir)
		os.Stdin = oldStdin
	})

	if exitCode != 2 {
		t.Errorf("expected exit 2 for fail-closed on unreadable dir, got %d", exitCode)
	}
}
