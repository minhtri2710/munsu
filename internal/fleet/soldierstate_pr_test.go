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
// no-mistakes run names this task's branch and the worktree's HEAD, and is
// otherwise "reported" from the status log with the failed condition named.
func TestReadTaskPR(t *testing.T) {
	const id = "t1"
	tests := []struct {
		name       string
		worktree   bool
		run        func(head string) string // fake `axi status` output; "" = no run
		statusLine string
		want       func(head string) string
	}{
		{"verified", true,
			func(h string) string { return runOut("mu/t1", h, taskPRURL) }, "",
			func(h string) string { return "delivered: PR " + taskPRURL + " @ " + h }},
		{"head mismatch", true,
			func(string) string { return runOut("mu/t1", "deadbeef", taskPRURL) }, "done: PR " + taskPRURL + " checks green",
			func(string) string { return "reported: PR " + taskPRURL + " (unverified: run head deadbeef" }},
		{"branch mismatch", true,
			func(h string) string { return runOut("mu/other", h, taskPRURL) }, "done: PR " + taskPRURL + " checks green",
			func(string) string { return "reported: PR " + taskPRURL + " (unverified: run branch mu/other" }},
		{"run has no PR", true,
			func(h string) string { return runOut("mu/t1", h, "") }, "done: PR " + taskPRURL + " checks green",
			func(string) string { return "reported: PR " + taskPRURL + " (unverified: run has no PR)" }},
		{"no run", true,
			func(string) string { return "" }, "done: PR " + taskPRURL + " checks green",
			func(string) string { return "reported: PR " + taskPRURL + " (unverified: no no-mistakes run)" }},
		{"no worktree", false,
			func(string) string { return "" }, "done: PR " + taskPRURL + " checks green",
			func(string) string { return "reported: PR " + taskPRURL + " (unverified: no worktree)" }},
		{"no PR anywhere", true,
			func(h string) string { return runOut("mu/t1", h, "") }, "working: spawned",
			func(string) string { return "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			homeDir, wt := t.TempDir(), t.TempDir()
			gitInRepo(t, wt, "init", "-b", "mu/t1")
			gitInRepo(t, wt, "commit", "--allow-empty", "-m", "c")
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
			if tt.statusLine != "" {
				if err := home.AppendStatus(homeDir, id, tt.statusLine); err != nil {
					t.Fatal(err)
				}
			}
			got := ReadTaskPR(homeDir, id).Line()
			if want := tt.want(head); want == "" && got != "" || !strings.HasPrefix(got, want) {
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
