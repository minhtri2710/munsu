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

	"github.com/minhtri2710/munsu/internal/home"
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
		err := runSafetyCheck(cmd, gitDir, "echo hello", "", "claude")
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
		runSafetyCheck(cmd, gitDir, "munsu watch", "", "claude")
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
	stdinPayload := `{"hookEventName":"PreToolUse","tool_input":{"command":"munsu watch"}}`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(stdinPayload))
	w.Close()
	os.Stdin = r

	stdout, stderr := captureBoth(func() {
		runSafetyCheck(cmd, gitDir, "", "", "claude")
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
		runSafetyCheck(cmd, tmpDir, "echo hello", "", "claude")
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
	t.Setenv("MUNSU_PARENT_STATUS", "")

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	// Call runGuardBlocking with stdin containing stop_hook_active=true
	stdinPayload := `{"hookEventName":"Stop","stop_hook_active":true}`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(stdinPayload))
	w.Close()
	os.Stdin = r

	err := runGuardBlocking(tmpDir)

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
	t.Setenv("MUNSU_PARENT_STATUS", "")

	// Initialize a canonical home so fleet state reads (fleet.Snapshot) work,
	// then make tmpDir a git repo so scope classifies it as Primary.
	if _, err := home.Init(tmpDir); err != nil {
		t.Fatal(err)
	}
	runGit(t, tmpDir, "init")

	// Create an in-flight canonical task (clean break: guard reads only
	// authoritative Task Authority records). home.Init must precede any state
	// directory creation so the home is recognized as a canonical munsu home.
	if _, err := home.Init(tmpDir); err != nil {
		t.Fatal(err)
	}
	metaDir := filepath.Join(tmpDir, "state")
	os.MkdirAll(metaDir, 0755)
	writeTaskMeta(t, tmpDir, "test-task", "ship")

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

		runGuardBlocking(tmpDir)

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
	t.Setenv("MUNSU_PARENT_STATUS", "")

	// Create an in-flight canonical task (clean break: guard reads only
	// authoritative Task Authority records). home.Init must precede any state
	// directory creation so the home is recognized as a canonical munsu home.
	if _, err := home.Init(tmpDir); err != nil {
		t.Fatal(err)
	}
	metaDir := filepath.Join(tmpDir, "state")
	os.MkdirAll(metaDir, 0755)
	writeTaskMeta(t, tmpDir, "test-task", "ship")

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

	err := runGuardBlocking(tmpDir)

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

	// Hold the session lock from this process (which is in our ancestry)
	if acquired, err := home.AcquireSessionLock(tmpDir); err != nil || !acquired {
		t.Fatalf("AcquireSessionLock = %v, %v; want held", acquired, err)
	}
	t.Cleanup(func() { _ = home.ReleaseSessionLock(tmpDir) })

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
		name   string
		setup  func(t *testing.T, tmpDir string)
		silent bool
	}

	tests := []testCase{
		{
			name: "gate agent silent exit 0",
			setup: func(t *testing.T, tmpDir string) {
				t.Setenv("NO_MISTAKES_GATE", "1")
			},
		},
		{
			name: "non-primary silent exit 0",
			setup: func(t *testing.T, tmpDir string) {
				// No git repo => non-primary
			},
		},
		{
			name: "lock held silent exit 0",
			setup: func(t *testing.T, tmpDir string) {
				// Create a git repo so scope is primary
				runGit(t, tmpDir, "init")
				// Hold the session lock from this process (in our ancestry)
				if acquired, err := home.AcquireSessionLock(tmpDir); err != nil || !acquired {
					t.Fatalf("AcquireSessionLock = %v, %v; want held", acquired, err)
				}
				t.Cleanup(func() { _ = home.ReleaseSessionLock(tmpDir) })
			},
			silent: true,
		},
		{
			name: "primary unlocked prints nudge exit 0",
			setup: func(t *testing.T, tmpDir string) {
				// Create a git repo so scope is primary
				runGit(t, tmpDir, "init")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("MUNSU_HOME", tmpDir)
			tt.setup(t, tmpDir)

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

			stdout, _ := captureBoth(func() {
				err := runSessionStartNudge(cmd, Ctx{Home: tmpDir})
				if err != nil {
					t.Errorf("runSessionStartNudge returned error: %v", err)
				}
			})
			if tt.silent && strings.TrimSpace(stdout) != "" {
				t.Errorf("expected silent output, got: %s", stdout)
			}

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

	// Second nudge: still no lock (simulates session-start not yet run,
	// e.g. underlying command failed or was cancelled)
	// Should also produce output - retry is allowed before success
	stdout2, _ := captureBoth(func() {
		err := runSessionStartNudge(cmd, Ctx{Home: tmpDir})
		if err != nil {
			t.Errorf("second nudge returned error: %v", err)
		}
	})
	if !strings.Contains(stdout2, "session-start") {
		t.Errorf("second nudge should also print instruction before lock acquired, got: %s", stdout2)
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

	// Hold the session lock from this process (which is in our ancestry)
	if acquired, err := home.AcquireSessionLock(tmpDir); err != nil || !acquired {
		t.Fatalf("AcquireSessionLock = %v, %v; want held", acquired, err)
	}
	t.Cleanup(func() { _ = home.ReleaseSessionLock(tmpDir) })

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

	// Second call: still silent (lock still held)
	stdout2, _ := captureBoth(func() {
		err := runSessionStartNudge(cmd, Ctx{Home: tmpDir})
		if err != nil {
			t.Errorf("second nudge with lock returned error: %v", err)
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

// A soldier seat runs the Human's Stop hook too, but its turn end is not the
// dispatcher's: the guard allows it to stop over a dispatcher whose turn would
// end blind, and still refuses the dispatcher in that state.
func TestGuardAllowsASoldierSeatToStop(t *testing.T) {
	for _, tc := range []struct {
		harness, role string
		wantExit      int
		wantOut       string
	}{
		{"claude", "soldier", 0, ""},
		{"claude", "general", 2, "TURN WOULD END BLIND"},
		{"claude", "captain", 2, "TURN WOULD END BLIND"},
		{"codex", "soldier", 0, ""},
		{"opencode", "soldier", 0, ""},
		{"grok", "soldier", 0, ""},
		{"agy", "soldier", 0, `{"decision":"allow"}`},
		{"agy", "general", 0, `"decision":"continue"`},
	} {
		t.Run(tc.harness+"/"+tc.role, func(t *testing.T) {
			tmpDir := t.TempDir()
			if _, err := home.Init(tmpDir); err != nil {
				t.Fatal(err)
			}
			runGit(t, tmpDir, "init")
			writeTaskMeta(t, tmpDir, "test-task", "ship")
			beat := fmt.Sprintf("%d %d", time.Now().Add(-10*time.Minute).Unix(), os.Getpid())
			if err := os.WriteFile(filepath.Join(tmpDir, "state", ".last-watcher-beat"), []byte(beat), 0644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("MUNSU_HOME", tmpDir)
			t.Setenv("MUNSU_ROLE", tc.role)

			exitCode := 0
			oldExit := exitWithCode
			exitWithCode = func(code int) { exitCode = code }
			defer func() { exitWithCode = oldExit }()
			stdout, stderr := captureBoth(func() {
				oldStdin := os.Stdin
				r, w, _ := os.Pipe()
				w.Write([]byte(`{"hookEventName":"Stop","stop_hook_active":false}`))
				w.Close()
				os.Stdin = r
				runRoot(t, "guard", "--harness", tc.harness, "--home", tmpDir)
				os.Stdin = oldStdin
			})
			if exitCode != tc.wantExit {
				t.Fatalf("exit = %d, want %d\nstdout=%s\nstderr=%s", exitCode, tc.wantExit, stdout, stderr)
			}
			if !strings.Contains(stdout+stderr, tc.wantOut) {
				t.Fatalf("output = %q %q, want %q", stdout, stderr, tc.wantOut)
			}
		})
	}
}
