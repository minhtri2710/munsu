package cli

import (
	"bytes"
	"os"
	"path/filepath"
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
	root.SetArgs([]string{"report", "done", "report.md", "--key", "scout-report", "--ring", "no-ring"})
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
	// The scout's terminal report closes its own handoff (ADR-0015): nothing
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
