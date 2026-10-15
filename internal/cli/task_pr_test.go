package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

const taskPRTestURL = "https://github.com/org/repo/pull/42"

// TestTaskShowAndSoldierStateShowReportedPR proves both commands show the PR
// the status log reports as unverified, never leave phase working, and never
// write a pr_* meta key.
func TestTaskShowAndSoldierStateShowReportedPR(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	for _, args := range [][]string{
		{"task", "add", "pr1", "ship it", "--kind", "ship", "--repo", "munsu", "--home", homeDir},
		{"task", "start", "pr1", "--home", homeDir},
	} {
		if out, err := runTaskCommand(t, args); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if err := home.WriteMeta(homeDir, "pr1", map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(homeDir, "pr1", "done: PR "+taskPRTestURL+" checks green [key=default]"); err != nil {
		t.Fatal(err)
	}

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
			PR     string `json:"pr"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("soldier-state json: %v\n%s", err, out)
	}
	if resp.Data.Status != "working" || !strings.Contains(show, "state: working") {
		t.Fatalf("phase must stay working:\n%s\n%s", show, out)
	}
	if !strings.Contains(show, "reported: PR "+taskPRTestURL+" (unverified)") || resp.Data.PR != taskPRTestURL {
		t.Fatalf("reported PR: show=%q soldier-state=%s", show, out)
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
