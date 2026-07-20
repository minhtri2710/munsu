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

// TestOpencodeSafetyCheckDeny verifies safety-check --harness opencode deny
// produces exit 2 and stderr plaintext (NOT JSON), matching the codex shape
// the OpenCode plugin reads (it throws on exit code 2, using stderr as reason).
// Regression: an earlier revision advertised opencode support in --help but had
// no deny branch, so blocks fell through to the Pi-shaped output (exit 0) and
// the OpenCode PreToolUse plugin never threw.
func TestOpencodeSafetyCheckDeny(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	// Create a git repo to pass the scope check.
	gitDir := filepath.Join(tmpDir, "repo")
	os.MkdirAll(gitDir, 0755)
	runGit(t, gitDir, "init")

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	stdout, stderr := captureBoth(func() {
		runSafetyCheck(cmd, gitDir, "munsu watch arm", "opencode")
	})

	if exitCode != 2 {
		t.Errorf("expected exit 2 for opencode deny, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected empty stdout for opencode deny, got %q", stdout)
	}

	stderrStr := strings.TrimSpace(stderr)
	if stderrStr == "" {
		t.Fatal("expected non-empty stderr for opencode deny")
	}
	if !strings.Contains(stderrStr, "[safety-block]") {
		t.Errorf("expected stderr to contain '[safety-block]', got: %s", stderrStr)
	}
	// OpenCode deny stderr must be plain text, not JSON.
	var parsed map[string]interface{}
	if json.Unmarshal([]byte(stderrStr), &parsed) == nil {
		t.Error("opencode deny stderr must NOT be valid JSON; the plugin reads plain-text stderr")
	}
}

// TestOpencodeSafetyCheckAllow verifies safety-check --harness opencode allow
// produces exit 0 (the plugin must not throw on a safe command).
func TestOpencodeSafetyCheckAllow(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	gitDir := filepath.Join(tmpDir, "repo")
	os.MkdirAll(gitDir, 0755)
	runGit(t, gitDir, "init")

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	runSafetyCheck(cmd, gitDir, "echo hello", "opencode")
	if exitCode != 0 {
		t.Errorf("expected exit 0 for opencode allow, got %d", exitCode)
	}
}

// TestOpencodeSafetyCheckGateRefused verifies opencode safety-check blocks
// when gate is active (gate_refused) — produces exit 2, stderr plaintext.
func TestOpencodeSafetyCheckGateRefused(t *testing.T) {
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
		runSafetyCheck(cmd, tmpDir, "echo hello", "opencode")
	})

	if exitCode != 2 {
		t.Errorf("expected exit 2 for gate-refused opencode, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected empty stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "safety-block") {
		t.Errorf("expected safety-block in stderr for gate-refused opencode, got: %s", stderr)
	}
}
