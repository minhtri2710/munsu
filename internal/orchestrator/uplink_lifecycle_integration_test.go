//go:build lifecycle_integration

// Behavior-based contract tests for the Uplink/control-plane lifecycle
// (issue #546 EP-5). They prove, over the existing Report/Recover/Ack/retire
// surfaces in this package, the lifecycle
//
//	Intent -> Durable -> Acked -> Retired
//
// Notification and receiver observation are orthogonal to that lifecycle;
// the tests cover their delivery/replay behavior across the named paths:
//
//   - crash/replay: a durable report that is never acked survives a simulated
//     crash and is retired exactly once after the receiver acks
//   - duplicate delivery suppression: an acked report is never re-delivered,
//     and no duplicate notification survives a replay
//   - direct versus relay delivery: a soldier report lands directly in the
//     captain home and is retired there; a captain report is relayed to the
//     general home and retired from the captain sender
//   - ack: an exact-match ProcessingAck retires the sender's pending record
//     and closes the open evidence; retirement continuity then passes
//   - retirement: VerifyRetirementContinuity blocks while an uplink report
//     is open, and passes once it is acked and retired
//
// These tests add no second lifecycle authority, migration path, alternate
// protocol, or compatibility shim (ADR-0008). They reuse the existing public
// surfaces only.
package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ackedNotifier returns a Notify callback that acknowledges delivery into the
// parent pane without touching any ack state (notification ack != ProcessingAck).
func ackedNotifier() func(NotificationRef) UplinkNotifyResult {
	return func(NotificationRef) UplinkNotifyResult { return UplinkNotifyResult{Acknowledged: true} }
}

// failOnNotify returns a Notify callback that fails the test if it is invoked.
func failOnNotify(t *testing.T) func(NotificationRef) UplinkNotifyResult {
	return func(NotificationRef) UplinkNotifyResult {
		t.Fatal("unexpected re-notification of an already-retired report")
		return UplinkNotifyResult{}
	}
}

// directReport writes a durable soldier->captain uplink report into the captain
// home (both sender and receiver homes are the captain home, matching the CLI
// soldier branch in report_cmd.go). The report is durable but never delivered
// (Notify is nil), simulating the writer crash-before-ack window.
func directReport(t *testing.T, captainHome, taskID string) *ReportResult {
	t.Helper()
	res, err := Report(ReportRequest{
		SenderHome:     captainHome,
		SenderIdentity: "soldier-one",
		SenderRank:     RankSoldier,
		ReceiverHome:   captainHome,
		ReceiverID:     "captain-one",
		ReceiverRank:   RankCaptain,
		TaskID:         taskID,
		Key:            "default",
		State:          "done",
		Message:        "complete",
	})
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	return res
}

func TestUplinkLifecycle_CrashReplay_ReportReplayAckRetire(t *testing.T) {
	captainHome := filepath.Join(t.TempDir(), "captain-one")
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteHomeIdentity(captainHome, "captain-one", RankCaptain); err != nil {
		t.Fatal(err)
	}

	taskID := "task:crash"
	res := directReport(t, captainHome, taskID)

	// Durable: envelope + pending + open evidence all exist before any ack.
	if env, _ := NewStore(captainHome).ReadEnvelope("soldier-one", res.MessageID); env == nil {
		t.Fatal("envelope must be durable before ack")
	}
	if pending, _ := NewStore(captainHome).ReadPending("soldier-one", res.MessageID); pending == nil {
		t.Fatal("pending must be durable before ack")
	}
	if !hasEvidence(openEvidencePath(captainHome, taskID, "default")) {
		t.Fatal("open evidence must be durable before ack")
	}
	if VerifyRetirementContinuity(captainHome, taskID) == nil {
		t.Fatal("teardown must be blocked while the report is open")
	}

	// "Crash": the writer died after the durable write and before any delivery.

	// Replay: Recover re-drives delivery of the NotificationRef to the parent.
	replayed, err := Recover(RecoverRequest{
		SenderHome:     captainHome,
		ReceiverHome:   captainHome,
		ReceiverRank:   RankCaptain,
		SenderIdentity: "soldier-one",
		ForceNotify:    true,
		Notify:         ackedNotifier(),
	})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if replayed.Notified != 1 || replayed.Accepted != 0 {
		t.Fatalf("replay = %+v, want notified=1 accepted=0", replayed)
	}

	// Ack: the receiver validates and acknowledges the exact ref.
	recv, err := NewReceiver(captainHome)
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	ref := NotificationRef{MessageID: res.MessageID, SenderIdentity: "soldier-one"}
	env, err := recv.Receive(ref)
	if err != nil || env == nil {
		t.Fatalf("Receive: %v", err)
	}
	ack, err := recv.Ack(ref)
	if err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if ack.Outcome != OutcomeAccepted {
		t.Fatalf("ack outcome = %q", ack.Outcome)
	}

	// Retire: Recover sees the validated ack, removes pending and closes evidence.
	retired, err := Recover(RecoverRequest{
		SenderHome:     captainHome,
		ReceiverHome:   captainHome,
		ReceiverRank:   RankCaptain,
		SenderIdentity: "soldier-one",
		ForceNotify:    true,
		Notify:         failOnNotify(t),
	})
	if err != nil {
		t.Fatalf("Recover retire: %v", err)
	}
	if retired.Accepted != 1 || retired.Notified != 0 {
		t.Fatalf("retire = %+v, want accepted=1 notified=0", retired)
	}
	if pending, _ := NewStore(captainHome).ReadPending("soldier-one", res.MessageID); pending != nil {
		t.Fatal("pending must be removed after exact ack")
	}
	if hasEvidence(openEvidencePath(captainHome, taskID, "default")) {
		t.Fatal("open evidence must be closed after exact ack")
	}
	if !hasEvidence(acceptedEvidencePath(captainHome, taskID, "default")) {
		t.Fatal("accepted evidence must be written after exact ack")
	}
	if VerifyRetirementContinuity(captainHome, taskID) != nil {
		t.Fatal("teardown must be allowed once the report is acked and retired")
	}
}

func TestUplinkLifecycle_DuplicateDeliverySuppressedAfterRetire(t *testing.T) {
	captainHome := filepath.Join(t.TempDir(), "captain-one")
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteHomeIdentity(captainHome, "captain-one", RankCaptain); err != nil {
		t.Fatal(err)
	}

	taskID := "task:dup"
	res := directReport(t, captainHome, taskID)

	recv, err := NewReceiver(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	ref := NotificationRef{MessageID: res.MessageID, SenderIdentity: "soldier-one"}
	if _, err := recv.Ack(ref); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(RecoverRequest{
		SenderHome: captainHome, ReceiverHome: captainHome,
		ReceiverRank: RankCaptain, SenderIdentity: "soldier-one",
		ForceNotify: true, Notify: failOnNotify(t),
	}); err != nil {
		t.Fatal(err)
	}

	// Replay after retire must not re-deliver or re-notify anything.
	again, err := Recover(RecoverRequest{
		SenderHome: captainHome, ReceiverHome: captainHome,
		ReceiverRank: RankCaptain, SenderIdentity: "soldier-one",
		ForceNotify: true, Notify: failOnNotify(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.Accepted != 0 || again.Notified != 0 || again.Queued != 0 {
		t.Fatalf("post-retire replay = %+v, want all zero (no duplicate delivery)", again)
	}
}

func TestUplinkLifecycle_DirectVersusRelayDelivery(t *testing.T) {
	// Direct: soldier -> captain. Envelope, pending and open evidence all live
	// in the captain home; the captain retires it in place.
	captainHome := filepath.Join(t.TempDir(), "captain-one")
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteHomeIdentity(captainHome, "captain-one", RankCaptain); err != nil {
		t.Fatal(err)
	}
	directTask := "task:direct"
	direct := directReport(t, captainHome, directTask)
	if hasEvidence(acceptedEvidencePath(captainHome, directTask, "default")) {
		t.Fatal("direct report must start open, not accepted")
	}
	// (the direct retire of a soldier report is asserted in the first test)

	// Relay: captain -> general. The envelope lives in the general home; the
	// pending lives in the captain sender home; Recover delivers to the general
	// and retires from the captain home.
	generalHome := filepath.Join(t.TempDir(), "general-home")
	if err := os.MkdirAll(generalHome, 0755); err != nil {
		t.Fatal(err)
	}
	relayTask := "task:relay"
	relay, err := Report(ReportRequest{
		SenderHome:     captainHome,
		SenderIdentity: "captain-one",
		SenderRank:     RankCaptain,
		ReceiverHome:   generalHome,
		ReceiverID:     "general-home",
		ReceiverRank:   RankGeneral,
		TaskID:         relayTask,
		Key:            "default",
		State:          "done",
		Message:        "relay complete",
	})
	if err != nil {
		t.Fatalf("relay Report: %v", err)
	}
	// Envelope must be durable in the general (receiver) home.
	if env, _ := NewStore(generalHome).ReadEnvelope("captain-one", relay.MessageID); env == nil {
		t.Fatal("relayed envelope must live in the general home")
	}
	// Pending must remain in the captain (sender) home.
	if pending, _ := NewStore(captainHome).ReadPending("captain-one", relay.MessageID); pending == nil {
		t.Fatal("relayed pending must live in the captain sender home")
	}

	// Recover must notify the general before its exact ref is acknowledged.
	generalRecv, err := NewReceiver(generalHome)
	if err != nil {
		t.Fatal(err)
	}
	relayRef := NotificationRef{MessageID: relay.MessageID, SenderIdentity: "captain-one"}
	notifications := 0
	notified, err := Recover(RecoverRequest{
		SenderHome:     captainHome,
		ReceiverHome:   generalHome,
		ReceiverRank:   RankGeneral,
		SenderIdentity: "captain-one",
		ForceNotify:    true,
		Notify: func(ref NotificationRef) UplinkNotifyResult {
			notifications++
			if ref != relayRef {
				t.Fatalf("relay notification ref = %+v, want %+v", ref, relayRef)
			}
			return UplinkNotifyResult{Acknowledged: true}
		},
	})
	if err != nil {
		t.Fatalf("relay notification Recover: %v", err)
	}
	if notified.Notified != 1 || notified.Accepted != 0 || notifications != 1 {
		t.Fatalf("relay notification recovery = %+v, notifications=%d, want notified=1 accepted=0 notifications=1", notified, notifications)
	}
	if pending, _ := NewStore(captainHome).ReadPending("captain-one", relay.MessageID); pending == nil {
		t.Fatal("relay pending must remain until the general acknowledges it")
	}
	if _, err := generalRecv.Ack(relayRef); err != nil {
		t.Fatalf("general Ack: %v", err)
	}

	// Captain Recover retires the relayed report from its sender home.
	retired, err := Recover(RecoverRequest{
		SenderHome:     captainHome,
		ReceiverHome:   generalHome,
		ReceiverRank:   RankGeneral,
		SenderIdentity: "captain-one",
		ForceNotify:    true,
		Notify:         failOnNotify(t),
	})
	if err != nil {
		t.Fatalf("relay Recover: %v", err)
	}
	if retired.Accepted != 1 {
		t.Fatalf("relay retire = %+v, want accepted=1", retired)
	}
	if pending, _ := NewStore(captainHome).ReadPending("captain-one", relay.MessageID); pending != nil {
		t.Fatal("relayed pending must be retired from the captain home")
	}
	if VerifyRetirementContinuity(captainHome, relayTask) != nil {
		t.Fatal("captain sender teardown must be allowed after the relayed report retires")
	}
	_ = direct // keep the direct assertion above meaningful on its own
}

func TestUplinkLifecycle_NotificationThrottleSuppressesDuplicateDelivery(t *testing.T) {
	captainHome := filepath.Join(t.TempDir(), "captain-one")
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteHomeIdentity(captainHome, "captain-one", RankCaptain); err != nil {
		t.Fatal(err)
	}

	taskID := "task:throttle"
	_, err := Report(ReportRequest{
		SenderHome:     captainHome,
		SenderIdentity: "soldier-one",
		SenderRank:     RankSoldier,
		ReceiverHome:   captainHome,
		ReceiverID:     "captain-one",
		ReceiverRank:   RankCaptain,
		TaskID:         taskID,
		Key:            "default",
		State:          "done",
		Message:        "complete",
		Notify:         ackedNotifier(),
	})
	if err != nil {
		t.Fatalf("Report: %v", err)
	}

	notifications := 0
	notify := func(NotificationRef) UplinkNotifyResult {
		notifications++
		return UplinkNotifyResult{Acknowledged: true}
	}
	// Recover without ForceNotify: the fresh report is still inside its 60s
	// notification window, so it must not be re-notified.
	sameMinute, err := Recover(RecoverRequest{
		SenderHome:     captainHome,
		ReceiverHome:   captainHome,
		ReceiverRank:   RankCaptain,
		SenderIdentity: "soldier-one",
		Now:            time.Now(),
		Notify:         notify,
	})
	if err != nil {
		t.Fatal(err)
	}
	if notifications != 0 || sameMinute.Queued == 0 {
		t.Fatalf("throttle: notifications=%d recover=%+v, want no inline notify and queued", notifications, sameMinute)
	}

	// ForceNotify (the watcher/startup replay) still delivers exactly once.
	forced, err := Recover(RecoverRequest{
		SenderHome:     captainHome,
		ReceiverHome:   captainHome,
		ReceiverRank:   RankCaptain,
		SenderIdentity: "soldier-one",
		ForceNotify:    true,
		Now:            time.Now(),
		Notify:         notify,
	})
	if err != nil {
		t.Fatal(err)
	}
	if forced.Notified != 1 || notifications != 1 {
		t.Fatalf("force replay: recover=%+v notifications=%d, want one forced delivery", forced, notifications)
	}
}
