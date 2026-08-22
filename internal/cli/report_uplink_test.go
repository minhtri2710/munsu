package cli

import (
	"bytes"
	"testing"

	"github.com/minhtri2710/munsu/internal/orchestrator"
)

func TestReportCmdMaterialSoldierUsesMailboxOnly(t *testing.T) {
	soldierHome := t.TempDir()
	captainHome := t.TempDir()
	// The receiving home must actually be a Captain home: the receiver rank is
	// derived from its durable provenance, not asserted by the sender.
	if err := orchestrator.WriteHomeIdentity(captainHome, "captain-one", orchestrator.RankCaptain); err != nil {
		t.Fatal(err)
	}
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

	pending, err := orchestrator.NewStore(captainHome).ListPending("task_one")
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%d err=%v", len(pending), err)
	}
	if pending[0].ReceiverRank != orchestrator.RankCaptain {
		t.Fatalf("receiver rank=%s", pending[0].ReceiverRank)
	}
	if env, _ := orchestrator.NewStore(captainHome).ReadEnvelope("task_one", pending[0].MessageID); env == nil {
		t.Fatal("Captain inbox should contain the Uplink Report")
	}
	if !orchestrator.HasAnyOpenReport(captainHome, "task:one") {
		t.Fatal("open evidence missing")
	}
}

func TestReportCmdMaterialCaptainUsesGeneralMailbox(t *testing.T) {
	captainHome := t.TempDir()
	generalHome := t.TempDir()
	if err := orchestrator.WriteHomeIdentity(captainHome, "captain-one", orchestrator.RankCaptain); err != nil {
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

	pending, err := orchestrator.NewStore(captainHome).ListPending("captain-one")
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%d err=%v", len(pending), err)
	}
	if pending[0].ReceiverRank != orchestrator.RankGeneral {
		t.Fatalf("receiver rank=%s", pending[0].ReceiverRank)
	}
	if env, _ := orchestrator.NewStore(generalHome).ReadEnvelope("captain-one", pending[0].MessageID); env == nil {
		t.Fatal("General inbox should contain the Uplink Report")
	}
}
