package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluateGitArgvSafetyRequiresTaskLocalNoMistakesPushMapping(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-gate-config", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-gate-config")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-gate-config")
	argv := []string{"push", "no-mistakes"}

	blocked, reason := evaluateGitArgvSafety(worktree, argv)
	if !blocked || !strings.Contains(reason, "remote.no-mistakes.url is not configured") {
		t.Fatalf("gate push without gate remote = blocked %v, reason %q; want configured-remote refusal", blocked, reason)
	}
	runGitForSafety(t, worktree, "config", "remote.no-mistakes.url", primary)
	blocked, reason = evaluateGitArgvSafety(worktree, argv)
	if !blocked || !strings.Contains(reason, "remote.no-mistakes.push must explicitly name the task branch") {
		t.Fatalf("gate push without push mapping = blocked %v, reason %q; want explicit-mapping refusal", blocked, reason)
	}
	runGitForSafety(t, worktree, "config", "remote.no-mistakes.push", "HEAD:refs/heads/mu/other-task")
	blocked, reason = evaluateGitArgvSafety(worktree, argv)
	if !blocked || !strings.Contains(reason, "remote.no-mistakes.push must name only the current task branch") {
		t.Fatalf("gate push to another task branch = blocked %v, reason %q; want task-local mapping refusal", blocked, reason)
	}
	runGitForSafety(t, worktree, "config", "--add", "remote.no-mistakes.push", "HEAD:refs/heads/mu/ship-gate-config")
	blocked, reason = evaluateGitArgvSafety(worktree, argv)
	if !blocked || !strings.Contains(reason, "remote.no-mistakes.push must name only the current task branch") {
		t.Fatalf("gate push with multiple mappings = blocked %v, reason %q; want single-mapping refusal", blocked, reason)
	}
}
