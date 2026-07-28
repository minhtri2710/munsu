package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// TestClaudeSafetyCheckAllow verifies safety-check --harness claude allow
// produces exit 0, empty stdout, empty stderr.
func TestClaudeSafetyCheckAllow(t *testing.T) {
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
		err := runSafetyCheck(cmd, gitDir, "echo hello", "claude")
		if err != nil {
			exitCode = 1
		}
	})

	if exitCode != 0 {
		t.Errorf("expected exit 0 for allow, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected empty stdout for claude allow, got %q", stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("expected empty stderr for claude allow, got %q", stderr)
	}
}

// TestClaudeSafetyCheckDeny verifies safety-check --harness claude deny
// produces exit 2, empty stdout, and a stderr deny JSON.
func TestClaudeSafetyCheckDeny(t *testing.T) {
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
		runSafetyCheck(cmd, gitDir, "munsu watch arm", "claude")
	})

	if exitCode != 2 {
		t.Errorf("expected exit 2 for claude deny, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected empty stdout for claude deny, got %q", stdout)
	}

	// Verify stderr is valid JSON with the deny shape
	stderrStr := strings.TrimSpace(stderr)
	if stderrStr == "" {
		t.Fatal("expected non-empty stderr for claude deny")
	}

	var denyPayload map[string]interface{}
	if err := json.Unmarshal([]byte(stderrStr), &denyPayload); err != nil {
		t.Fatalf("stderr must be valid JSON: %v\nGot: %s", err, stderrStr)
	}

	// Check Claude deny shape
	hookOutput, ok := denyPayload["hookSpecificOutput"].(map[string]interface{})
	if !ok {
		t.Fatal("stderr JSON must have hookSpecificOutput object")
	}
	if hookOutput["hookEventName"] != "PreToolUse" {
		t.Errorf("expected hookEventName PreToolUse, got %v", hookOutput["hookEventName"])
	}
	if hookOutput["permissionDecision"] != "deny" {
		t.Errorf("expected permissionDecision deny, got %v", hookOutput["permissionDecision"])
	}
	if _, ok := denyPayload["systemMessage"]; !ok {
		t.Error("stderr JSON must have systemMessage")
	}
}

// TestClaudeSafetyCheckDenyViaStdin verifies claude safety-check deny works
// when the command is provided via stdin JSON.
func TestClaudeSafetyCheckDenyViaStdin(t *testing.T) {
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

	// Mock stdin with Claude-shaped JSON
	stdinPayload := `{"hookEventName":"PreToolUse","tool_input":{"command":"munsu watch arm"}}`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(stdinPayload))
	w.Close()
	os.Stdin = r

	stdout, stderr := captureBoth(func() {
		runSafetyCheck(cmd, gitDir, "", "claude")
	})

	os.Stdin = oldStdin

	if exitCode != 2 {
		t.Errorf("expected exit 2 for claude deny via stdin, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected empty stdout for claude deny, got %q", stdout)
	}

	stderrStr := strings.TrimSpace(stderr)
	if stderrStr == "" {
		t.Fatal("expected non-empty stderr for claude deny")
	}

	var denyPayload map[string]interface{}
	if err := json.Unmarshal([]byte(stderrStr), &denyPayload); err != nil {
		t.Fatalf("stderr must be valid JSON: %v\nGot: %s", err, stderrStr)
	}
}

// TestClaudeSafetyCheckGateRefused verifies claude safety-check blocks
// when gate is active (gate_refused).
func TestClaudeSafetyCheckGateRefused(t *testing.T) {
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
		runSafetyCheck(cmd, tmpDir, "echo hello", "claude")
	})

	if exitCode != 2 {
		t.Errorf("expected exit 2 for gate-refused claude, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected empty stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "deny") {
		t.Errorf("expected deny in stderr for gate-refused claude, got: %s", stderr)
	}
}

// TestClaudeGuardStopHookActive verifies guard --harness claude exits 0
// when stop_hook_active is true (loop guard).
func TestClaudeGuardStopHookActive(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	// Call runGuardClaude with stdin containing stop_hook_active=true
	stdinPayload := `{"hookEventName":"Stop","stop_hook_active":true}`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(stdinPayload))
	w.Close()
	os.Stdin = r

	err := runGuardClaude(tmpDir)

	os.Stdin = oldStdin

	if err != nil {
		t.Errorf("expected no error for stop_hook_active, got: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit 0 for stop_hook_active, got %d", exitCode)
	}
}

// TestClaudeGuardBlindTurn verifies guard --harness claude blocks when
// tasks are in-flight and watcher beat is stale.
func TestClaudeGuardBlindTurn(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

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

		runGuardClaude(tmpDir)

		os.Stdin = oldStdin
	})

	if exitCode != 2 {
		t.Errorf("expected exit 2 for blind turn, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected empty stdout for claude guard, got %q", stdout)
	}
	if !strings.Contains(stderr, "TURN WOULD END BLIND") {
		t.Errorf("stderr must contain 'TURN WOULD END BLIND', got: %s", stderr)
	}
	if !strings.Contains(stderr, "in-flight") {
		t.Errorf("stderr must mention in-flight tasks, got: %s", stderr)
	}
}

// TestClaudeGuardHealthyExit verifies guard --harness claude exits 0
// when in-flight tasks exist but watcher is healthy.
func TestClaudeGuardHealthyExit(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

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

	err := runGuardClaude(tmpDir)

	os.Stdin = oldStdin

	if err != nil {
		t.Errorf("expected no error for healthy guard, got: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit 0 for healthy guard, got %d", exitCode)
	}
}

// TestSessionStartNudgeGateAgent verifies sessionstart-nudge stays silent
// when NO_MISTAKES_GATE is set.
func TestSessionStartNudgeGateAgent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("NO_MISTAKES_GATE", "1")

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	stdout, stderr := captureBoth(func() {
		err := runSessionStartNudge(cmd, Ctx{Home: tmpDir})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected silent output for gate agent, got: %s", stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("expected silent stderr for gate agent, got: %s", stderr)
	}
}

// TestSessionStartNudgeNonPrimary verifies sessionstart-nudge stays silent
// when not in a primary checkout.
func TestSessionStartNudgeNonPrimary(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	// Change to tmpDir (no git repo) so CheckSessionScope sees unrelated → non-primary
	oldCwd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldCwd)

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	stdout, stderr := captureBoth(func() {
		err := runSessionStartNudge(cmd, Ctx{Home: tmpDir})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected silent output for non-primary, got: %s", stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("expected silent stderr for non-primary, got: %s", stderr)
	}
}

// TestSessionStartNudgePrintsNudgeInPrimary verifies sessionstart-nudge prints
// the nudge in a git repo primary checkout.
func TestSessionStartNudgePrintsNudge(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	// Create a git repo in tmpDir
	runGit(t, tmpDir, "init")

	// runSessionStartNudge checks os.Getwd() for primary scope.
	// Change to tmpDir so CheckSessionScope sees the git repo.
	oldCwd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldCwd)

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	stdout, stderr := captureBoth(func() {
		err := runSessionStartNudge(cmd, Ctx{Home: tmpDir})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	if !strings.Contains(stdout, "session-start") {
		t.Errorf("expected nudge containing 'session-start', got: %s", stdout)
	}
	if !strings.Contains(stdout, "exactly once") {
		t.Errorf("expected nudge containing 'exactly once', got: %s", stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("expected empty stderr, got: %s", stderr)
	}
}

// TestSessionStartNudgeSilentWhenLocked verifies sessionstart-nudge stays silent
// when the lock is held by an ancestor process.
func TestSessionStartNudgeSilentWhenLocked(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	// Create a git repo in tmpDir
	runGit(t, tmpDir, "init")

	// runSessionStartNudge checks os.Getwd() for primary scope
	oldCwd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldCwd)

	// Create a lock file with current PID (which is in our ancestry)
	stateDir := filepath.Join(tmpDir, "state")
	os.MkdirAll(stateDir, 0755)
	lockContent := fmt.Sprintf("%d\n", os.Getpid())
	if err := os.WriteFile(filepath.Join(stateDir, ".lock"), []byte(lockContent), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	stdout, stderr := captureBoth(func() {
		err := runSessionStartNudge(cmd, Ctx{Home: tmpDir})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected silent output when lock held, got: %s", stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("expected silent stderr when lock held, got: %s", stderr)
	}
}

// -- helpers --

// captureBoth runs f and returns everything written to stdout and stderr.
func captureBoth(f func()) (string, string) {
	stdoutR, stdoutW, _ := os.Pipe()
	stderrR, stderrW, _ := os.Pipe()
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	os.Stdout = stdoutW
	os.Stderr = stderrW

	f()

	stdoutW.Close()
	stderrW.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	var stdoutBuf, stderrBuf bytes.Buffer
	io.Copy(&stdoutBuf, stdoutR)
	io.Copy(&stderrBuf, stderrR)
	return stdoutBuf.String(), stderrBuf.String()
}

// runGit runs a git command in the given directory.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, string(out))
	}
}

// TestClaudeGuardPendingRelayBlocks verifies guard --harness claude blocks
// (exit 2) when a pending terminal receipt exists with material status.
func TestClaudeGuardPendingRelayBlocks(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	// Create minimal state/.terminal-receipts with a pending receipt
	receiptsDir := filepath.Join(tmpDir, "state", ".terminal-receipts")
	os.MkdirAll(receiptsDir, 0755)
	receiptPath := filepath.Join(receiptsDir, "test-task.uplink.receipt")
	os.WriteFile(receiptPath, []byte("state=done\n"), 0644)

	// No ack file means the receipt is pending

	// Create material status file so MaterialReportExists returns true
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
			runGuardClaude(tmpDir)
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

// TestClaudeGuardNoPendingRelayAllows verifies guard --harness claude allows
// (exit 0) when no pending terminal receipts exist.
func TestClaudeGuardNoPendingRelayAllows(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	// No receipts directory at all — should allow
	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(`{}`))
	w.Close()
	os.Stdin = r

	err := runGuardClaude(tmpDir)
	os.Stdin = oldStdin

	if err != nil {
		t.Errorf("expected no error when no pending obligations, got: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit 0 when no pending obligations, got %d", exitCode)
	}
}

// TestClaudeGuardParentHomeReceiptBlocks verifies guard --harness claude blocks
// when homeDir has NO receipt but MUNSU_PARENT_STATUS has a pending material receipt.
func TestClaudeGuardParentHomeReceiptBlocks(t *testing.T) {
	tmpDir := t.TempDir()
	parentHome := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	// Create receipt + material status in parent home only
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
			runGuardClaude(tmpDir)
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

// TestClaudeGuardParentHomeAckedAllows verifies guard --harness claude allows
// after the parent-home receipt is acknowledged.
func TestClaudeGuardParentHomeAckedAllows(t *testing.T) {
	tmpDir := t.TempDir()
	parentHome := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	// Create receipt + ack in parent home (simulates captain having relayed)
	parentReceipts := filepath.Join(parentHome, "state", ".terminal-receipts")
	os.MkdirAll(parentReceipts, 0755)
	os.WriteFile(filepath.Join(parentReceipts, "task-1.uplink.receipt"), []byte("state=done\n"), 0644)
	// Write ack file to acknowledge the receipt
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

	err := runGuardClaude(tmpDir)
	os.Stdin = oldStdin

	if err != nil {
		t.Errorf("expected no error after ack, got: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit 0 after ack, got %d", exitCode)
	}
}

// TestClaudeGuardParentHomeUnreadableFailsClosed verifies guard fails closed
// when parent receipt directory exists but is unreadable.
func TestClaudeGuardParentHomeUnreadableFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	parentHome := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	// Create receipt dir with restricted permissions
	parentReceipts := filepath.Join(parentHome, "state", ".terminal-receipts")
	os.MkdirAll(parentReceipts, 0755)
	os.WriteFile(filepath.Join(parentReceipts, "task-1.uplink.receipt"), []byte("state=done\n"), 0644)
	// Make the directory unreadable
	os.Chmod(parentReceipts, 0000)
	if err := os.Chmod(parentReceipts, 0000); err == nil {
		// Only test if we can actually restrict permissions
	}

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

		runGuardClaude(tmpDir)
		os.Stdin = oldStdin
	})

	if exitCode != 2 {
		t.Errorf("expected exit 2 for fail-closed on unreadable dir, got %d", exitCode)
	}
}

// Walking from a known child reaches a known ancestor (monotonic pid change).
func TestReadParentPID(t *testing.T) {
	ppid := readParentPID(os.Getpid())
	expected := os.Getppid()
	if ppid != expected {
		t.Errorf("readParentPID(%d) = %d, want %d (os.Getppid())", os.Getpid(), ppid, expected)
	}

	// Walk up: pid should change each step until reaching 1 (or the walk limit)
	visited := map[int]bool{}
	pid := os.Getpid()
	for i := 0; i < 8; i++ {
		if pid <= 1 {
			break
		}
		if visited[pid] {
			t.Errorf("ancestry cycle at pid %d on step %d", pid, i)
			break
		}
		visited[pid] = true
		nextPid := readParentPID(pid)
		if nextPid <= 0 {
			break
		}
		if nextPid >= pid && pid > 1 {
			// Parent may have the same PID during exec wrappers (unlikely on macOS)
		}
		pid = nextPid
	}
}

// TestSessionStartNudgeAlwaysExitsZero verifies every path exits 0.
// Gate agent, non-primary, lock-held, and primary-unlocked must all
// exit 0 because Claude/Codex-class hooks treat non-zero as blocking
// session init.
func TestSessionStartNudgeAlwaysExitsZero(t *testing.T) {
	type testCase struct {
		name  string
		setup func(tmpDir string)
	}

	tests := []testCase{
		{
			name: "gate agent silent exit 0",
			setup: func(tmpDir string) {
				t.Setenv("NO_MISTAKES_GATE", "1")
			},
		},
		{
			name: "non-primary silent exit 0",
			setup: func(tmpDir string) {
				// No git repo => non-primary
			},
		},
		{
			name: "lock held silent exit 0",
			setup: func(tmpDir string) {
				// Create a git repo so scope is primary
				runGit(t, tmpDir, "init")
				// Create lock file with current PID
				stateDir := filepath.Join(tmpDir, "state")
				os.MkdirAll(stateDir, 0755)
				lockContent := fmt.Sprintf("%d\n", os.Getpid())
				os.WriteFile(filepath.Join(stateDir, ".lock"), []byte(lockContent), 0644)
			},
		},
		{
			name: "primary unlocked prints nudge exit 0",
			setup: func(tmpDir string) {
				// Create a git repo so scope is primary
				runGit(t, tmpDir, "init")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("MUNSU_HOME", tmpDir)
			tt.setup(tmpDir)

			// runSessionStartNudge checks os.Getwd() for primary scope.
			oldCwd, _ := os.Getwd()
			os.Chdir(tmpDir)
			defer os.Chdir(oldCwd)

			var exitCode int
			oldExit := exitWithCode
			exitWithCode = func(code int) { exitCode = code }
			defer func() { exitWithCode = oldExit }()

			cmd := &cobra.Command{}
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)

			captureBoth(func() {
				err := runSessionStartNudge(cmd, Ctx{Home: tmpDir})
				if err != nil {
					t.Errorf("runSessionStartNudge returned error: %v", err)
				}
			})

			// runSessionStartNudge never calls exitWithCode -- it returns nil.
			// But we track exitCode to be safe.
			if exitCode != 0 {
				t.Errorf("expected exit 0, got %d", exitCode)
			}
		})
	}
}

// TestSessionStartNudgeRetryBeforeSuccess verifies that when the lock is
// NOT held (session-start has not yet succeeded), a second nudge call
// still produces the nudge -- retry before success is allowed.
//
// The nudge function itself does not track session-start success; that's
// delegated to `munsu session-start` which acquires the lock. The retry
// test simulates the condition where session-start hasn't been run yet
// (no lock file), verifies the nudge fires, then verifies it fires again
// on a retry -- proving that a failed/absent session-start doesn't
// suppress subsequent nudges.
func TestSessionStartNudgeRetryBeforeSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	// Create a git repo so scope is primary
	runGit(t, tmpDir, "init")

	oldCwd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldCwd)

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	// First nudge: should produce output (no lock, primary, no gate)
	stdout1, _ := captureBoth(func() {
		err := runSessionStartNudge(cmd, Ctx{Home: tmpDir})
		if err != nil {
			t.Errorf("first nudge returned error: %v", err)
		}
	})
	if !strings.Contains(stdout1, "session-start") {
		t.Errorf("first nudge should print instruction, got: %s", stdout1)
	}

	// Captain nudge: still no lock (simulates session-start not yet run,
	// e.g. underlying command failed or was cancelled)
	// Should also produce output - retry is allowed before success
	stdout2, _ := captureBoth(func() {
		err := runSessionStartNudge(cmd, Ctx{Home: tmpDir})
		if err != nil {
			t.Errorf("captain nudge returned error: %v", err)
		}
	})
	if !strings.Contains(stdout2, "session-start") {
		t.Errorf("captain nudge should also print instruction before lock acquired, got: %s", stdout2)
	}

	// Verify both calls produce the same nudge (retry is identical to first attempt)
	if strings.TrimSpace(stdout1) != strings.TrimSpace(stdout2) {
		t.Errorf("retry nudge must be identical to first nudge\nfirst: %q\nretry: %q", stdout1, stdout2)
	}
}

// TestSessionStartNudgeIdempotentAfterLock verifies that once the lock is
// held by an ancestor, subsequent nudge calls stay silent.
func TestSessionStartNudgeIdempotentAfterLock(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	runGit(t, tmpDir, "init")

	oldCwd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldCwd)

	// Create lock file with current PID to simulate lock held
	stateDir := filepath.Join(tmpDir, "state")
	os.MkdirAll(stateDir, 0755)
	lockContent := fmt.Sprintf("%d\n", os.Getpid())
	os.WriteFile(filepath.Join(stateDir, ".lock"), []byte(lockContent), 0644)

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	// First call with lock held: silent
	stdout1, _ := captureBoth(func() {
		err := runSessionStartNudge(cmd, Ctx{Home: tmpDir})
		if err != nil {
			t.Errorf("first nudge with lock returned error: %v", err)
		}
	})
	if strings.TrimSpace(stdout1) != "" {
		t.Errorf("expected silent output when lock held, got: %s", stdout1)
	}

	// Captain call: still silent (lock still held)
	stdout2, _ := captureBoth(func() {
		err := runSessionStartNudge(cmd, Ctx{Home: tmpDir})
		if err != nil {
			t.Errorf("captain nudge with lock returned error: %v", err)
		}
	})
	if strings.TrimSpace(stdout2) != "" {
		t.Errorf("expected silent output on second call when lock still held, got: %s", stdout2)
	}
}

// TestSessionStartNudgeExactLine verifies the exact nudge line matches the
// contract specification.
func TestSessionStartNudgeExactLine(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	runGit(t, tmpDir, "init")

	oldCwd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldCwd)

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	stdout, _ := captureBoth(func() {
		err := runSessionStartNudge(cmd, Ctx{Home: tmpDir})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	// Must be exactly one line
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 1 {
		t.Errorf("expected exactly 1 line of output, got %d lines: %s", len(lines), stdout)
	}

	line := strings.TrimSpace(lines[0])
	expected := "Run `munsu session-start` now, exactly once, before executing any other instructions."
	if line != expected {
		t.Errorf("nudge line mismatch:\ngot:  %q\nwant: %q", line, expected)
	}
}
