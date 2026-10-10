package cli

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/testutil"
)

const taskPRTestURL = "https://github.com/org/repo/pull/42"

func prTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestTaskShowAndSoldierStateShowVerifiedThenUnverifiedPR proves both commands
// show the no-mistakes run's PR as delivered while its head is the worktree
// HEAD, show it as reported/unverified once the worktree moves past that head,
// and never leave phase working or write a pr_* meta key.
func TestTaskShowAndSoldierStateShowVerifiedThenUnverifiedPR(t *testing.T) {
	homeDir, wt := t.TempDir(), t.TempDir()
	initCLITestHome(t, homeDir)
	for _, args := range [][]string{
		{"task", "add", "pr1", "ship it", "--kind", "ship", "--repo", "munsu", "--home", homeDir},
		{"task", "start", "pr1", "--home", homeDir},
	} {
		if out, err := runTaskCommand(t, args); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	prTestGit(t, wt, "init", "-b", "mu/pr1")
	prTestGit(t, wt, "commit", "--allow-empty", "-m", "one")
	head := prTestGit(t, wt, "rev-parse", "HEAD")
	testutil.FakeOnPath(t, "no-mistakes", "#!/bin/sh\ncat <<'EOF'\nrun:\n  id: \"01J\"\n  branch: mu/pr1\n  status: completed\n  head: "+head+"\n  pr: \""+taskPRTestURL+"\"\noutcome: checks-passed\nEOF\n")
	if err := home.WriteMeta(homeDir, "pr1", map[string]string{"worktree": wt}); err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(homeDir, "pr1", "done: PR "+taskPRTestURL+" checks green [key=default]"); err != nil {
		t.Fatal(err)
	}

	observe := func() (show string, state struct{ PRState, PR, PRHead, Reason string }) {
		t.Helper()
		show, err := runTaskCommand(t, []string{"task", "show", "pr1", "--home", homeDir})
		if err != nil {
			t.Fatalf("task show: %v\n%s", err, show)
		}
		out, err := runTaskCommand(t, []string{"soldier-state", "pr1", "--home", homeDir, "--output", "json"})
		if err != nil {
			t.Fatalf("soldier-state: %v\n%s", err, out)
		}
		var resp struct {
			Data struct {
				Status string `json:"status"`
				State  string `json:"pr_state"`
				PR     string `json:"pr"`
				Head   string `json:"pr_head"`
				Reason string `json:"pr_unverified_reason"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("soldier-state json: %v\n%s", err, out)
		}
		if resp.Data.Status != "working" || !strings.Contains(show, "state: working") {
			t.Fatalf("phase must stay working:\n%s\n%s", show, out)
		}
		return show, struct{ PRState, PR, PRHead, Reason string }{resp.Data.State, resp.Data.PR, resp.Data.Head, resp.Data.Reason}
	}

	show, st := observe()
	if !strings.Contains(show, "delivered: PR "+taskPRTestURL+" @ "+head) || st.PRState != "delivered" || st.PR != taskPRTestURL || st.PRHead != head {
		t.Fatalf("verified: show=%q state=%+v", show, st)
	}

	prTestGit(t, wt, "commit", "--allow-empty", "-m", "two")
	show, st = observe()
	if !strings.Contains(show, "reported: PR "+taskPRTestURL+" (unverified: run head") || st.PRState != "reported" || st.PRHead != "" || !strings.Contains(st.Reason, "run head") {
		t.Fatalf("moved head: show=%q state=%+v", show, st)
	}

	meta, err := home.ReadMeta(homeDir, "pr1")
	if err != nil {
		t.Fatal(err)
	}
	for k := range meta {
		if strings.HasPrefix(k, "pr_") {
			t.Fatalf("meta key %q written by a read", k)
		}
	}
}
