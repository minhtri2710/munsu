package orchestrator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestSupersededRefCannotBeReceivedOrAcked(t *testing.T) {
	senderHome := t.TempDir()
	base := t.TempDir()
	receiverHome := filepath.Join(base, "captain-one")
	if err := os.MkdirAll(receiverHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := home.SeedCaptainProvenance(receiverHome, "captain-one"); err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(receiverHome, "task:1", map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}
	soldierIdentity := home.ReceiverIDForTask("task:1")
	first, err := Report(ReportRequest{SenderHome: senderHome, ReceiverHome: receiverHome, SenderRank: home.RankSoldier, SenderIdentity: soldierIdentity, ReceiverRank: home.RankCaptain, ReceiverID: "captain-one", TaskID: "task:1", Key: "phase", State: "blocked", Message: "waiting"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Report(ReportRequest{SenderHome: senderHome, ReceiverHome: receiverHome, SenderRank: home.RankSoldier, SenderIdentity: soldierIdentity, ReceiverRank: home.RankCaptain, ReceiverID: "captain-one", TaskID: "task:1", Key: "phase", State: "done", Message: "complete"})
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := home.NewReceiver(receiverHome)
	if err != nil {
		t.Fatal(err)
	}
	oldRef := home.NotificationRef{MessageID: first.MessageID, SenderIdentity: soldierIdentity}
	if _, err := receiver.Receive(oldRef); err == nil {
		t.Fatal("old ref receive should fail")
	}
	if _, err := receiver.Ack(oldRef); err == nil {
		t.Fatal("old ref ack should fail")
	}
	newRef := home.NotificationRef{MessageID: second.MessageID, SenderIdentity: soldierIdentity}
	if _, err := receiver.Receive(newRef); err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.Ack(newRef); err != nil {
		t.Fatal(err)
	}
}
