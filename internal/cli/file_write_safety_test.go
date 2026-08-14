package cli

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// runClaudeSafety runs safety-check in the Claude output shape and returns the
// exit code and stderr, i.e. exactly what the PreToolUse hook sees.
func runClaudeSafety(t *testing.T, checkPath, command, filePath string) (int, string) {
	t.Helper()
	exitCode := 0
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	_, stderr := captureBoth(func() {
		if err := runSafetyCheck(cmd, checkPath, command, filePath, "claude"); err != nil {
			exitCode = 1
		}
	})
	return exitCode, stderr
}

// TestFileWriteRefusedInPrimaryCheckoutDuringTaskRun is the blocked direction of
// C2: a native write tool aimed at the shared primary checkout is denied even
// though no shell command exists to inspect.
func TestFileWriteRefusedInPrimaryCheckoutDuringTaskRun(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	t.Setenv("MUNSU_HOME", t.TempDir())
	t.Setenv("MUNSU_TASK_ID", "ship-write")

	target := filepath.Join(primary, "internal", "backend", "worktree.go")
	code, stderr := runClaudeSafety(t, worktree, "", target)
	if code != 2 {
		t.Fatalf("write into primary checkout: exit=%d, want 2 (stderr=%q)", code, stderr)
	}
	if !strings.Contains(stderr, "primary checkout refused for file write") {
		t.Fatalf("deny reason does not name the primary checkout: %q", stderr)
	}
}

// TestFileWriteAllowedInsideWorktree is the allowed direction of C2 and the one
// that matters most: a false positive here would stall every agent run.
func TestFileWriteAllowedInsideWorktree(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	t.Setenv("MUNSU_HOME", t.TempDir())
	t.Setenv("MUNSU_TASK_ID", "ship-write")

	for _, target := range []string{
		filepath.Join(worktree, "README.md"),
		filepath.Join(worktree, "internal", "new", "file.go"), // path does not exist yet
	} {
		code, stderr := runClaudeSafety(t, worktree, "", target)
		if code != 0 {
			t.Fatalf("write into worktree %s: exit=%d, want 0 (stderr=%q)", target, code, stderr)
		}
	}
}

// TestFileWriteAllowedOutsideAnyRepository pins the fail-open decision for
// targets git cannot classify: scratch directories are ordinary work.
func TestFileWriteAllowedOutsideAnyRepository(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	t.Setenv("MUNSU_HOME", t.TempDir())
	t.Setenv("MUNSU_TASK_ID", "ship-write")

	target := filepath.Join(t.TempDir(), "scratch", "notes.md")
	if code, stderr := runClaudeSafety(t, worktree, "", target); code != 0 {
		t.Fatalf("write outside any repository: exit=%d, want 0 (stderr=%q)", code, stderr)
	}
}

// TestFileWriteIntoPrimaryAllowedOutsideTaskRun pins the L3 whitelist boundary:
// without MUNSU_TASK_ID this is a general or a human, and munsu's own sync
// paths write into the primary checkout on purpose.
func TestFileWriteIntoPrimaryAllowedOutsideTaskRun(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	t.Setenv("MUNSU_HOME", t.TempDir())
	t.Setenv("MUNSU_TASK_ID", "")

	target := filepath.Join(primary, "README.md")
	if code, stderr := runClaudeSafety(t, primary, "", target); code != 0 {
		t.Fatalf("write into primary outside a task run: exit=%d, want 0 (stderr=%q)", code, stderr)
	}
}

// TestMissingFilePathFallsThrough pins the fail-open decision for a payload
// that carries no path: a matcher that fires for an unknown tool shape must
// behave exactly as it did before this guard existed.
func TestMissingFilePathFallsThrough(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	t.Setenv("MUNSU_HOME", t.TempDir())
	t.Setenv("MUNSU_TASK_ID", "ship-write")

	if code, stderr := runClaudeSafety(t, worktree, "", ""); code != 0 {
		t.Fatalf("empty payload: exit=%d, want 0 (stderr=%q)", code, stderr)
	}
}

// TestSafetyCheckRefusesPrimaryCheckoutDuringTaskRun is the blocked direction
// of H1: a non-git command in the primary checkout reaches the hook and is now
// refused on location, closing the path that opened BEO-40.
func TestSafetyCheckRefusesPrimaryCheckoutDuringTaskRun(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	t.Setenv("MUNSU_HOME", t.TempDir())
	t.Setenv("MUNSU_TASK_ID", "ship-h1")

	for _, command := range []string{
		"python - <<'EOF'\nopen('x','w')\nEOF",
		"sed -i '' s/a/b/ README.md",
		"echo hi > README.md",
	} {
		code, stderr := runClaudeSafety(t, primary, command, "")
		if code != 2 {
			t.Fatalf("%q in primary checkout: exit=%d, want 2 (stderr=%q)", command, code, stderr)
		}
		if !strings.Contains(stderr, "primary checkout refused inside an active munsu task run") {
			t.Fatalf("%q deny reason unexpected: %q", command, stderr)
		}
	}
}

// TestSafetyCheckAllowsPrimaryCheckoutOutsideTaskRun is the allowed direction
// of H1. Generals and humans work in the primary checkout; refusing them
// unconditionally would break every non-task session.
func TestSafetyCheckAllowsPrimaryCheckoutOutsideTaskRun(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	t.Setenv("MUNSU_HOME", t.TempDir())
	t.Setenv("MUNSU_TASK_ID", "")

	if code, stderr := runClaudeSafety(t, primary, "echo hi > README.md", ""); code != 0 {
		t.Fatalf("primary checkout outside a task run: exit=%d, want 0 (stderr=%q)", code, stderr)
	}
}

// TestSafetyCheckAllowsWorktreeDuringTaskRun is the allowed direction that
// covers the normal soldier flow.
func TestSafetyCheckAllowsWorktreeDuringTaskRun(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	t.Setenv("MUNSU_HOME", t.TempDir())
	t.Setenv("MUNSU_TASK_ID", "ship-h1")

	if code, stderr := runClaudeSafety(t, worktree, "echo hi > README.md", ""); code != 0 {
		t.Fatalf("worktree during task run: exit=%d, want 0 (stderr=%q)", code, stderr)
	}
}
