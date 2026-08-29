package orchestrator

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

func TestResolveReceiverTargetCaptainUsesParentMetaNotTaskID(t *testing.T) {
	captainHome, generalHome := t.TempDir(), t.TempDir()
	if err := config.Set(captainHome, "parent-home", generalHome); err != nil {
		t.Fatal(err)
	}
	// Seed through the production writer: the persisted filename is the
	// platform durable key for "captain:captain-one", never the logical id.
	if err := mhome.WriteMeta(generalHome, "captain:captain-one", map[string]string{
		"herdr_pane_id": "p9",
		"herdr_session": "fleet",
	}); err != nil {
		t.Fatal(err)
	}
	env := &Envelope{Kind: "uplink-report", SenderRank: RankSoldier, SenderIdentity: "task_special", ReceiverRank: RankCaptain, ReceiverID: "captain-one", TaskID: "task:with/slash", Payload: "done"}
	if err := NewStore(captainHome).WriteEnvelope(env); err != nil {
		t.Fatal(err)
	}
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: env.SenderIdentity}
	target, err := resolveReceiverTarget(captainHome, false, ref)
	if err != nil {
		t.Fatal(err)
	}
	if target.Handle != "fleet:p9" {
		t.Fatalf("handle=%q", target.Handle)
	}
}

func TestResolveReceiverTargetCaptainFailsClosedWithoutMeta(t *testing.T) {
	captainHome := t.TempDir()
	env := &Envelope{Kind: "uplink-report", SenderRank: RankSoldier, SenderIdentity: "soldier", ReceiverRank: RankCaptain, ReceiverID: "captain-one", TaskID: "task:1", Payload: "done"}
	if err := NewStore(captainHome).WriteEnvelope(env); err != nil {
		t.Fatal(err)
	}
	_, err := resolveReceiverTarget(captainHome, false, NotificationRef{MessageID: env.MessageID, SenderIdentity: env.SenderIdentity})
	if err == nil {
		t.Fatal("missing authoritative captain meta must fail closed")
	}
}
