package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluateGitArgvSafetyRequiresTaskLocalNoMistakesPushMapping(t *testing.T) {
	const taskBranch = "mu/ship-gate-config"
	cases := []struct {
		name    string
		config  [][]string
		refused string
	}{
		{"block: no gate remote url", nil, "remote.no-mistakes.url is not configured"},
		{"block: no push mapping", [][]string{{"remote.no-mistakes.url", "@primary"}}, "remote.no-mistakes.push must explicitly name the task branch"},
		{"block: mapping names another task branch", [][]string{{"remote.no-mistakes.url", "@primary"}, {"remote.no-mistakes.push", "HEAD:refs/heads/mu/other-task"}}, "remote.no-mistakes.push must name only the current task branch"},
		{"block: more than one mapping", [][]string{{"remote.no-mistakes.url", "@primary"}, {"remote.no-mistakes.push", "HEAD:refs/heads/mu/other-task"}, {"--add", "remote.no-mistakes.push", "HEAD:refs/heads/" + taskBranch}}, "remote.no-mistakes.push must name only the current task branch"},
		{"allow: one mapping for the task branch", [][]string{{"remote.no-mistakes.url", "@primary"}, {"remote.no-mistakes.push", "HEAD:refs/heads/" + taskBranch}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			primary := initGitRepoForSafety(t, t.TempDir())
			worktree := filepath.Join(t.TempDir(), "wt")
			runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
			homeDir := bindSafetyWorktree(t, "ship-gate-config", primary, worktree)
			t.Setenv("MUNSU_HOME", homeDir)
			t.Setenv("MUNSU_TASK_ID", "ship-gate-config")
			runGitForSafety(t, worktree, "checkout", "-b", taskBranch)
			for _, kv := range tc.config {
				args := append([]string{"config"}, kv...)
				for i, arg := range args {
					if arg == "@primary" {
						args[i] = primary
					}
				}
				runGitForSafety(t, worktree, args...)
			}

			blocked, reason := evaluateGitArgvSafety(worktree, []string{"push", "no-mistakes"})
			if tc.refused == "" {
				if blocked {
					t.Fatalf("gate push = blocked, reason %q; want allowed", reason)
				}
				return
			}
			if !blocked || !strings.Contains(reason, tc.refused) {
				t.Fatalf("gate push = blocked %v, reason %q; want %q", blocked, reason, tc.refused)
			}
		})
	}
}
