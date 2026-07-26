package cli

import (
	"bytes"
	"testing"

	"github.com/minhtri2710/munsu/internal/mailbox"
	"github.com/minhtri2710/munsu/internal/uplink"
)

func TestReportCmdMaterialSoldierUsesMailboxOnly(t *testing.T) {
	soldierHome := t.TempDir()
	captainHome := t.TempDir()
	t.Setenv("MUNSU_HOME", soldierHome)
	t.Setenv("MUNSU_TASK_ID", "task:one")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", captainHome)

	root := NewRootCommand()
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"report", "--ring", "no-ring", "failed", "tests failed"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	pending, err := mailbox.NewStore(captainHome).ListPending("task_one")
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%d err=%v", len(pending), err)
	}
	if pending[0].ReceiverRank != mailbox.RankCaptain {
		t.Fatalf("receiver rank=%s", pending[0].ReceiverRank)
	}
	if env, _ := mailbox.NewStore(captainHome).ReadEnvelope("task_one", pending[0].MessageID); env == nil {
		t.Fatal("Captain inbox should contain the Uplink Report")
	}
	if !uplink.HasOpenReport(captainHome, "task:one", "default") {
		t.Fatal("open evidence missing")
	}
}

func TestReportCmdMaterialCaptainUsesGeneralMailbox(t *testing.T) {
	captainHome := t.TempDir()
	generalHome := t.TempDir()
	if err := mailbox.WriteHomeIdentity(captainHome, "captain-one", mailbox.RankCaptain); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", captainHome)
	t.Setenv("MUNSU_TASK_ID", "captain:captain-one")
	t.Setenv("MUNSU_ROLE", "captain")
	t.Setenv("MUNSU_PARENT_STATUS", generalHome)

	root := NewRootCommand()
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"report", "--ring", "no-ring", "done", "domain complete"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	pending, err := mailbox.NewStore(captainHome).ListPending("captain-one")
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%d err=%v", len(pending), err)
	}
	if pending[0].ReceiverRank != mailbox.RankGeneral {
		t.Fatalf("receiver rank=%s", pending[0].ReceiverRank)
	}
	if env, _ := mailbox.NewStore(generalHome).ReadEnvelope("captain-one", pending[0].MessageID); env == nil {
		t.Fatal("General inbox should contain the Uplink Report")
	}
}
