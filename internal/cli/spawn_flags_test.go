package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestSpawnRejectsReopenFlag(t *testing.T) {
	t.Setenv("MUNSU_HOME", t.TempDir())
	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "--reopen"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --reopen") {
		t.Fatalf("spawn --reopen = %v, want unknown flag", err)
	}
}

// TestSpawnTakesTheProjectFromTheTask: spawn refuses a task that records no
// project and passes the project check for one that does.
func TestSpawnTakesTheProjectFromTheTask(t *testing.T) {
	for _, tc := range []struct {
		name    string
		project string
		refused bool
	}{
		{"blocks a task with no project", "", true},
		{"allows a task with a project", "munsu", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			initCLITestHome(t, homeDir)
			auth := cliCanonicalForHome(t, homeDir)
			req := taskauthority.CanonicalCreateRequest{HomeID: auth.HomeID(), TaskID: mustTaskIDFor(t, "t1"), Owner: "owner", Description: "work", Kind: "ship", Reason: "test"}
			if tc.project != "" {
				pid, err := domain.NewProjectID(tc.project)
				if err != nil {
					t.Fatal(err)
				}
				req.Project = pid
			}
			if _, err := auth.Create(mustCanonicalOp(t, "op-create-t1", req), req); err != nil {
				t.Fatal(err)
			}
			_, err := runRoot(t, "spawn", "t1", "--home", homeDir)
			if err == nil {
				t.Fatal("spawn succeeded; this fixture has no registered project to launch into")
			}
			if got := strings.Contains(err.Error(), "the task records no project"); got != tc.refused {
				t.Fatalf("spawn error = %v, want the no-project refusal %t", err, tc.refused)
			}
		})
	}
}
