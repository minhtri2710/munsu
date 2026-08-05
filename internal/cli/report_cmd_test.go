package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// TestReportDoneDoesNotCaptureDeliveryIdentity proves the terminal report
// path never captures or commits delivery identity through a parallel path:
// `report done` with a PR message proceeds as a plain uplink report and
// leaves no pr_* meta projection and no delivery state behind. Delivery
// truth is consumed from canonical authorization/outcome records only.
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
