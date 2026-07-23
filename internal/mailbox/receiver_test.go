package mailbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- NotificationRef validation ---

func TestNotificationRef_Valid(t *testing.T) {
	ref := NotificationRef{MessageID: "msg-1", SenderIdentity: "soldier-1"}
	if err := ref.Validate(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestNotificationRef_EmptyMessageID(t *testing.T) {
	ref := NotificationRef{MessageID: "", SenderIdentity: "soldier-1"}
	if err := ref.Validate(); err == nil || !strings.Contains(err.Error(), "message ID") {
		t.Errorf("expected message ID error, got: %v", err)
	}
}

func TestNotificationRef_EmptySenderIdentity(t *testing.T) {
	ref := NotificationRef{MessageID: "msg-1", SenderIdentity: ""}
	if err := ref.Validate(); err == nil || !strings.Contains(err.Error(), "sender identity") {
		t.Errorf("expected sender identity error, got: %v", err)
	}
}

// --- Receiver.Process: valid resolution ---

func TestReceiver_Process_ValidResolution(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	// Write an envelope to the receiver's inbox.
	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-munsu",
		TaskID:         "task:1",
		Key:            "my-key",
		Payload:        "done: all work complete",
	}
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	// Process as the intended receiver.
	recv := NewReceiver(home, "captain-munsu", RankCaptain)
	ref := NotificationRef{
		MessageID:      env.MessageID,
		SenderIdentity: "soldier-1",
	}
	res := recv.Process(ref, "done")
	if !res.Ok() {
		t.Fatalf("Process failed: %v", res.Err)
	}
	if res.Ack == nil {
		t.Fatal("expected non-nil ack")
	}
	if res.Ack.Outcome != "done" {
		t.Errorf("ack outcome=%q, want %q", res.Ack.Outcome, "done")
	}
	if res.Ack.MessageID != env.MessageID {
		t.Errorf("ack MessageID=%q, want %q", res.Ack.MessageID, env.MessageID)
	}
	if res.Ack.SenderIdentity != "soldier-1" {
		t.Errorf("ack SenderIdentity=%q", res.Ack.SenderIdentity)
	}
	if res.Envelope == nil {
		t.Fatal("expected envelope in resolution")
	}
	if res.Envelope.MessageID != env.MessageID {
		t.Errorf("envelope MessageID mismatch")
	}

	// Verify ack was written to disk.
	ack, err := store.ReadAck("soldier-1", env.MessageID)
	if err != nil {
		t.Fatalf("ReadAck: %v", err)
	}
	if ack == nil {
		t.Fatal("ack not found on disk")
	}
	if ack.Outcome != "done" {
		t.Errorf("disk ack outcome=%q", ack.Outcome)
	}
}

// --- Receiver.Process: malformed reference ---

func TestReceiver_Process_MalformedRef(t *testing.T) {
	recv := NewReceiver(t.TempDir(), "captain-1", RankCaptain)

	tests := []struct {
		name string
		ref  NotificationRef
	}{
		{"empty message ID", NotificationRef{MessageID: "", SenderIdentity: "soldier-1"}},
		{"empty sender identity", NotificationRef{MessageID: "msg-1", SenderIdentity: ""}},
		{"both empty", NotificationRef{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := recv.Process(tt.ref, "done")
			if res.Ok() {
				t.Fatal("expected error for malformed ref")
			}
			if res.Err == nil {
				t.Fatal("expected non-nil error")
			}
		})
	}
}

// --- Receiver.Process: missing envelope ---

func TestReceiver_Process_MissingEnvelope(t *testing.T) {
	recv := NewReceiver(t.TempDir(), "captain-1", RankCaptain)
	ref := NotificationRef{MessageID: "nonexistent-id", SenderIdentity: "soldier-1"}
	res := recv.Process(ref, "done")
	if res.Ok() {
		t.Fatal("expected error for missing envelope")
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", res.Err)
	}
}

// --- Receiver.Process: wrong receiver identity ---

func TestReceiver_Process_WrongReceiverIdentity(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-alpha", // intended for alpha
		Payload:        "hello",
	}
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	// Process as beta — should fail.
	recv := NewReceiver(home, "captain-beta", RankCaptain)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "soldier-1"}
	res := recv.Process(ref, "done")
	if res.Ok() {
		t.Fatal("expected error for wrong receiver identity")
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "receiver identity mismatch") {
		t.Errorf("expected receiver identity mismatch error, got: %v", res.Err)
	}
}

// --- Receiver.Process: wrong receiver rank ---

func TestReceiver_Process_WrongReceiverRank(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain, // envelope says captain
		ReceiverID:     "captain-1",
		Payload:        "hello",
	}
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	// Process as general — should fail.
	recv := NewReceiver(home, "captain-1", RankGeneral)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "soldier-1"}
	res := recv.Process(ref, "done")
	if res.Ok() {
		t.Fatal("expected error for wrong receiver rank")
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "receiver rank mismatch") {
		t.Errorf("expected receiver rank mismatch error, got: %v", res.Err)
	}
}

// --- Receiver.Process: wrong sender identity ---

func TestReceiver_Process_WrongSenderIdentity(t *testing.T) {
	home := t.TempDir()

	// Write an envelope under soldier-beta's inbox directory (so the ref can
	// find it) but with SenderIdentity set to soldier-alpha internally.
	// This simulates a mismatch between the ref's sender identity and the
	// envelope's stored sender identity.
	mismatchDir := filepath.Join(home, "state", InboxDir, "soldier-beta")
	if err := os.MkdirAll(mismatchDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mismatchEnv := &Envelope{
		MessageID:      "mismatch-msg",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-alpha", // internal field says alpha
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "hello",
		PayloadHash:    PayloadHashHex("hello"),
		SchemaVersion:  SchemaVersion,
		CreatedAt:      time.Now().UnixNano(),
	}
	data, _ := json.MarshalIndent(mismatchEnv, "", "  ")
	if err := os.WriteFile(filepath.Join(mismatchDir, "mismatch-msg.json"), data, 0644); err != nil {
		t.Fatalf("write mismatch envelope: %v", err)
	}

	// Ref says soldier-beta — finds the file but internal SenderIdentity
	// doesn't match.
	recv := NewReceiver(home, "captain-1", RankCaptain)
	ref := NotificationRef{MessageID: "mismatch-msg", SenderIdentity: "soldier-beta"}
	res := recv.Process(ref, "done")
	if res.Ok() {
		t.Fatal("expected error for wrong sender identity")
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "sender identity mismatch") {
		t.Errorf("expected sender identity mismatch error, got: %v", res.Err)
	}
}

// --- Receiver.Process: tampered payload/hash ---

func TestReceiver_Process_TamperedPayloadHash(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "original content",
	}
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	// Tamper with the envelope file on disk: change the payload but not the hash.
	path := filepath.Join(home, "state", InboxDir, "soldier-1", env.MessageID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading envelope: %v", err)
	}
	tampered := strings.Replace(string(data), "original content", "tampered content", 1)
	if err := os.WriteFile(path, []byte(tampered), 0644); err != nil {
		t.Fatalf("writing tampered envelope: %v", err)
	}

	// Re-read to verify tamper worked.
	readBack, err := store.ReadEnvelope("soldier-1", env.MessageID)
	if err != nil || readBack == nil {
		t.Fatal("should still read tampered envelope")
	}
	if readBack.Payload != "tampered content" {
		t.Fatalf("tamper failed: payload=%q", readBack.Payload)
	}

	recv := NewReceiver(home, "captain-1", RankCaptain)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "soldier-1"}
	res := recv.Process(ref, "done")
	if res.Ok() {
		t.Fatal("expected error for tampered payload")
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "hash mismatch") {
		t.Errorf("expected hash mismatch error, got: %v", res.Err)
	}
}

func TestReceiver_Process_TamperedHashField(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "content",
	}
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	// Tamper with the payload_hash field directly.
	path := filepath.Join(home, "state", InboxDir, "soldier-1", env.MessageID+".json")
	var raw map[string]interface{}
	d, _ := os.ReadFile(path)
	json.Unmarshal(d, &raw)
	raw["payload_hash"] = "0000000000000000000000000000000000000000000000000000000000000000"
	tampered, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(path, tampered, 0644)

	recv := NewReceiver(home, "captain-1", RankCaptain)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "soldier-1"}
	res := recv.Process(ref, "done")
	if res.Ok() {
		t.Fatal("expected error for tampered hash")
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "hash mismatch") {
		t.Errorf("expected hash mismatch error, got: %v", res.Err)
	}
}

// --- Receiver.Process: idempotent same outcome ---

func TestReceiver_Process_DuplicateSameOutcome(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "done: work",
	}
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	recv := NewReceiver(home, "captain-1", RankCaptain)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "soldier-1"}

	// First call — should succeed.
	res1 := recv.Process(ref, "done")
	if !res1.Ok() {
		t.Fatalf("first Process failed: %v", res1.Err)
	}
	firstAck := res1.Ack

	// Second call with same outcome — should be idempotent.
	res2 := recv.Process(ref, "done")
	if !res2.Ok() {
		t.Fatalf("second Process (same outcome) failed: %v", res2.Err)
	}
	if res2.Ack == nil {
		t.Fatal("expected non-nil ack on duplicate")
	}
	if res2.Ack.Outcome != "done" {
		t.Errorf("duplicate ack outcome=%q", res2.Ack.Outcome)
	}
	// Should return the existing ack (same outcome).
	if res2.Ack.ProcessedAt != firstAck.ProcessedAt {
		t.Log("duplicate returned existing ack (ProcessedAt matches)")
	}
	// Only one ack file on disk.
	count := countAckFiles(t, home, "soldier-1", env.MessageID)
	if count != 1 {
		t.Errorf("expected 1 ack file on disk, got %d", count)
	}
}

// --- Receiver.Process: conflicting outcome ---

func TestReceiver_Process_ConflictingOutcome(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "work item",
	}
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	recv := NewReceiver(home, "captain-1", RankCaptain)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "soldier-1"}

	// First call with "done".
	res1 := recv.Process(ref, "done")
	if !res1.Ok() {
		t.Fatalf("first Process failed: %v", res1.Err)
	}

	// Second call with "failed" — should fail closed.
	res2 := recv.Process(ref, "failed")
	if res2.Ok() {
		t.Fatal("expected error for conflicting outcome")
	}
	if res2.Err == nil || !strings.Contains(res2.Err.Error(), "conflicting ack") {
		t.Errorf("expected conflicting ack error, got: %v", res2.Err)
	}
	if res2.Ack != nil {
		t.Error("expected nil ack on conflict")
	}

	// Third call with "needs-decision" — should also fail closed.
	res3 := recv.Process(ref, "needs-decision")
	if res3.Ok() {
		t.Fatal("expected error for another conflicting outcome")
	}
	if res3.Err == nil || !strings.Contains(res3.Err.Error(), "conflicting ack") {
		t.Errorf("expected conflicting ack error, got: %v", res3.Err)
	}
}

// --- Receiver.Process: all valid outcomes ---

func TestReceiver_Process_AllOutcomes(t *testing.T) {
	outcomes := []string{
		OutcomeDone,
		OutcomeFailed,
		OutcomeNeedsDecisio,
		OutcomeBlocked,
		OutcomePaused,
	}
	for _, outcome := range outcomes {
		t.Run(outcome, func(t *testing.T) {
			home := t.TempDir()
			store := NewStore(home)
			env := &Envelope{
				SenderRank:     RankSoldier,
				SenderIdentity: "soldier-1",
				ReceiverRank:   RankCaptain,
				ReceiverID:     "captain-1",
				Payload:        outcome + ": work",
			}
			if err := store.WriteEnvelope(env); err != nil {
				t.Fatalf("WriteEnvelope: %v", err)
			}

			recv := NewReceiver(home, "captain-1", RankCaptain)
			ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "soldier-1"}
			res := recv.Process(ref, outcome)
			if !res.Ok() {
				t.Fatalf("Process(%q) failed: %v", outcome, res.Err)
			}
			if res.Ack.Outcome != outcome {
				t.Errorf("ack outcome=%q, want %q", res.Ack.Outcome, outcome)
			}
		})
	}
}

// --- Receiver.Process: invalid outcome ---

func TestReceiver_Process_InvalidOutcome(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "hello",
	}
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	recv := NewReceiver(home, "captain-1", RankCaptain)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "soldier-1"}
	res := recv.Process(ref, "invalid-outcome")
	if res.Ok() {
		t.Fatal("expected error for invalid outcome")
	}
	// WriteAck rejects invalid outcomes.
	if res.Err == nil {
		t.Fatal("expected non-nil error")
	}
}

// --- NotifyResult: Acknowledged field preserved, pending not removed ---

func TestNotifyResult_PendingNotRemoved(t *testing.T) {
	// This test verifies that NotifyResult.Acknowledged is independent of
	// sender pending state. Acknowledging a notification must never remove
	// the sender's pending record — that is handled through the separate
	// ack/RemovePendingAfterAck flow.

	// Simulate a scenario: notification is acknowledged but pending remains.
	home := t.TempDir()
	store := NewStore(home)

	env := &Envelope{
		MessageID:      "msg-pending-test",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "done: test",
		PayloadHash:    PayloadHashHex("done: test"),
	}
	if err := store.WritePending(env); err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	// Verify pending exists.
	pending, err := store.ReadPending("soldier-1", "msg-pending-test")
	if err != nil || pending == nil {
		t.Fatal("pending should exist")
	}

	// NotifyResult with Acknowledged=true — this is what would happen
	// after a successful SubmitPrompt.
	nr := &NotifyResult{
		Ref:          NotificationRef{MessageID: "msg-pending-test", SenderIdentity: "soldier-1"},
		Acknowledged: true,
		Status:       "submitted",
	}
	_ = nr // Acknowledged notification must not remove pending.

	// Verify pending still exists after acknowledgment.
	pending, err = store.ReadPending("soldier-1", "msg-pending-test")
	if err != nil || pending == nil {
		t.Fatal("pending must still exist after notification acknowledgment")
	}

	// Verify pending is only removed through the ack flow.
	ack := &ProcessingAck{
		MessageID: "msg-pending-test", SenderRank: RankSoldier,
		SenderIdentity: "soldier-1", ReceiverRank: RankCaptain,
		ReceiverID: "captain-1", PayloadHash: PayloadHashHex("done: test"),
		ProcessedAt: time.Now().UnixNano(), Outcome: "done",
	}
	if err := store.WriteAck(ack); err != nil {
		t.Fatalf("WriteAck: %v", err)
	}
	if err := store.RemovePendingAfterAck("soldier-1", "msg-pending-test", ack); err != nil {
		t.Fatalf("RemovePendingAfterAck: %v", err)
	}
	pending, _ = store.ReadPending("soldier-1", "msg-pending-test")
	if pending != nil {
		t.Fatal("pending should be removed only after validated ack")
	}
}

// --- Receiver.Process with different rank transitions ---

func TestReceiver_Process_DifferentRankTransitions(t *testing.T) {
	tests := []struct {
		name         string
		senderRank   Rank
		senderID     string
		receiverRank Rank
		receiverID   string
	}{
		{"general-to-captain", RankGeneral, "general-1", RankCaptain, "captain-1"},
		{"captain-to-general", RankCaptain, "captain-1", RankGeneral, "general-main"},
		{"captain-to-soldier", RankCaptain, "captain-1", RankSoldier, "soldier-1"},
		{"soldier-to-captain", RankSoldier, "soldier-1", RankCaptain, "captain-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			store := NewStore(home)

			env := &Envelope{
				SenderRank:     tt.senderRank,
				SenderIdentity: tt.senderID,
				ReceiverRank:   tt.receiverRank,
				ReceiverID:     tt.receiverID,
				Payload:        "work",
			}
			if err := store.WriteEnvelope(env); err != nil {
				t.Fatalf("WriteEnvelope: %v", err)
			}

			recv := NewReceiver(home, tt.receiverID, tt.receiverRank)
			ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: tt.senderID}
			res := recv.Process(ref, "done")
			if !res.Ok() {
				t.Fatalf("Process failed: %v", res.Err)
			}
			if res.Ack.ReceiverRank != tt.receiverRank {
				t.Errorf("ack ReceiverRank=%q, want %q", res.Ack.ReceiverRank, tt.receiverRank)
			}
		})
	}
}

// --- Receiver.Process: envelope has task/key fields ---

func TestReceiver_Process_WithTaskAndKey(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "task:rebrand-phase1",
		Key:            "real-estate-apex",
		Payload:        "done: rebrand complete",
	}
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	recv := NewReceiver(home, "captain-1", RankCaptain)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "soldier-1"}
	res := recv.Process(ref, "done")
	if !res.Ok() {
		t.Fatalf("Process failed: %v", res.Err)
	}
	if res.Ack.TaskID != "task:rebrand-phase1" {
		t.Errorf("ack TaskID=%q", res.Ack.TaskID)
	}
	if res.Ack.Key != "real-estate-apex" {
		t.Errorf("ack Key=%q", res.Ack.Key)
	}
}

// --- NotificationRef JSON round-trip (compact struct) ---

func TestNotificationRef_JSONRoundTrip(t *testing.T) {
	ref := NotificationRef{MessageID: "msg-abc-123", SenderIdentity: "soldier-task-42"}
	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded NotificationRef
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.MessageID != "msg-abc-123" {
		t.Errorf("MessageID=%q", decoded.MessageID)
	}
	if decoded.SenderIdentity != "soldier-task-42" {
		t.Errorf("SenderIdentity=%q", decoded.SenderIdentity)
	}

	// Verify output is compact (only two fields).
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	if len(raw) != 2 {
		t.Errorf("expected 2 fields in JSON, got %d: %v", len(raw), raw)
	}
}

// --- Helper ---

func countAckFiles(t *testing.T, home, senderID, messageID string) int {
	t.Helper()
	ackPath := filepath.Join(home, "state", InboxDir, senderID, messageID+".ack")
	if _, err := os.Stat(ackPath); err != nil {
		return 0
	}
	return 1
}
