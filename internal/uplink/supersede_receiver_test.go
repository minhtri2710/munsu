package uplink

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/mailbox"
)

func TestSupersededRefCannotBeReceivedOrAcked(t *testing.T) {
	senderHome := t.TempDir()
	base := t.TempDir()
	receiverHome := filepath.Join(base, "captain-one")
	if err := os.MkdirAll(receiverHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := mailbox.WriteHomeIdentity(receiverHome, "captain-one", mailbox.RankCaptain); err != nil {
		t.Fatal(err)
	}
	first, err := Report(ReportRequest{SenderHome: senderHome, ReceiverHome: receiverHome, SenderRank: mailbox.RankSoldier, SenderIdentity: "soldier-one", ReceiverRank: mailbox.RankCaptain, ReceiverID: "captain-one", TaskID: "task:1", Key: "phase", State: "blocked", Message: "waiting"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Report(ReportRequest{SenderHome: senderHome, ReceiverHome: receiverHome, SenderRank: mailbox.RankSoldier, SenderIdentity: "soldier-one", ReceiverRank: mailbox.RankCaptain, ReceiverID: "captain-one", TaskID: "task:1", Key: "phase", State: "done", Message: "complete"})
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := mailbox.NewReceiver(receiverHome)
	if err != nil {
		t.Fatal(err)
	}
	oldRef := mailbox.NotificationRef{MessageID: first.MessageID, SenderIdentity: "soldier-one"}
	if _, err := receiver.Receive(oldRef); err == nil {
		t.Fatal("old ref receive should fail")
	}
	if _, err := receiver.Ack(oldRef); err == nil {
		t.Fatal("old ref ack should fail")
	}
	newRef := mailbox.NotificationRef{MessageID: second.MessageID, SenderIdentity: "soldier-one"}
	if _, err := receiver.Receive(newRef); err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.Ack(newRef); err != nil {
		t.Fatal(err)
	}
}
