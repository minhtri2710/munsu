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
