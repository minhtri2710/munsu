package cli

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

const taskPRTestURL = "https://github.com/org/repo/pull/42"

// TestTaskShowShowsReportedPR proves task show shows the PR the status log
// reports as unverified and the pane liveness, never leaves phase working, and
// never writes a pr_* meta key.
func TestTaskShowShowsReportedPR(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	registerCLITestProject(t, homeDir, "munsu")
	for _, args := range [][]string{
		{"task", "add", "pr1", "ship it", "--kind", "ship", "--repo", "munsu", "--home", homeDir},
		{"task", "start", "pr1", "--home", homeDir},
	} {
		if out, err := runTaskCommand(t, args); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if err := home.WriteMeta(homeDir, "pr1", map[string]string{"kind": "ship", "window": "@1"}); err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(homeDir, "pr1", "done: PR "+taskPRTestURL+" checks green [key=default]"); err != nil {
		t.Fatal(err)
	}

	show, err := runTaskCommand(t, []string{"task", "show", "pr1", "--home", homeDir})
	if err != nil {
		t.Fatalf("task show: %v\n%s", err, show)
	}
	if !strings.Contains(show, "state: working") || !strings.Contains(show, "pane_alive: false") {
		t.Fatalf("phase must stay working:\n%s", show)
	}
	if !strings.Contains(show, "reported: PR "+taskPRTestURL+" (unverified)") {
		t.Fatalf("reported PR: show=%q", show)
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
