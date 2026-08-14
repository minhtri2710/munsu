package cli

import (
	"io"
	"os"
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

// boundTaskFixture builds a primary checkout, a worktree bound to it, and the
// task authority home that records the binding — the shape of a real soldier
// run. It returns the primary and worktree paths with the environment already
// pointing at the binding.
func boundTaskFixture(t *testing.T, taskID string) (primary, worktree string) {
	t.Helper()
	primary = initGitRepoForSafety(t, t.TempDir())
	worktree = filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, taskID, primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", taskID)
	return primary, worktree
}

// TestFileWriteRefusedInBoundPrimaryCheckout is the blocked direction of C2: a
// native write tool aimed at the shared checkout of the bound repository is
// denied even though no shell command exists to inspect.
func TestFileWriteRefusedInBoundPrimaryCheckout(t *testing.T) {
	primary, worktree := boundTaskFixture(t, "ship-write")

	target := filepath.Join(primary, "internal", "backend", "worktree.go")
	code, stderr := runClaudeSafety(t, worktree, "", target)
	if code != 2 {
		t.Fatalf("write into bound primary checkout: exit=%d, want 2 (stderr=%q)", code, stderr)
	}
	if !strings.Contains(stderr, "shared primary checkout of the bound repository refused for file write") {
		t.Fatalf("deny reason does not name the bound repository: %q", stderr)
	}
}

// TestFileWriteAllowedInsideWorktree is the allowed direction of C2 and the one
// that matters most: a false positive here would stall every agent run.
func TestFileWriteAllowedInsideWorktree(t *testing.T) {
	_, worktree := boundTaskFixture(t, "ship-write")

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

// TestFileWriteAllowedInNestedRepositoryInsideWorktree is the false-positive
// case that matters: `Primary` means gitDir == commonDir, which is true of
// every clone and every `git init`. A reference repo or fixture the agent
// creates inside its own worktree must not be mistaken for the shared checkout.
func TestFileWriteAllowedInNestedRepositoryInsideWorktree(t *testing.T) {
	_, worktree := boundTaskFixture(t, "ship-nested")

	nested := filepath.Join(worktree, "vendor-lib")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepoForSafety(t, nested)

	target := filepath.Join(nested, "main.go")
	if code, stderr := runClaudeSafety(t, worktree, "", target); code != 0 {
		t.Fatalf("write into nested repository: exit=%d, want 0 (stderr=%q)", code, stderr)
	}
}

// TestFileWriteAllowedInUnrelatedRepository covers the scratch repo an agent
// clones or inits outside its worktree to reproduce a bug.
func TestFileWriteAllowedInUnrelatedRepository(t *testing.T) {
	_, worktree := boundTaskFixture(t, "ship-scratch")

	scratch := initGitRepoForSafety(t, t.TempDir())
	target := filepath.Join(scratch, "notes.md")
	if code, stderr := runClaudeSafety(t, worktree, "", target); code != 0 {
		t.Fatalf("write into unrelated repository: exit=%d, want 0 (stderr=%q)", code, stderr)
	}
}

// TestSafetyCheckAllowsCommandsInNestedRepository is the same false positive on
// the cwd side: a bash call whose cwd sits in a nested repo must run.
func TestSafetyCheckAllowsCommandsInNestedRepository(t *testing.T) {
	_, worktree := boundTaskFixture(t, "ship-nested-cmd")

	nested := filepath.Join(worktree, "vendor-lib")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepoForSafety(t, nested)

	for _, command := range []string{"ls -la", "echo hi > notes.md"} {
		if code, stderr := runClaudeSafety(t, nested, command, ""); code != 0 {
			t.Fatalf("%q in nested repository: exit=%d, want 0 (stderr=%q)", command, code, stderr)
		}
	}
}

// TestFileWriteAllowedOutsideAnyRepository pins the fail-open decision for
// targets git cannot classify: scratch directories are ordinary work.
func TestFileWriteAllowedOutsideAnyRepository(t *testing.T) {
	_, worktree := boundTaskFixture(t, "ship-write")

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

// TestFileWriteIntoPrimaryAllowedWithoutBinding pins the fail-open decision for
// a task run whose worktree binding cannot be read: an unreadable binding is
// not proof that the target is shared state.
func TestFileWriteIntoPrimaryAllowedWithoutBinding(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	t.Setenv("MUNSU_HOME", t.TempDir())
	t.Setenv("MUNSU_TASK_ID", "ship-unbound")

	target := filepath.Join(primary, "README.md")
	if code, stderr := runClaudeSafety(t, primary, "", target); code != 0 {
		t.Fatalf("write into primary without a binding: exit=%d, want 0 (stderr=%q)", code, stderr)
	}
}

// TestMissingFilePathFallsThrough pins the fail-open decision for a payload
// that carries no path: a matcher that fires for an unknown tool shape must
// behave exactly as it did before this guard existed.
func TestMissingFilePathFallsThrough(t *testing.T) {
	_, worktree := boundTaskFixture(t, "ship-write")

	if code, stderr := runClaudeSafety(t, worktree, "", ""); code != 0 {
		t.Fatalf("empty payload: exit=%d, want 0 (stderr=%q)", code, stderr)
	}
}

// TestSafetyCheckRefusesBoundPrimaryCheckout is the blocked direction of H1: a
// non-git command in the shared checkout reaches the hook and is refused on
// location, closing the path that opened BEO-40.
func TestSafetyCheckRefusesBoundPrimaryCheckout(t *testing.T) {
	primary, _ := boundTaskFixture(t, "ship-h1")

	for _, command := range []string{
		"python - <<'EOF'\nopen('x','w')\nEOF",
		"sed -i '' s/a/b/ README.md",
		"echo hi > README.md",
	} {
		code, stderr := runClaudeSafety(t, primary, command, "")
		if code != 2 {
			t.Fatalf("%q in bound primary checkout: exit=%d, want 2 (stderr=%q)", command, code, stderr)
		}
		if !strings.Contains(stderr, "shared primary checkout of the bound repository refused") {
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
	_, worktree := boundTaskFixture(t, "ship-h1")

	if code, stderr := runClaudeSafety(t, worktree, "echo hi > README.md", ""); code != 0 {
		t.Fatalf("worktree during task run: exit=%d, want 0 (stderr=%q)", code, stderr)
	}
}
