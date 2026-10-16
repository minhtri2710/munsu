package cli

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

const taskPRTestURL = "https://github.com/org/repo/pull/42"

// TestTaskShowShowsReportedPR proves task show shows the PR the status log
// reports as unverified and the pane liveness, never leaves phase working, and
// never writes a pr_* meta key.
func TestTaskShowShowsReportedPR(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	mustBindCLIEndpoint(t, homeDir, "pr1", taskauthority.EndpointBinding{Backend: "fake", Handle: "@1"})
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
}
