package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestReportDoneDoesNotCaptureDeliveryIdentity proves the terminal report
// path never captures or commits delivery identity through a parallel path:
// `report done` with a PR message proceeds as a plain uplink report and
// leaves no pr_* meta projection and no delivery state behind. Delivery
// truth is consumed from canonical authorization/outcome records only.
func TestReportDoneCompletesScoutLifecycle(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "scout-report")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_TASK_KIND", "scout")
	t.Setenv("MUNSU_PARENT_STATUS", homeDir)

	root := NewRootCommand()
	root.SetArgs([]string{"task", "add", "scout-report", "investigate", "--kind", "scout", "--scope", "investigate report", "--budget", "300"})
	if err := root.Execute(); err != nil {
		t.Fatalf("task add: %v", err)
	}
	root = NewRootCommand()
	root.SetArgs([]string{"task", "start", "scout-report"})
	if err := root.Execute(); err != nil {
		t.Fatalf("task start: %v", err)
	}
	root = NewRootCommand()
	root.SetArgs([]string{"report", "done", "findings recorded", "--key", "scout-report", "--ring", "no-ring"})
	if err := root.Execute(); err != nil {
		t.Fatalf("report done: %v", err)
	}

	auth, err := taskAuthorityForRead(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	tid, _ := domain.NewTaskID("scout-report")
	agg, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseDone {
		t.Fatalf("phase = %s, want done", agg.Phase)
	}
	// The scout's terminal report closes its own handoff: nothing
	// else in the binary can write the ack, so leaving it pending would wedge
	// the turn-end guard forever.
	if !orchestrator.IsReceiptAcked(homeDir, "scout-report", "scout-report") {
		t.Fatal("terminal receipt was not acknowledged by its writer")
	}
	pending, err := orchestrator.ListPendingReceipts(homeDir)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending receipts=%d err=%v, want 0", len(pending), err)
	}
	open, err := orchestrator.IsTaskReportRelayOpen(homeDir, "scout-report")
	if err != nil || open {
		t.Fatalf("relay obligation open=%v err=%v, want closed", open, err)
	}
}

func TestReportDoneDoesNotCaptureDeliveryIdentity(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()
	initCLITestHome(t, parentHome)

	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-report")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"report", "--ring", "no-ring", "done", "work landed, PR https://github.com/owner/repo/pull/7"})
	if err := root.Execute(); err != nil {
		t.Fatalf("report done: %v\n%s", err, buf.String())
	}

	meta, metaErr := home.ReadMeta(homeDir, "ship-report")
	if metaErr == nil {
		if _, hasPR := meta["pr_url"]; hasPR {
			t.Fatalf("report done wrote pr_url meta projection: %v", meta)
		}
	}
	// No delivery journal or canonical delivery evidence may exist.
	if _, err := os.Stat(filepath.Join(homeDir, "state", "delivery")); !os.IsNotExist(err) {
		t.Fatalf("report done created delivery state: %v", err)
	}
}

// TestReportResolvedRefusalReadsCanonicalKind pins the `resolved` refusal for
// a ship task: the kind is the canonical task's, never the .meta's, and a
// canonical record that cannot be read refuses. The blocking rows are a
// canonical ship whose .meta carries no kind and a corrupt canonical record;
// the allowing row is a canonical scout whose .meta claims ship.
func TestReportResolvedRefusalReadsCanonicalKind(t *testing.T) {
	const (
		shipRefusal = "cannot use 'resolved' as delivery completion"
		readRefusal = "cannot read task res-1 kind to check 'resolved'"
	)
	for _, tc := range []struct {
		name    string
		addArgs []string
		meta    map[string]string
		corrupt bool
		noTask  bool
		want    string
	}{
		{"canonical ship is refused with no meta kind", []string{"--kind", "ship"}, map[string]string{"window": "w"}, false, false, shipRefusal},
		{"unreadable canonical record is refused", []string{"--kind", "scout", "--scope", "investigate report", "--budget", "300"}, map[string]string{"kind": "scout"}, true, false, readRefusal},
		{"canonical scout passes although meta claims ship", []string{"--kind", "scout", "--scope", "investigate report", "--budget", "300"}, map[string]string{"kind": "ship"}, false, false, ""},
		{"task with no canonical record and no meta is allowed", nil, nil, false, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			initCLITestHome(t, homeDir)
			t.Setenv("MUNSU_HOME", homeDir)
			t.Setenv("MUNSU_TASK_ID", "res-1")
			t.Setenv("MUNSU_ROLE", "soldier")
			t.Setenv("MUNSU_PARENT_STATUS", homeDir)
			if !tc.noTask {
				if out, err := runTaskCommand(t, append([]string{"task", "add", "res-1", "resolve", "--home", homeDir}, tc.addArgs...)); err != nil {
					t.Fatalf("task add: %v\n%s", err, out)
				}
				if err := home.WriteMeta(homeDir, "res-1", tc.meta); err != nil {
					t.Fatal(err)
				}
			}
			if tc.corrupt {
				current := filepath.Join(homeDir, "state", "task-authority", "tasks", "res-1", "current.json")
				if err := os.WriteFile(current, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			root := NewRootCommand()
			root.SetArgs([]string{"report", "resolved", "done with it", "--key", "res-1", "--ring", "no-ring"})
			err := root.Execute()
			for _, refusal := range []string{shipRefusal, readRefusal} {
				got := err != nil && strings.Contains(err.Error(), refusal)
				if got != (refusal == tc.want) {
					t.Fatalf("report resolved err = %v, refusal %q seen=%v, want only %q", err, refusal, got, tc.want)
				}
			}
			if tc.noTask {
				if err != nil {
					t.Fatalf("report resolved for a task with no canonical record: %v", err)
				}
				lines, statusErr := home.ReadStatus(homeDir, "res-1")
				if statusErr != nil || len(lines) == 0 || !strings.Contains(lines[len(lines)-1], "resolved") || !strings.Contains(lines[len(lines)-1], "done with it") {
					t.Fatalf("status lines = %q (%v), want a resolved line ending the file", lines, statusErr)
				}
			}
		})
	}
}
