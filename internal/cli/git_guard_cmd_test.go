package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The argv fence is the shim's entry to the same worktree-binding core the
// string path enforces. These tests exercise it directly (checkPath is a
// parameter, so no chdir is needed) on a real bound worktree.

func TestEvaluateGitArgvSafetyAllowsReadAndBoundMutations(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-argv", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-argv")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-argv")

	allowed := [][]string{
		{"status", "--short"},
		{"branch", "--show-current"},
		{"-C", ".", "add", "file.txt"},
		{"commit", "-m", "work"},
		{"push", "origin", "HEAD:refs/heads/mu/ship-argv"},
	}
	for _, argv := range allowed {
		block, reason := evaluateGitArgvSafety(worktree, argv)
		if block {
			t.Fatalf("argv %v blocked: %s", argv, reason)
		}
	}
}

// TestEvaluateGitArgvSafetyClosesShellWrapperResidual proves the shim closes the
// residual the PreToolUse hook leaves open: a shell wrapper such as
// `sh -c 'git push origin --force refs/heads/mu/ship-argv'` reaches the real git
// through the shim's PATH entry, so the shell has already word-split the command
// and the guard receives the same argv any direct invocation would. Force-push
// is denied even against the bound task branch.
func TestEvaluateGitArgvSafetyClosesShellWrapperResidual(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-force", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-force")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-force")

	denied := [][]string{
		{"push", "origin", "--force", "refs/heads/mu/ship-force"},
		{"push", "origin", "+refs/heads/mu/ship-force"},
		{"push", "origin", "--delete", "mu/ship-force"},
		{"branch", "-D", "mu/ship-force"},
		{"reset", "--hard", "HEAD~1"},
	}
	for _, argv := range denied {
		block, reason := evaluateGitArgvSafety(worktree, argv)
		if !block || reason == "" {
			t.Fatalf("argv %v block=%v reason=%q, want deny", argv, block, reason)
		}
	}
}

func TestEvaluateGitArgvSafetyFailsClosedWithoutBinding(t *testing.T) {
	worktree := t.TempDir()
	t.Setenv("MUNSU_HOME", "")
	t.Setenv("MUNSU_TASK_ID", "")
	block, reason := evaluateGitArgvSafety(worktree, []string{"add", "file.txt"})
	if !block || !strings.Contains(reason, "active munsu task worktree binding") {
		t.Fatalf("block=%v reason=%q, want fail-closed binding refusal", block, reason)
	}
}

func TestRunGitGuardRefusesBlockedArgv(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-guard", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-guard")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-guard")
	t.Chdir(worktree)

	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	_, stderr := captureBoth(func() {
		_ = runGitGuard([]string{"push", "origin", "--force", "refs/heads/mu/ship-guard"})
	})
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1 for blocked git mutation", exitCode)
	}
	if !strings.Contains(stderr, "[git-fence]") {
		t.Fatalf("stderr = %q, want [git-fence] refusal marker", stderr)
	}
}

func TestRunGitGuardExecsRealGitForAllowedRead(t *testing.T) {
	if os.Getenv("MUNSU_GIT_GUARD_ALLOWED_HELPER") == "1" {
		if err := runGitGuard([]string{"--version"}); err != nil {
			os.Exit(2)
		}
		return
	}
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-read-guard", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-read-guard")
	t.Chdir(worktree)

	var exitCode int
	called := false
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code; called = true }
	defer func() { exitWithCode = oldExit }()

	cmd := exec.Command(os.Args[0], "-test.run=^TestRunGitGuardExecsRealGitForAllowedRead$", "-test.v")
	cmd.Env = append(os.Environ(), "MUNSU_GIT_GUARD_ALLOWED_HELPER=1")
	if err := cmd.Run(); err != nil {
		t.Fatalf("helper process error = %v", err)
	}
	if called {
		t.Fatalf("exitWithCode called (code=%d) for a passing read; real git handoff expected", exitCode)
	}
}

func TestGitGuardRefuseExitsNonZero(t *testing.T) {
	var exitCode int
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	_, stderr := captureBoth(func() {
		_ = gitGuardRefuse("boom")
	})
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr, "[git-fence] boom") {
		t.Fatalf("stderr = %q, want fence reason", stderr)
	}
}

func TestStripShimDirFromPath(t *testing.T) {
	home := t.TempDir()
	shimDir := filepath.Join(home, "state", "shim", "bin")
	other := t.TempDir()
	sep := string(filepath.ListSeparator)

	t.Run("removes shim dir when present", func(t *testing.T) {
		t.Setenv("PATH", shimDir+sep+other)
		stripShimDirFromPath()
		got := filepath.SplitList(os.Getenv("PATH"))
		for _, e := range got {
			if e == shimDir {
				t.Fatalf("shim dir still on PATH: %v", got)
			}
		}
		if len(got) != 1 || got[0] != other {
			t.Fatalf("PATH = %v, want only %q", got, other)
		}
	})

	// The strip matches the …/state/shim/bin suffix, not a dir rederived from
	// MUNSU_HOME, so it removes the shim even when MUNSU_HOME is unset or points
	// elsewhere. This is the recursion guard: a strip that keyed off MUNSU_HOME
	// would leave the shim on PATH here, and the real-git handoff would re-invoke
	// the shim without bound.
	t.Run("removes shim dir regardless of MUNSU_HOME", func(t *testing.T) {
		t.Setenv("MUNSU_HOME", "")
		t.Setenv("PATH", shimDir+sep+other)
		stripShimDirFromPath()
		got := filepath.SplitList(os.Getenv("PATH"))
		if len(got) != 1 || got[0] != other {
			t.Fatalf("PATH = %v, want shim stripped leaving only %q", got, other)
		}
	})

	t.Run("no-op when shim dir absent", func(t *testing.T) {
		t.Setenv("PATH", other)
		stripShimDirFromPath()
		if got := os.Getenv("PATH"); got != other {
			t.Fatalf("PATH = %q, want unchanged", got)
		}
	})
}

// TestGuardDoesNotRecurseIntoShim composes the two halves of the fence: the root
// PATH strip followed by the guard's real-git handoff. A fake `git` under the
// shim dir writes a marker (standing in for "the shim was resolved again"); the
// real git the guard should reach is a second fake `git` that does not. With
// MUNSU_HOME unset — the case that used to defeat the strip and recurse — the
// guard must resolve the real git, so the shim marker must never be written.
func TestGuardDoesNotRecurseIntoShim(t *testing.T) {
	home := t.TempDir()
	shimDir := filepath.Join(home, "state", "shim", "bin")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "shim-was-hit")
	writeExecutable(t, filepath.Join(shimDir, "git"), "#!/bin/sh\nprintf x > "+marker+"\nexit 0\n")

	realBin := t.TempDir()
	realMarker := filepath.Join(home, "real-git-was-hit")
	writeExecutable(t, filepath.Join(realBin, "git"), "#!/bin/sh\nprintf x > "+realMarker+"\nexit 23\n")

	t.Setenv("MUNSU_HOME", "")
	t.Setenv("MUNSU_TASK_ID", "")
	t.Setenv("PATH", shimDir+string(filepath.ListSeparator)+realBin)
	t.Chdir(t.TempDir())

	if os.Getenv("MUNSU_GIT_GUARD_HELPER") == "1" {
		marker = os.Getenv("MUNSU_GIT_GUARD_SHIM_MARKER")
		realMarker = os.Getenv("MUNSU_GIT_GUARD_REAL_MARKER")
		t.Setenv("PATH", os.Getenv("MUNSU_GIT_GUARD_TEST_PATH"))
		t.Setenv("MUNSU_HOME", "")
		t.Setenv("MUNSU_TASK_ID", "")
		if err := os.Chdir(os.Getenv("MUNSU_GIT_GUARD_TEST_CWD")); err != nil {
			t.Fatal(err)
		}
		stripShimDirFromPath()
		if err := runGitGuard([]string{"--version"}); err != nil {
			os.Exit(2)
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestGuardDoesNotRecurseIntoShim$", "-test.v")
	cmd.Env = append(os.Environ(), "MUNSU_GIT_GUARD_HELPER=1", "MUNSU_GIT_GUARD_TEST_PATH="+shimDir+string(filepath.ListSeparator)+realBin, "MUNSU_GIT_GUARD_TEST_CWD="+t.TempDir(), "MUNSU_GIT_GUARD_SHIM_MARKER="+marker, "MUNSU_GIT_GUARD_REAL_MARKER="+realMarker)
	if err := cmd.Run(); err == nil {
		t.Fatal("helper process unexpectedly succeeded")
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 23 {
		t.Fatalf("helper process error = %v, want exit 23", err)
	}
	if _, err := os.Stat(realMarker); err != nil {
		t.Fatalf("real git marker missing: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("shim was resolved a second time: the strip did not prevent recursion")
	}
}
