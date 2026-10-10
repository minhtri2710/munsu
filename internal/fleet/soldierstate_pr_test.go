package fleet

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/testutil"
)

const taskPRURL = "https://github.com/org/repo/pull/42"

func gitInRepo(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestReadTaskPR proves the read-time PR is "delivered" only when the
// no-mistakes run names this task's branch, the worktree's branch and the
// worktree's HEAD, and is otherwise "reported" from the status log with the
// failed condition named.
func TestReadTaskPR(t *testing.T) {
	const id = "t1"
	doneLine := "done: PR " + taskPRURL + " checks green"
	reported := func(reason string) func(string) string {
		return func(string) string { return "reported: PR " + taskPRURL + " (unverified: " + reason }
	}
	tests := []struct {
		name     string
		worktree bool
		wtBranch string                   // worktree branch; "" = mu/t1
		run      func(head string) string // fake `axi status` output; "" = no run
		status   []string
		want     func(head string) string
	}{
		{name: "verified", worktree: true,
			run:  func(h string) string { return runOut("mu/t1", h, taskPRURL) },
			want: func(h string) string { return "delivered: PR " + taskPRURL + " @ " + h }},
		{name: "head mismatch", worktree: true,
			run:    func(string) string { return runOut("mu/t1", "deadbeef", taskPRURL) },
			status: []string{doneLine}, want: reported("run head deadbeef")},
		{name: "run branch is not the task branch", worktree: true, wtBranch: "feature",
			run:    func(h string) string { return runOut("feature", h, taskPRURL) },
			status: []string{doneLine}, want: reported("run branch feature is not the task branch mu/t1")},
		{name: "worktree is not on the run branch", worktree: true, wtBranch: "feature",
			run:    func(h string) string { return runOut("mu/t1", h, taskPRURL) },
			status: []string{doneLine}, want: reported("worktree is on feature, not the run branch mu/t1")},
		{name: "run has no PR", worktree: true,
			run:    func(h string) string { return runOut("mu/t1", h, "") },
			status: []string{doneLine}, want: reported("run has no PR)")},
		{name: "no run", worktree: true,
			run:    func(string) string { return "" },
			status: []string{doneLine}, want: reported("no no-mistakes run)")},
		{name: "no worktree",
			run:    func(string) string { return "" },
			status: []string{doneLine}, want: reported("no worktree)")},
		{name: "newer non-PR URL does not hide the PR line", worktree: true,
			run:    func(string) string { return "" },
			status: []string{doneLine, "blocked: see https://ci.example.com/log/9"}, want: reported("no no-mistakes run)")},
		{name: "trailing punctuation is trimmed", worktree: true,
			run:    func(string) string { return "" },
			status: []string{"done: PR (" + taskPRURL + "). green"}, want: reported("no no-mistakes run)")},
		{name: "no PR anywhere", worktree: true,
			run:    func(h string) string { return runOut("mu/t1", h, "") },
			status: []string{"working: spawned", "blocked: see https://ci.example.com/log/9"},
			want:   func(string) string { return "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			homeDir, wt := t.TempDir(), t.TempDir()
			gitInRepo(t, wt, "init", "-b", "mu/t1")
			gitInRepo(t, wt, "commit", "--allow-empty", "-m", "c")
			if tt.wtBranch != "" {
				gitInRepo(t, wt, "checkout", "-b", tt.wtBranch)
			}
			head := gitInRepo(t, wt, "rev-parse", "HEAD")
			script := "#!/bin/sh\nexit 1\n"
			if out := tt.run(head); out != "" {
				script = "#!/bin/sh\ncat <<'EOF'\n" + out + "\nEOF\n"
			}
			testutil.FakeOnPath(t, "no-mistakes", script)
			meta := map[string]string{"kind": "ship"}
			if tt.worktree {
				meta["worktree"] = wt
			}
			if err := home.WriteMeta(homeDir, id, meta); err != nil {
				t.Fatal(err)
			}
			for _, line := range tt.status {
				if err := home.AppendStatus(homeDir, id, line); err != nil {
					t.Fatal(err)
				}
			}
			got := ReadTaskPR(homeDir, id).Line()
			if want := tt.want(head); got != "" && !strings.HasPrefix(got, want) || got == "" && want != "" {
				t.Fatalf("Line() = %q, want prefix %q", got, want)
			}
		})
	}
}

func runOut(branch, head, pr string) string {
	out := "run:\n  id: \"01JTEST\"\n  branch: " + branch + "\n  status: completed\n  head: " + head + "\n"
	if pr != "" {
		out += "  pr: \"" + pr + "\"\n"
	}
	return out + "outcome: checks-passed"
}
