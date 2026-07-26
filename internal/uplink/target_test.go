package uplink

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/mailbox"
)

func TestResolveReceiverTargetCaptainUsesParentMetaNotTaskID(t *testing.T) {
	captainHome, generalHome := t.TempDir(), t.TempDir()
	if err := config.Set(captainHome, "parent-home", generalHome); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(generalHome, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generalHome, "state", "captain:captain-one.meta"), []byte("herdr_pane_id=p9\nherdr_session=fleet\n"), 0644); err != nil {
		t.Fatal(err)
	}
	env := &mailbox.Envelope{Kind: "uplink-report", SenderRank: mailbox.RankSoldier, SenderIdentity: "task_special", ReceiverRank: mailbox.RankCaptain, ReceiverID: "captain-one", TaskID: "task:with/slash", Payload: "done"}
	if err := mailbox.NewStore(captainHome).WriteEnvelope(env); err != nil {
		t.Fatal(err)
	}
	ref := mailbox.NotificationRef{MessageID: env.MessageID, SenderIdentity: env.SenderIdentity}
	target, err := resolveReceiverTarget(captainHome, ref)
	if err != nil {
		t.Fatal(err)
	}
	if target.Handle != "fleet:p9" {
		t.Fatalf("handle=%q", target.Handle)
	}
}

func TestResolveReceiverTargetCaptainFailsClosedWithoutMeta(t *testing.T) {
	captainHome := t.TempDir()
	env := &mailbox.Envelope{Kind: "uplink-report", SenderRank: mailbox.RankSoldier, SenderIdentity: "soldier", ReceiverRank: mailbox.RankCaptain, ReceiverID: "captain-one", TaskID: "task:1", Payload: "done"}
	if err := mailbox.NewStore(captainHome).WriteEnvelope(env); err != nil {
		t.Fatal(err)
	}
	_, err := resolveReceiverTarget(captainHome, mailbox.NotificationRef{MessageID: env.MessageID, SenderIdentity: env.SenderIdentity})
	if err == nil {
		t.Fatal("missing authoritative captain meta must fail closed")
	}
}
