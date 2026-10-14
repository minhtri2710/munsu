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
		runSafetyCheck(cmd, gitDir, "munsu watch", "", "codex")
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
	stdinPayload := `{"hookEventName":"PreToolUse","tool_input":{"command":"munsu watch"}}`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte(stdinPayload))
	w.Close()
	os.Stdin = r

	stdout, stderr := captureBoth(func() {
		runSafetyCheck(cmd, gitDir, "", "", "codex")
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
