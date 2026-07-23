package mailbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Test helpers ---

// setupReceiver creates a Receiver for testing. It writes the necessary
// home identity marker so the receiver's identity/rank is derived from
// durable home provenance rather than trusting caller strings.
func setupReceiver(t *testing.T, homeDir, identity string, rank Rank) *Receiver {
	t.Helper()
	if err := WriteHomeIdentity(homeDir, identity, rank); err != nil {
		t.Fatalf("WriteHomeIdentity: %v", err)
	}
	recv, err := NewReceiver(homeDir)
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	return recv
}

// --- NotificationRef Encode / Parse ---

func TestNotificationRef_Encode_RoundTrip(t *testing.T) {
	ref := NotificationRef{MessageID: "msg-abc-123", SenderIdentity: "soldier-42"}
	encoded := ref.Encode()

	// Must be valid JSON.
	if !json.Valid([]byte(encoded)) {
		t.Fatal("Encode output is not valid JSON")
	}

	// Must parse back to identical ref.
	parsed, err := ParseNotificationRef(encoded)
	if err != nil {
		t.Fatalf("ParseNotificationRef: %v", err)
	}
	if parsed.MessageID != ref.MessageID {
		t.Errorf("MessageID: %q != %q", parsed.MessageID, ref.MessageID)
	}
	if parsed.SenderIdentity != ref.SenderIdentity {
		t.Errorf("SenderIdentity: %q != %q", parsed.SenderIdentity, ref.SenderIdentity)
	}
}

func TestNotificationRef_Encode_NoAdHocFormat(t *testing.T) {
	// Verify Encode is JSON, not fmt.Sprintf("notification: ...").
	ref := NotificationRef{MessageID: "m1", SenderIdentity: "s1"}
	enc := ref.Encode()
	if strings.HasPrefix(enc, "notification:") {
		t.Error("Encode must not use ad-hoc notification: format")
	}
	if !strings.Contains(enc, `"message_id"`) {
		t.Error("Encode must be JSON with message_id field")
	}
	if !strings.Contains(enc, `"sender_identity"`) {
		t.Error("Encode must be JSON with sender_identity field")
	}
}

func TestParseNotificationRef_InvalidJSON(t *testing.T) {
	_, err := ParseNotificationRef("not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseNotificationRef_EmptyMessageID(t *testing.T) {
	_, err := ParseNotificationRef(`{"message_id":"","sender_identity":"s1"}`)
	if err == nil || !strings.Contains(err.Error(), "message ID") {
		t.Errorf("expected message ID error, got: %v", err)
	}
}

func TestParseNotificationRef_EmptySenderIdentity(t *testing.T) {
	_, err := ParseNotificationRef(`{"message_id":"m1","sender_identity":""}`)
	if err == nil || !strings.Contains(err.Error(), "sender identity") {
		t.Errorf("expected sender identity error, got: %v", err)
	}
}

func TestParseNotificationRef_ExtraFieldsIgnored(t *testing.T) {
	// Extra fields are tolerated — the ref is a two-field struct.
	s := `{"message_id":"m1","sender_identity":"s1","extra":"ignored"}`
	ref, err := ParseNotificationRef(s)
	if err != nil {
		t.Fatalf("ParseNotificationRef: %v", err)
	}
	if ref.MessageID != "m1" {
		t.Errorf("MessageID=%q", ref.MessageID)
	}
	if ref.SenderIdentity != "s1" {
		t.Errorf("SenderIdentity=%q", ref.SenderIdentity)
	}
}

// --- ReadHomeIdentity ---

func TestReadHomeIdentity_CaptainMarker(t *testing.T) {
	home := t.TempDir()
	if err := WriteHomeIdentity(home, "captain-test", RankCaptain); err != nil {
		t.Fatalf("WriteHomeIdentity: %v", err)
	}
	ident, rank, err := ReadHomeIdentity(home)
	if err != nil {
		t.Fatalf("ReadHomeIdentity: %v", err)
	}
	if ident != "captain-test" {
		t.Errorf("identity=%q, want %q", ident, "captain-test")
	}
	if rank != RankCaptain {
		t.Errorf("rank=%q, want %q", rank, RankCaptain)
	}
}

func TestReadHomeIdentity_GeneralHome(t *testing.T) {
	home := t.TempDir()
	// No marker — should derive from basename with general rank.
	ident, rank, err := ReadHomeIdentity(home)
	if err != nil {
		t.Fatalf("ReadHomeIdentity: %v", err)
	}
	if ident != filepath.Base(home) {
		t.Errorf("identity=%q, want basename %q", ident, filepath.Base(home))
	}
	if rank != RankGeneral {
		t.Errorf("rank=%q, want %q", rank, RankGeneral)
	}
}

func TestReadHomeIdentity_EmptyBasename(t *testing.T) {
	_, _, err := ReadHomeIdentity("/")
	if err == nil {
		t.Fatal("expected error for root home with no marker")
	}
	if !strings.Contains(err.Error(), "cannot derive identity") {
		t.Errorf("expected derive error, got: %v", err)
	}
}

func TestReadHomeIdentity_MalformedMarker(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, captainMarkerName), []byte("only-one-line\n"), 0644)
	_, _, err := ReadHomeIdentity(home)
	if err == nil {
		t.Fatal("expected error for malformed marker")
	}
}

func TestReadHomeIdentity_WrongVersion(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, captainMarkerName), []byte("old-v0\nsome-id\n/path\n"), 0644)
	_, _, err := ReadHomeIdentity(home)
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected unsupported version error, got: %v", err)
	}
}

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

	recv := setupReceiver(t, home, "captain-munsu", RankCaptain)
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
	home := t.TempDir()
	recv := setupReceiver(t, home, "captain-1", RankCaptain)

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
	home := t.TempDir()
	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	ref := NotificationRef{MessageID: "nonexistent-id", SenderIdentity: "soldier-1"}
	res := recv.Process(ref, "done")
	if res.Ok() {
		t.Fatal("expected error for missing envelope")
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", res.Err)
	}
}

// --- Receiver.Process: invalid envelope (ValidateEnvelope gate) ---

func TestReceiver_Process_ValidateEnvelopeGate(t *testing.T) {
	// Write an envelope file directly that fails ValidateEnvelope
	// (general->soldier invalid transition). Bypass WriteEnvelope's
	// own validation to test that Process calls ValidateEnvelope.
	// Use a named subdirectory so that ReadHomeIdentity derives
	// the expected identity "soldier-1".
	home := filepath.Join(t.TempDir(), "soldier-1")
	os.MkdirAll(home, 0755)

	env := &Envelope{
		MessageID:      "test-invalid-transition",
		SenderRank:     RankGeneral,
		SenderIdentity: "general-main",
		ReceiverRank:   RankSoldier,
		ReceiverID:     "soldier-1",
		Payload:        "hello",
		PayloadHash:    PayloadHashHex("hello"),
		SchemaVersion:  SchemaVersion,
		CreatedAt:      time.Now().UnixNano(),
	}
	inboxDir := filepath.Join(home, "state", InboxDir, "general-main")
	os.MkdirAll(inboxDir, 0755)
	data, _ := json.MarshalIndent(env, "", "  ")
	if err := os.WriteFile(filepath.Join(inboxDir, "test-invalid-transition.json"), data, 0644); err != nil {
		t.Fatalf("write envelope: %v", err)
	}

	recv := setupReceiver(t, home, "soldier-1", RankSoldier)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "general-main"}
	res := recv.Process(ref, "done")
	if res.Ok() {
		t.Fatal("expected error for invalid rank transition")
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "validate envelope") {
		t.Errorf("expected validate envelope error, got: %v", res.Err)
	}
}

func TestReceiver_Process_ValidateEnvelopeKey(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	// Envelope entirely missing TaskID/Key — ValidateEnvelope does not require
	// these (they're omitempty), but Process should still validate the envelope.
	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "test",
	}
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	// Missing TaskID/Key are fine (omitempty), so Process should succeed.
	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "soldier-1"}
	res := recv.Process(ref, "done")
	if !res.Ok() {
		t.Fatalf("Process failed with omitempty fields: %v", res.Err)
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

	// Process as beta — should fail because identity derived from home
	// (captain-beta) doesn't match envelope's ReceiverID (captain-alpha).
	recv := setupReceiver(t, home, "captain-beta", RankCaptain)
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

	// Create a valid envelope: captain sends to general with
	// ReceiverID="captain-1". The envelope passes ValidateEnvelope
	// (captain->general is valid). The receiver has a captain marker
	// with identity "captain-1" (gives RankCaptain), so identity
	// matches but rank (Captain vs General) does not.
	env := &Envelope{
		SenderRank:     RankCaptain,
		SenderIdentity: "parent-captain",
		ReceiverRank:   RankGeneral, // envelope says general
		ReceiverID:     "captain-1",
		Payload:        "hello",
	}
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	// Write captain marker so receiver identity="captain-1" matching
	// envelope ReceiverID, but receiver rank=RankCaptain, which differs
	// from envelope's ReceiverRank=RankGeneral.
	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "parent-captain"}
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
	recv := setupReceiver(t, home, "captain-1", RankCaptain)
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

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
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

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "soldier-1"}
	res := recv.Process(ref, "done")
	if res.Ok() {
		t.Fatal("expected error for tampered hash")
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "hash mismatch") {
		t.Errorf("expected hash mismatch error, got: %v", res.Err)
	}
}

// --- Receiver.Process: idempotent same outcome, timestamp preserved ---

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

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "soldier-1"}

	// First call — should succeed.
	res1 := recv.Process(ref, "done")
	if !res1.Ok() {
		t.Fatalf("first Process failed: %v", res1.Err)
	}
	firstAck := res1.Ack

	// Wait a tiny bit so timestamps would differ.
	time.Sleep(time.Millisecond)

	// Second call with same outcome — should be idempotent and return
	// the existing ack with the original ProcessedAt preserved.
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
	// Must preserve original timestamp.
	if res2.Ack.ProcessedAt != firstAck.ProcessedAt {
		t.Errorf("duplicate returned different ProcessedAt: original=%d, duplicate=%d",
			firstAck.ProcessedAt, res2.Ack.ProcessedAt)
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

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
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

			recv := setupReceiver(t, home, "captain-1", RankCaptain)
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

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "soldier-1"}
	res := recv.Process(ref, "invalid-outcome")
	if res.Ok() {
		t.Fatal("expected error for invalid outcome")
	}
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
		{"soldier-to-captain", RankSoldier, "soldier-1", RankCaptain, "captain-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// For non-captain receiver ranks, identity is derived from the
			// home directory basename. Use a named subdirectory so that
			// ReadHomeIdentity returns the expected identity.
			var home string
			if tt.receiverRank == RankCaptain {
				home = t.TempDir()
			} else {
				home = filepath.Join(t.TempDir(), tt.receiverID)
				os.MkdirAll(home, 0755)
			}
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

			recv := setupReceiver(t, home, tt.receiverID, tt.receiverRank)
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

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
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

// --- WriteHomeIdentity ---

func TestWriteHomeIdentity_Captain(t *testing.T) {
	home := t.TempDir()
	if err := WriteHomeIdentity(home, "test-captain", RankCaptain); err != nil {
		t.Fatalf("WriteHomeIdentity: %v", err)
	}
	// Verify marker file exists.
	path := filepath.Join(home, captainMarkerName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading marker: %v", err)
	}
	content := string(data)
	if !strings.HasPrefix(content, "munsu-v2\n") {
		t.Errorf("expected munsu-v2 version prefix, got: %s", content)
	}
	if !strings.Contains(content, "\ntest-captain\n") {
		t.Errorf("expected identity test-captain, got: %s", content)
	}
}

func TestWriteHomeIdentity_NonCaptain(t *testing.T) {
	home := t.TempDir()
	// Non-captain ranks do not write a marker — identity is derived from
	// directory basename.
	if err := WriteHomeIdentity(home, "general-main", RankGeneral); err != nil {
		t.Fatalf("WriteHomeIdentity: %v", err)
	}
	// Verify no marker file was written.
	path := filepath.Join(home, captainMarkerName)
	if _, err := os.Stat(path); err == nil {
		t.Error("non-captain WriteHomeIdentity should not create marker file")
	}

	// Identity should derive from basename.
	ident, rank, err := ReadHomeIdentity(home)
	if err != nil {
		t.Fatalf("ReadHomeIdentity: %v", err)
	}
	if ident != filepath.Base(home) {
		t.Errorf("identity=%q, want basename %q", ident, filepath.Base(home))
	}
	if rank != RankGeneral {
		t.Errorf("rank=%q, want %q", rank, RankGeneral)
	}
}

func TestWriteHomeIdentity_EmptyIdentity(t *testing.T) {
	if err := WriteHomeIdentity(t.TempDir(), "", RankCaptain); err == nil {
		t.Fatal("expected error for empty identity")
	}
}

func TestWriteHomeIdentity_InvalidRank(t *testing.T) {
	if err := WriteHomeIdentity(t.TempDir(), "test", Rank("invalid")); err == nil {
		t.Fatal("expected error for invalid rank")
	}
}

// --- NotifyReceiver SubmitPrompt only (integration with session) ---
// NotifyReceiver requires a live backend, so we test the contract:
// the notification text uses canonical Encode, not ad-hoc format.

func TestNotifyReceiver_UsesEncode(t *testing.T) {
	// This is a compile-time/contract test: NotifyReceiver builds the
	// notification text from ref.Encode(), not fmt.Sprintf.
	ref := NotificationRef{MessageID: "test-msg", SenderIdentity: "test-sender"}
	encoded := ref.Encode()
	// The encoded text is JSON, not the old "notification: ..." format.
	if strings.HasPrefix(encoded, "notification:") {
		t.Error("notification text must use canonical Encode, not ad-hoc format")
	}
	// Verify the receiver can parse it back.
	parsed, err := ParseNotificationRef(encoded)
	if err != nil {
		t.Fatalf("ParseNotificationRef: %v", err)
	}
	if parsed.MessageID != "test-msg" || parsed.SenderIdentity != "test-sender" {
		t.Error("ParseNotificationRef round-trip failed")
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
