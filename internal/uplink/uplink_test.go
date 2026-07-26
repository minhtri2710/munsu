package uplink

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/mailbox"
)

func TestReportPersistsBeforeNotificationAndQueuesFailure(t *testing.T) {
	senderHome := t.TempDir()
	receiverHome := t.TempDir()
	var observedDurable bool

	result, err := Report(ReportRequest{
		SenderHome:     senderHome,
		ReceiverHome:   receiverHome,
		SenderRank:     mailbox.RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   mailbox.RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "task:1",
		Key:            "default",
		State:          "done",
		Message:        "complete",
		Notify: func(ref mailbox.NotificationRef) NotifyResult {
			pending, _ := mailbox.NewStore(senderHome).ReadPending("soldier-1", ref.MessageID)
			envelope, _ := mailbox.NewStore(receiverHome).ReadEnvelope("soldier-1", ref.MessageID)
			observedDurable = pending != nil && envelope != nil
			return NotifyResult{Queued: true}
		},
	})
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if !observedDurable {
		t.Fatal("envelope and pending must exist before notification")
	}
	if !result.Queued || result.MessageID == "" {
		t.Fatalf("result = %+v, want durable queued report", result)
	}
	if !HasOpenReport(senderHome, "task:1", "default") {
		t.Fatal("report should remain open before Processing Ack")
	}
}

func TestReportLatestSupersedesSameTaskAndKey(t *testing.T) {
	senderHome := t.TempDir()
	receiverHome := t.TempDir()

	first, err := Report(ReportRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderRank: mailbox.RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: mailbox.RankCaptain, ReceiverID: "captain-1",
		TaskID: "task:1", Key: "phase", State: "blocked", Message: "waiting",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Report(ReportRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderRank: mailbox.RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: mailbox.RankCaptain, ReceiverID: "captain-1",
		TaskID: "task:1", Key: "phase", State: "done", Message: "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.MessageID == second.MessageID {
		t.Fatal("superseding report should have a new immutable message ID")
	}
	pending, err := mailbox.NewStore(senderHome).ListPending("soldier-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].MessageID != second.MessageID {
		t.Fatalf("pending = %+v, want only latest report", pending)
	}
	if env, _ := mailbox.NewStore(receiverHome).ReadEnvelope("soldier-1", first.MessageID); env == nil {
		t.Fatal("superseded receiver envelope should remain immutable history")
	}
	if !mailbox.NewStore(receiverHome).IsSuperseded("soldier-1", first.MessageID) {
		t.Fatal("superseded marker missing")
	}
}

func TestReportReplacementFailurePreservesOldPending(t *testing.T) {
	senderHome := t.TempDir()
	receiverHome := t.TempDir()
	first, err := Report(ReportRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderRank: mailbox.RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: mailbox.RankCaptain, ReceiverID: "captain-1",
		TaskID: "task:1", Key: "phase", State: "blocked", Message: "waiting",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(receiverHome, "state")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(receiverHome, "state"), []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = Report(ReportRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderRank: mailbox.RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: mailbox.RankCaptain, ReceiverID: "captain-1",
		TaskID: "task:1", Key: "phase", State: "done", Message: "complete",
	})
	if err == nil {
		t.Fatal("replacement should fail")
	}
	pending, readErr := mailbox.NewStore(senderHome).ReadPending("soldier-1", first.MessageID)
	if readErr != nil || pending == nil {
		t.Fatalf("old pending must survive: %v", readErr)
	}
}

func TestRecoverUsesNotificationRefAndClosesAfterExactAck(t *testing.T) {
	senderHome := t.TempDir()
	receiverHome := t.TempDir()
	result, err := Report(ReportRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderRank: mailbox.RankCaptain, SenderIdentity: "captain-1",
		ReceiverRank: mailbox.RankGeneral, ReceiverID: "general-1",
		TaskID: "captain:1", Key: "default", State: "failed", Message: "failed",
	})
	if err != nil {
		t.Fatal(err)
	}

	env, err := mailbox.NewStore(receiverHome).ReadEnvelope("captain-1", result.MessageID)
	if err != nil || env == nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	ack := &mailbox.ProcessingAck{
		MessageID: env.MessageID, SenderRank: env.SenderRank,
		SenderIdentity: env.SenderIdentity, ReceiverRank: env.ReceiverRank,
		ReceiverID: env.ReceiverID, TaskID: env.TaskID, Key: env.Key,
		PayloadHash: env.PayloadHash, ProcessedAt: time.Now().UnixNano(),
		Outcome: mailbox.OutcomeAccepted,
	}
	if err := mailbox.NewStore(receiverHome).WriteAck(ack); err != nil {
		t.Fatal(err)
	}

	var notified string
	recovered, err := Recover(RecoverRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderIdentity: "captain-1", ForceNotify: true,
		Notify: func(ref mailbox.NotificationRef) NotifyResult {
			notified = ref.Encode()
			return NotifyResult{Acknowledged: true}
		},
	})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if recovered.Accepted != 1 {
		t.Fatalf("accepted = %d, want 1", recovered.Accepted)
	}
	if notified != "" {
		t.Fatalf("acked report must not notify again: %s", notified)
	}
	if HasOpenReport(senderHome, "captain:1", "default") {
		t.Fatal("exact ack should close local report evidence")
	}
	if !HasAcceptedReport(senderHome, "captain:1", "default") {
		t.Fatal("accepted evidence should be durable")
	}
	pending, _ := mailbox.NewStore(senderHome).ReadPending("captain-1", result.MessageID)
	if pending != nil {
		t.Fatal("accepted pending should be removed")
	}
}

func TestRecoverRetriesSameRefAfterSixtySeconds(t *testing.T) {
	senderHome := t.TempDir()
	receiverHome := t.TempDir()
	result, err := Report(ReportRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderRank: mailbox.RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: mailbox.RankCaptain, ReceiverID: "captain-1",
		TaskID: "task:1", Key: "default", State: "done", Message: "complete",
		Notify: func(mailbox.NotificationRef) NotifyResult { return NotifyResult{Acknowledged: true} },
	})
	if err != nil {
		t.Fatal(err)
	}

	attemptPath := notificationAttemptPath(senderHome, result.MessageID)
	old := time.Now().Add(-61 * time.Second)
	if err := os.Chtimes(attemptPath, old, old); err != nil {
		t.Fatal(err)
	}

	var got mailbox.NotificationRef
	recovered, err := Recover(RecoverRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderIdentity: "soldier-1", Now: time.Now(),
		Notify: func(ref mailbox.NotificationRef) NotifyResult { got = ref; return NotifyResult{Acknowledged: true} },
	})
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Notified != 1 || got.MessageID != result.MessageID {
		t.Fatalf("recover = %+v ref=%+v", recovered, got)
	}
	if _, err := os.Stat(filepath.Join(receiverHome, "state", mailbox.InboxDir, "soldier-1", result.MessageID+".json")); err != nil {
		t.Fatal("recovery must retain the original envelope")
	}
}
