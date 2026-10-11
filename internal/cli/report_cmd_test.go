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
// a ship task: the kind is the canonical task's, never the .meta's. The
// blocking row is a canonical ship whose .meta carries no kind; the allowing
// row is a canonical scout whose .meta claims ship.
func TestReportResolvedRefusalReadsCanonicalKind(t *testing.T) {
	const refusal = "cannot use 'resolved' as delivery completion"
	for _, tc := range []struct {
		name      string
		addArgs   []string
		meta      map[string]string
		wantBlock bool
	}{
		{"canonical ship is refused with no meta kind", []string{"--kind", "ship"}, map[string]string{"window": "w"}, true},
		{"canonical scout passes although meta claims ship", []string{"--kind", "scout", "--scope", "investigate report", "--budget", "300"}, map[string]string{"kind": "ship"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			initCLITestHome(t, homeDir)
			t.Setenv("MUNSU_HOME", homeDir)
			t.Setenv("MUNSU_TASK_ID", "res-1")
			t.Setenv("MUNSU_ROLE", "soldier")
			t.Setenv("MUNSU_PARENT_STATUS", homeDir)
			if out, err := runTaskCommand(t, append([]string{"task", "add", "res-1", "resolve", "--home", homeDir}, tc.addArgs...)); err != nil {
				t.Fatalf("task add: %v\n%s", err, out)
			}
			if err := home.WriteMeta(homeDir, "res-1", tc.meta); err != nil {
				t.Fatal(err)
			}
			root := NewRootCommand()
			root.SetArgs([]string{"report", "resolved", "done with it", "--key", "res-1", "--ring", "no-ring"})
			err := root.Execute()
			blocked := err != nil && strings.Contains(err.Error(), refusal)
			if blocked != tc.wantBlock {
				t.Fatalf("report resolved blocked=%v (err=%v), want blocked=%v", blocked, err, tc.wantBlock)
			}
		})
	}
}
