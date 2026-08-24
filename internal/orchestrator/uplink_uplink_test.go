package orchestrator

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// captainReceiverHome builds a receiving home whose durable provenance really
// is a Captain's. The uplink derives the receiver rank from the home, so a bare
// temp dir is a General home no matter what rank the sender names.
func captainReceiverHome(t *testing.T, identity string) string {
	t.Helper()
	dir := t.TempDir()
	if err := WriteHomeIdentity(dir, identity, RankCaptain); err != nil {
		t.Fatal(err)
	}
	return dir
}

// hasEvidence reports whether a keyed uplink evidence file exists. The binary
// only ever asks the task-wide question (HasAnyOpenReport, HasPendingReport),
// so the per-key lookup belongs to the tests that assert the keyed lifecycle.
func hasEvidence(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestReportPersistsBeforeNotificationAndQueuesFailure(t *testing.T) {
	senderHome := t.TempDir()
	receiverHome := captainReceiverHome(t, "captain-1")
	var observedDurable bool

	result, err := Report(ReportRequest{
		SenderHome:     senderHome,
		ReceiverHome:   receiverHome,
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "task:1",
		Key:            "default",
		State:          "done",
		Message:        "complete",
		Notify: func(ref NotificationRef) UplinkNotifyResult {
			pending, _ := NewStore(senderHome).ReadPending("soldier-1", ref.MessageID)
			envelope, _ := NewStore(receiverHome).ReadEnvelope("soldier-1", ref.MessageID)
			observedDurable = pending != nil && envelope != nil
			return QueuedNotification()
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
	if !hasEvidence(openEvidencePath(senderHome, "task:1", "default")) {
		t.Fatal("report should remain open before Processing Ack")
	}
}

func TestReportLatestSupersedesSameTaskAndKey(t *testing.T) {
	senderHome := t.TempDir()
	receiverHome := captainReceiverHome(t, "captain-1")

	first, err := Report(ReportRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderRank: RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: RankCaptain, ReceiverID: "captain-1",
		TaskID: "task:1", Key: "phase", State: "blocked", Message: "waiting",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Report(ReportRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderRank: RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: RankCaptain, ReceiverID: "captain-1",
		TaskID: "task:1", Key: "phase", State: "done", Message: "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.MessageID == second.MessageID {
		t.Fatal("superseding report should have a new immutable message ID")
	}
	pending, err := NewStore(senderHome).ListPending("soldier-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].MessageID != second.MessageID {
		t.Fatalf("pending = %+v, want only latest report", pending)
	}
	if env, _ := NewStore(receiverHome).ReadEnvelope("soldier-1", first.MessageID); env == nil {
		t.Fatal("superseded receiver envelope should remain immutable history")
	}
	if !NewStore(receiverHome).IsSuperseded("soldier-1", first.MessageID) {
		t.Fatal("superseded marker missing")
	}
}

func TestReportReplacementFailurePreservesOldPending(t *testing.T) {
	senderHome := t.TempDir()
	receiverHome := captainReceiverHome(t, "captain-1")
	first, err := Report(ReportRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderRank: RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: RankCaptain, ReceiverID: "captain-1",
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
		SenderRank: RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: RankCaptain, ReceiverID: "captain-1",
		TaskID: "task:1", Key: "phase", State: "done", Message: "complete",
	})
	if err == nil {
		t.Fatal("replacement should fail")
	}
	pending, readErr := NewStore(senderHome).ReadPending("soldier-1", first.MessageID)
	if readErr != nil || pending == nil {
		t.Fatalf("old pending must survive: %v", readErr)
	}
}

func TestRecoverUsesNotificationRefAndClosesAfterExactAck(t *testing.T) {
	senderHome := t.TempDir()
	receiverHome := t.TempDir()
	result, err := Report(ReportRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderRank: RankCaptain, SenderIdentity: "captain-1",
		ReceiverRank: RankGeneral, ReceiverID: "general-1",
		TaskID: "captain:1", Key: "default", State: "failed", Message: "failed",
	})
	if err != nil {
		t.Fatal(err)
	}

	env, err := NewStore(receiverHome).ReadEnvelope("captain-1", result.MessageID)
	if err != nil || env == nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	ack := &ProcessingAck{
		MessageID: env.MessageID, SenderRank: env.SenderRank,
		SenderIdentity: env.SenderIdentity, ReceiverRank: env.ReceiverRank,
		ReceiverID: env.ReceiverID, TaskID: env.TaskID, Key: env.Key,
		PayloadHash: env.PayloadHash, ProcessedAt: time.Now().UnixNano(),
		Outcome: OutcomeAccepted,
	}
	if err := NewStore(receiverHome).WriteAck(ack); err != nil {
		t.Fatal(err)
	}

	var notified string
	recovered, err := Recover(RecoverRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderIdentity: "captain-1", ForceNotify: true,
		Notify: func(ref NotificationRef) UplinkNotifyResult {
			notified = ref.Encode()
			return AcknowledgedNotification()
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
	if hasEvidence(openEvidencePath(senderHome, "captain:1", "default")) {
		t.Fatal("exact ack should close local report evidence")
	}
	if !hasEvidence(acceptedEvidencePath(senderHome, "captain:1", "default")) {
		t.Fatal("accepted evidence should be durable")
	}
	pending, _ := NewStore(senderHome).ReadPending("captain-1", result.MessageID)
	if pending != nil {
		t.Fatal("accepted pending should be removed")
	}
}

func TestRecoverRetriesSameRefAfterSixtySeconds(t *testing.T) {
	senderHome := t.TempDir()
	receiverHome := captainReceiverHome(t, "captain-1")
	result, err := Report(ReportRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderRank: RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: RankCaptain, ReceiverID: "captain-1",
		TaskID: "task:1", Key: "default", State: "done", Message: "complete",
		Notify: func(NotificationRef) UplinkNotifyResult { return AcknowledgedNotification() },
	})
	if err != nil {
		t.Fatal(err)
	}

	attemptPath := notificationAttemptPath(senderHome, result.MessageID)
	old := time.Now().Add(-61 * time.Second)
	if err := os.Chtimes(attemptPath, old, old); err != nil {
		t.Fatal(err)
	}

	var got NotificationRef
	recovered, err := Recover(RecoverRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderIdentity: "soldier-1", Now: time.Now(),
		Notify: func(ref NotificationRef) UplinkNotifyResult {
			got = ref
			return AcknowledgedNotification()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Notified != 1 || got.MessageID != result.MessageID {
		t.Fatalf("recover = %+v ref=%+v", recovered, got)
	}
	if _, err := os.Stat(filepath.Join(receiverHome, "state", InboxDir, "soldier-1", result.MessageID+".json")); err != nil {
		t.Fatal("recovery must retain the original envelope")
	}
}

func TestRecoverTransportFailureIsLoudAndRetriable(t *testing.T) {
	senderHome := t.TempDir()
	receiverHome := captainReceiverHome(t, "captain-1")
	result, err := Report(ReportRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderRank: RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: RankCaptain, ReceiverID: "captain-1",
		TaskID: "task:transport", Key: "default", State: "failed", Message: "failed",
		Notify: func(NotificationRef) UplinkNotifyResult { return QueuedNotification() },
	})
	if err != nil {
		t.Fatal(err)
	}

	sentinel := errors.New("transport unavailable")
	calls := 0
	_, err = Recover(RecoverRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderIdentity: "soldier-1", ForceNotify: true,
		Notify: func(NotificationRef) UplinkNotifyResult {
			calls++
			return FailedNotification(sentinel)
		},
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Recover error = %v, want transport failure", err)
	}
	pending, readErr := NewStore(senderHome).ReadPending("soldier-1", result.MessageID)
	if readErr != nil || pending == nil {
		t.Fatalf("pending report = %v, want durable retry record", readErr)
	}
	if calls != 1 {
		t.Fatalf("notification calls = %d, want 1", calls)
	}

	_, err = Recover(RecoverRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderIdentity: "soldier-1", ForceNotify: true,
		Notify: func(NotificationRef) UplinkNotifyResult {
			calls++
			return AcknowledgedNotification()
		},
	})
	if err != nil {
		t.Fatalf("retry Recover: %v", err)
	}
	if calls != 2 {
		t.Fatalf("notification calls after retry = %d, want 2", calls)
	}
}

func TestUplinkNotifyResult_ClassifiedOutcomesAgreement(t *testing.T) {
	// 1. Acknowledged outcome: Report sets Notified=true, Queued=false; Recover counts Notified++.
	senderHome := t.TempDir()
	receiverHome := captainReceiverHome(t, "captain-1")
	repRes, err := Report(ReportRequest{
		SenderHome:     senderHome,
		ReceiverHome:   receiverHome,
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "task:ack",
		Key:            "default",
		State:          "done",
		Message:        "complete",
		Notify: func(NotificationRef) UplinkNotifyResult {
			return AcknowledgedNotification()
		},
	})
	if err != nil {
		t.Fatalf("Report with acknowledged outcome: %v", err)
	}
	if !repRes.Notified || repRes.Queued {
		t.Fatalf("ReportResult for acknowledged = %+v, want Notified=true, Queued=false", *repRes)
	}

	recRes, err := Recover(RecoverRequest{
		SenderHome:     senderHome,
		ReceiverHome:   receiverHome,
		SenderIdentity: "soldier-1",
		ForceNotify:    true,
		Notify: func(NotificationRef) UplinkNotifyResult {
			return AcknowledgedNotification()
		},
	})
	if err != nil {
		t.Fatalf("Recover with acknowledged outcome: %v", err)
	}
	if recRes.Notified != 1 || recRes.Queued != 0 {
		t.Fatalf("RecoverResult for acknowledged = %+v, want Notified=1, Queued=0", *recRes)
	}

	// 2. Queued outcome: Report sets Notified=false, Queued=true; Recover counts Queued++.
	repQueued, err := Report(ReportRequest{
		SenderHome:     senderHome,
		ReceiverHome:   receiverHome,
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "task:queued",
		Key:            "default",
		State:          "done",
		Message:        "complete",
		Notify: func(NotificationRef) UplinkNotifyResult {
			return QueuedNotification()
		},
	})
	if err != nil {
		t.Fatalf("Report with queued outcome: %v", err)
	}
	if repQueued.Notified || !repQueued.Queued {
		t.Fatalf("ReportResult for queued = %+v, want Notified=false, Queued=true", *repQueued)
	}

	recQueued, err := Recover(RecoverRequest{
		SenderHome:     senderHome,
		ReceiverHome:   receiverHome,
		SenderIdentity: "soldier-1",
		ForceNotify:    true,
		Notify: func(NotificationRef) UplinkNotifyResult {
			return QueuedNotification()
		},
	})
	if err != nil {
		t.Fatalf("Recover with queued outcome: %v", err)
	}
	// Note: 2 pending envelopes now in senderHome (task:ack and task:queued).
	// With notify returning queued, both increment Queued count.
	if recQueued.Queued != 2 || recQueued.Notified != 0 {
		t.Fatalf("RecoverResult for queued = %+v, want Queued=2, Notified=0", *recQueued)
	}
}
