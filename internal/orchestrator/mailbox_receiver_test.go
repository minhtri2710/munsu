//go:build integration

package orchestrator

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

// setupEnvelope writes a valid envelope to the store and returns it.
func setupEnvelope(t *testing.T, store *Store, env *Envelope) *Envelope {
	t.Helper()
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	return env
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

// =============================================================
// Receiver.Receive tests — validate/load envelope, NO ack
// =============================================================

func TestReceiver_Receive_Valid(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := setupEnvelope(t, store, &Envelope{
		SenderRank:     RankGeneral,
		SenderIdentity: "general-main",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-munsu",
		TaskID:         "task:1",
		Key:            "my-key",
		Payload:        "do: work",
	})

	recv := setupReceiver(t, home, "captain-munsu", RankCaptain)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "general-main"}

	got, err := recv.Receive(ref)
	if err != nil {
		t.Fatalf("Receive failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil envelope")
	}
	if got.MessageID != env.MessageID {
		t.Errorf("MessageID=%q, want %q", got.MessageID, env.MessageID)
	}
	if got.Payload != "do: work" {
		t.Errorf("Payload=%q, want %q", got.Payload, "do: work")
	}

	// Verify NO ack was written.
	ack, err := store.ReadAck("general-main", env.MessageID)
	if err != nil {
		t.Fatalf("ReadAck: %v", err)
	}
	if ack != nil {
		t.Fatal("Receive must NOT write an ack")
	}
}

func TestReceiver_Receive_ReturnsMarkedPayload(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := setupEnvelope(t, store, &Envelope{
		SenderRank:     RankGeneral,
		SenderIdentity: "general-main",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "[from-general] report status",
	})

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "general-main"}

	got, err := recv.Receive(ref)
	if err != nil {
		t.Fatalf("Receive failed: %v", err)
	}
	if got.Payload != "[from-general] report status" {
		t.Errorf("Payload=%q", got.Payload)
	}

	// Verify no ack was written.
	ack, _ := store.ReadAck("general-main", env.MessageID)
	if ack != nil {
		t.Fatal("Receive must not write ack")
	}
}

func TestReceiver_Receive_MalformedRef(t *testing.T) {
	home := t.TempDir()
	recv := setupReceiver(t, home, "captain-1", RankCaptain)

	tests := []struct {
		name string
		ref  NotificationRef
	}{
		{"empty message ID", NotificationRef{MessageID: "", SenderIdentity: "general-1"}},
		{"empty sender identity", NotificationRef{MessageID: "msg-1", SenderIdentity: ""}},
		{"both empty", NotificationRef{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := recv.Receive(tt.ref)
			if err == nil {
				t.Fatal("expected error for malformed ref")
			}
		})
	}
}

func TestReceiver_Receive_MissingEnvelope(t *testing.T) {
	home := t.TempDir()
	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	_, err := recv.Receive(NotificationRef{MessageID: "nonexistent-id", SenderIdentity: "general-1"})
	if err == nil {
		t.Fatal("expected error for missing envelope")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestReceiver_Receive_ValidateEnvelopeGate(t *testing.T) {
	// Write an envelope file directly that fails ValidateEnvelope
	// (general->general is an invalid transition). Bypass WriteEnvelope's
	// own validation to test that Receive calls ValidateEnvelope.
	home := filepath.Join(t.TempDir(), "soldier-1")
	os.MkdirAll(home, 0755)

	env := &Envelope{
		MessageID:      "test-invalid-transition",
		SenderRank:     RankGeneral,
		SenderIdentity: "general-main",
		ReceiverRank:   RankGeneral,
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

	recv := setupReceiver(t, home, "soldier-1", RankGeneral)
	_, err := recv.Receive(NotificationRef{
		MessageID: env.MessageID, SenderIdentity: "general-main",
	})
	if err == nil {
		t.Fatal("expected error for invalid rank transition")
	}
	if !strings.Contains(err.Error(), "validate envelope") {
		t.Errorf("expected validate envelope error, got: %v", err)
	}
}

func TestReceiver_Receive_OmitemptyFields(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := setupEnvelope(t, store, &Envelope{
		SenderRank:     RankGeneral,
		SenderIdentity: "general-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "test",
	})

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	got, err := recv.Receive(NotificationRef{
		MessageID: env.MessageID, SenderIdentity: "general-1",
	})
	if err != nil {
		t.Fatalf("Receive failed with omitempty fields: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil envelope")
	}
}

func TestReceiver_Receive_WrongReceiverIdentity(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := setupEnvelope(t, store, &Envelope{
		SenderRank:     RankGeneral,
		SenderIdentity: "general-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-alpha",
		Payload:        "hello",
	})

	// Receive as beta — should fail because identity derived from home
	// (captain-beta) doesn't match envelope's ReceiverID (captain-alpha).
	recv := setupReceiver(t, home, "captain-beta", RankCaptain)
	_, err := recv.Receive(NotificationRef{
		MessageID: env.MessageID, SenderIdentity: "general-1",
	})
	if err == nil {
		t.Fatal("expected error for wrong receiver identity")
	}
	if !strings.Contains(err.Error(), "receiver identity mismatch") {
		t.Errorf("expected receiver identity mismatch error, got: %v", err)
	}
}

func TestReceiver_Receive_WrongReceiverRank(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := setupEnvelope(t, store, &Envelope{
		SenderRank:     RankCaptain,
		SenderIdentity: "parent-captain",
		ReceiverRank:   RankGeneral,
		ReceiverID:     "captain-1",
		Payload:        "hello",
	})

	// Write captain marker so receiver identity="captain-1" matching
	// envelope ReceiverID, but receiver rank=RankCaptain, which differs
	// from envelope's ReceiverRank=RankGeneral.
	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	_, err := recv.Receive(NotificationRef{
		MessageID: env.MessageID, SenderIdentity: "parent-captain",
	})
	if err == nil {
		t.Fatal("expected error for wrong receiver rank")
	}
	if !strings.Contains(err.Error(), "receiver rank mismatch") {
		t.Errorf("expected receiver rank mismatch error, got: %v", err)
	}
}

func TestReceiver_Receive_WrongSenderIdentity(t *testing.T) {
	home := t.TempDir()

	mismatchDir := filepath.Join(home, "state", InboxDir, "general-beta")
	if err := os.MkdirAll(mismatchDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mismatchEnv := &Envelope{
		MessageID:      "mismatch-msg",
		SenderRank:     RankGeneral,
		SenderIdentity: "general-alpha",
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

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	_, err := recv.Receive(NotificationRef{
		MessageID: "mismatch-msg", SenderIdentity: "general-beta",
	})
	if err == nil {
		t.Fatal("expected error for wrong sender identity")
	}
	if !strings.Contains(err.Error(), "sender identity mismatch") {
		t.Errorf("expected sender identity mismatch error, got: %v", err)
	}
}

func TestReceiver_Receive_TamperedPayloadHash(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := setupEnvelope(t, store, &Envelope{
		SenderRank:     RankGeneral,
		SenderIdentity: "general-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "original content",
	})

	// Tamper with the envelope file on disk: change the payload but not the hash.
	path := filepath.Join(home, "state", InboxDir, "general-1", env.MessageID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading envelope: %v", err)
	}
	tampered := strings.Replace(string(data), "original content", "tampered content", 1)
	if err := os.WriteFile(path, []byte(tampered), 0644); err != nil {
		t.Fatalf("writing tampered envelope: %v", err)
	}

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	_, err = recv.Receive(NotificationRef{
		MessageID: env.MessageID, SenderIdentity: "general-1",
	})
	if err == nil {
		t.Fatal("expected error for tampered payload")
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("expected hash mismatch error, got: %v", err)
	}
}

func TestReceiver_Receive_TamperedHashField(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := setupEnvelope(t, store, &Envelope{
		SenderRank:     RankGeneral,
		SenderIdentity: "general-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "content",
	})

	// Tamper with the payload_hash field directly.
	path := filepath.Join(home, "state", InboxDir, "general-1", env.MessageID+".json")
	var raw map[string]interface{}
	d, _ := os.ReadFile(path)
	json.Unmarshal(d, &raw)
	raw["payload_hash"] = "0000000000000000000000000000000000000000000000000000000000000000"
	tampered, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(path, tampered, 0644)

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	_, err := recv.Receive(NotificationRef{
		MessageID: env.MessageID, SenderIdentity: "general-1",
	})
	if err == nil {
		t.Fatal("expected error for tampered hash")
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("expected hash mismatch error, got: %v", err)
	}
}

// =============================================================
// Receiver.Ack tests — write "accepted" ack
// =============================================================

func TestReceiver_Ack_Valid(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := setupEnvelope(t, store, &Envelope{
		SenderRank:     RankGeneral,
		SenderIdentity: "general-main",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-munsu",
		TaskID:         "task:1",
		Key:            "my-key",
		Payload:        "do: work",
	})

	recv := setupReceiver(t, home, "captain-munsu", RankCaptain)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "general-main"}

	ack, err := recv.Ack(ref)
	if err != nil {
		t.Fatalf("Ack failed: %v", err)
	}
	if ack == nil {
		t.Fatal("expected non-nil ack")
	}
	if ack.Outcome != OutcomeAccepted {
		t.Errorf("ack outcome=%q, want %q", ack.Outcome, OutcomeAccepted)
	}
	if ack.MessageID != env.MessageID {
		t.Errorf("ack MessageID=%q, want %q", ack.MessageID, env.MessageID)
	}
	if ack.SenderIdentity != "general-main" {
		t.Errorf("ack SenderIdentity=%q", ack.SenderIdentity)
	}
	if ack.ReceiverID != "captain-munsu" {
		t.Errorf("ack ReceiverID=%q", ack.ReceiverID)
	}

	// Verify ack was written to disk.
	diskAck, err := store.ReadAck("general-main", env.MessageID)
	if err != nil {
		t.Fatalf("ReadAck: %v", err)
	}
	if diskAck == nil {
		t.Fatal("ack not found on disk")
	}
	if diskAck.Outcome != OutcomeAccepted {
		t.Errorf("disk ack outcome=%q, want %q", diskAck.Outcome, OutcomeAccepted)
	}
}

func TestReceiver_Ack_MalformedRef(t *testing.T) {
	home := t.TempDir()
	recv := setupReceiver(t, home, "captain-1", RankCaptain)

	tests := []struct {
		name string
		ref  NotificationRef
	}{
		{"empty message ID", NotificationRef{MessageID: "", SenderIdentity: "general-1"}},
		{"empty sender identity", NotificationRef{MessageID: "msg-1", SenderIdentity: ""}},
		{"both empty", NotificationRef{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := recv.Ack(tt.ref)
			if err == nil {
				t.Fatal("expected error for malformed ref")
			}
		})
	}
}

func TestReceiver_Ack_MissingEnvelope(t *testing.T) {
	home := t.TempDir()
	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	_, err := recv.Ack(NotificationRef{MessageID: "nonexistent-id", SenderIdentity: "general-1"})
	if err == nil {
		t.Fatal("expected error for missing envelope")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestReceiver_Ack_ValidateEnvelopeGate(t *testing.T) {
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
	_, err := recv.Ack(NotificationRef{
		MessageID: env.MessageID, SenderIdentity: "general-main",
	})
	if err == nil {
		t.Fatal("expected error for invalid rank transition")
	}
}

func TestReceiver_Ack_OmitemptyFields(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := setupEnvelope(t, store, &Envelope{
		SenderRank:     RankGeneral,
		SenderIdentity: "general-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "test",
	})

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	ack, err := recv.Ack(NotificationRef{
		MessageID: env.MessageID, SenderIdentity: "general-1",
	})
	if err != nil {
		t.Fatalf("Ack failed with omitempty fields: %v", err)
	}
	if ack == nil {
		t.Fatal("expected non-nil ack")
	}
	if ack.Outcome != OutcomeAccepted {
		t.Errorf("ack outcome=%q", ack.Outcome)
	}
}

func TestReceiver_Ack_WrongReceiverIdentity(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := setupEnvelope(t, store, &Envelope{
		SenderRank:     RankGeneral,
		SenderIdentity: "general-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-alpha",
		Payload:        "hello",
	})

	recv := setupReceiver(t, home, "captain-beta", RankCaptain)
	_, err := recv.Ack(NotificationRef{
		MessageID: env.MessageID, SenderIdentity: "general-1",
	})
	if err == nil {
		t.Fatal("expected error for wrong receiver identity")
	}
}

func TestReceiver_Ack_WrongReceiverRank(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := setupEnvelope(t, store, &Envelope{
		SenderRank:     RankCaptain,
		SenderIdentity: "parent-captain",
		ReceiverRank:   RankGeneral,
		ReceiverID:     "captain-1",
		Payload:        "hello",
	})

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	_, err := recv.Ack(NotificationRef{
		MessageID: env.MessageID, SenderIdentity: "parent-captain",
	})
	if err == nil {
		t.Fatal("expected error for wrong receiver rank")
	}
}

func TestReceiver_Ack_WrongSenderIdentity(t *testing.T) {
	home := t.TempDir()

	mismatchDir := filepath.Join(home, "state", InboxDir, "general-beta")
	if err := os.MkdirAll(mismatchDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mismatchEnv := &Envelope{
		MessageID:      "mismatch-msg",
		SenderRank:     RankGeneral,
		SenderIdentity: "general-alpha",
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

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	_, err := recv.Ack(NotificationRef{
		MessageID: "mismatch-msg", SenderIdentity: "general-beta",
	})
	if err == nil {
		t.Fatal("expected error for wrong sender identity")
	}
}

func TestReceiver_Ack_TamperedPayloadHash(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := setupEnvelope(t, store, &Envelope{
		SenderRank:     RankGeneral,
		SenderIdentity: "general-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "original content",
	})

	path := filepath.Join(home, "state", InboxDir, "general-1", env.MessageID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading envelope: %v", err)
	}
	tampered := strings.Replace(string(data), "original content", "tampered content", 1)
	if err := os.WriteFile(path, []byte(tampered), 0644); err != nil {
		t.Fatalf("writing tampered envelope: %v", err)
	}

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	_, err = recv.Ack(NotificationRef{
		MessageID: env.MessageID, SenderIdentity: "general-1",
	})
	if err == nil {
		t.Fatal("expected error for tampered payload")
	}
}

func TestReceiver_Ack_TamperedHashField(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := setupEnvelope(t, store, &Envelope{
		SenderRank:     RankGeneral,
		SenderIdentity: "general-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "content",
	})

	path := filepath.Join(home, "state", InboxDir, "general-1", env.MessageID+".json")
	var raw map[string]interface{}
	d, _ := os.ReadFile(path)
	json.Unmarshal(d, &raw)
	raw["payload_hash"] = "0000000000000000000000000000000000000000000000000000000000000000"
	tampered, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(path, tampered, 0644)

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	_, err := recv.Ack(NotificationRef{
		MessageID: env.MessageID, SenderIdentity: "general-1",
	})
	if err == nil {
		t.Fatal("expected error for tampered hash")
	}
}

// --- Acquisition ack idempotence ---

func TestReceiver_Ack_DuplicateSameOutcome(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := setupEnvelope(t, store, &Envelope{
		SenderRank:     RankGeneral,
		SenderIdentity: "general-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "do: work",
	})

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "general-1"}

	// First call — should succeed with "accepted".
	ack1, err := recv.Ack(ref)
	if err != nil {
		t.Fatalf("first Ack failed: %v", err)
	}
	if ack1.Outcome != OutcomeAccepted {
		t.Errorf("ack1 outcome=%q, want %q", ack1.Outcome, OutcomeAccepted)
	}

	// Wait a tiny bit so timestamps would differ.
	time.Sleep(time.Millisecond)

	// Second call with same ref — should be idempotent and return
	// the existing ack with the original ProcessedAt preserved.
	ack2, err := recv.Ack(ref)
	if err != nil {
		t.Fatalf("second Ack (same ref) failed: %v", err)
	}
	if ack2 == nil {
		t.Fatal("expected non-nil ack on duplicate")
	}
	if ack2.Outcome != OutcomeAccepted {
		t.Errorf("duplicate ack outcome=%q, want %q", ack2.Outcome, OutcomeAccepted)
	}
	// Must preserve original timestamp.
	if ack2.ProcessedAt != ack1.ProcessedAt {
		t.Errorf("duplicate returned different ProcessedAt: original=%d, duplicate=%d",
			ack1.ProcessedAt, ack2.ProcessedAt)
	}
	// Only one ack file on disk.
	count := countAckFiles(t, home, "general-1", env.MessageID)
	if count != 1 {
		t.Errorf("expected 1 ack file on disk, got %d", count)
	}
}

// --- Acquisition ack conflict ---

func TestReceiver_Ack_ConflictingOutcome(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := setupEnvelope(t, store, &Envelope{
		SenderRank:     RankGeneral,
		SenderIdentity: "general-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "work item",
	})

	// Manually write a "done" ack (simulate corruption or protocol change).
	// This bypasses Ack which only writes "accepted".
	doneAck := &ProcessingAck{
		MessageID: env.MessageID, SenderRank: env.SenderRank,
		SenderIdentity: env.SenderIdentity, ReceiverRank: env.ReceiverRank,
		ReceiverID: env.ReceiverID, PayloadHash: env.PayloadHash,
		ProcessedAt: time.Now().UnixNano(), Outcome: "done",
	}
	if err := store.WriteAck(doneAck); err != nil {
		t.Fatalf("WriteAck: %v", err)
	}

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: "general-1"}

	// Ack with "accepted" should fail closed because existing outcome is "done".
	_, err := recv.Ack(ref)
	if err == nil {
		t.Fatal("expected error for conflicting outcome")
	}
	if !strings.Contains(err.Error(), "conflicting") {
		t.Errorf("expected conflicting error, got: %v", err)
	}
}

// =============================================================
// Different rank transitions
// =============================================================

func TestReceiver_Ack_DifferentRankTransitions(t *testing.T) {
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
			var home string
			if tt.receiverRank == RankCaptain {
				home = t.TempDir()
			} else {
				home = filepath.Join(t.TempDir(), tt.receiverID)
				os.MkdirAll(home, 0755)
			}
			store := NewStore(home)

			env := setupEnvelope(t, store, &Envelope{
				SenderRank:     tt.senderRank,
				SenderIdentity: tt.senderID,
				ReceiverRank:   tt.receiverRank,
				ReceiverID:     tt.receiverID,
				Payload:        "work",
			})

			recv := setupReceiver(t, home, tt.receiverID, tt.receiverRank)
			ack, err := recv.Ack(NotificationRef{
				MessageID: env.MessageID, SenderIdentity: tt.senderID,
			})
			if err != nil {
				t.Fatalf("Ack failed: %v", err)
			}
			if ack.Outcome != OutcomeAccepted {
				t.Errorf("ack outcome=%q, want %q", ack.Outcome, OutcomeAccepted)
			}
			if ack.ReceiverRank != tt.receiverRank {
				t.Errorf("ack ReceiverRank=%q, want %q", ack.ReceiverRank, tt.receiverRank)
			}
		})
	}
}

// --- Acquisition ack with task/key fields ---

func TestReceiver_Ack_WithTaskAndKey(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := setupEnvelope(t, store, &Envelope{
		SenderRank:     RankGeneral,
		SenderIdentity: "general-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "task:rebrand-phase1",
		Key:            "real-estate-apex",
		Payload:        "do: rebrand work",
	})

	recv := setupReceiver(t, home, "captain-1", RankCaptain)
	ack, err := recv.Ack(NotificationRef{
		MessageID: env.MessageID, SenderIdentity: "general-1",
	})
	if err != nil {
		t.Fatalf("Ack failed: %v", err)
	}
	if ack.TaskID != "task:rebrand-phase1" {
		t.Errorf("ack TaskID=%q", ack.TaskID)
	}
	if ack.Key != "real-estate-apex" {
		t.Errorf("ack Key=%q", ack.Key)
	}
	if ack.Outcome != OutcomeAccepted {
		t.Errorf("ack outcome=%q, want %q", ack.Outcome, OutcomeAccepted)
	}
}

// =============================================================
// NotifyResult: Acknowledged field preserved, pending not removed
// =============================================================

func TestNotifyResult_PendingNotRemoved(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := &Envelope{
		MessageID:      "msg-pending-test",
		SenderRank:     RankGeneral,
		SenderIdentity: "general-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "do: test",
		PayloadHash:    PayloadHashHex("do: test"),
	}
	if err := store.WritePending(env); err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	// Verify pending exists.
	pending, err := store.ReadPending("general-1", "msg-pending-test")
	if err != nil || pending == nil {
		t.Fatal("pending should exist")
	}

	// NotifyResult with Acknowledged=true — notification acknowledgment
	// must not remove pending.
	nr := &NotifyResult{
		Ref:          NotificationRef{MessageID: "msg-pending-test", SenderIdentity: "general-1"},
		Acknowledged: true,
		Status:       "submitted",
	}
	_ = nr

	// Verify pending still exists after notification acknowledgment.
	pending, err = store.ReadPending("general-1", "msg-pending-test")
	if err != nil || pending == nil {
		t.Fatal("pending must still exist after notification acknowledgment")
	}

	// Verify pending is only removed through the exact ack + reconcile flow.
	ack := &ProcessingAck{
		MessageID: "msg-pending-test", SenderRank: RankGeneral,
		SenderIdentity: "general-1", ReceiverRank: RankCaptain,
		ReceiverID: "captain-1", PayloadHash: PayloadHashHex("do: test"),
		ProcessedAt: time.Now().UnixNano(), Outcome: OutcomeAccepted,
	}
	if err := store.WriteAck(ack); err != nil {
		t.Fatalf("WriteAck: %v", err)
	}
	if err := store.RemovePendingAfterAck("general-1", "msg-pending-test", ack); err != nil {
		t.Fatalf("RemovePendingAfterAck: %v", err)
	}
	pending, _ = store.ReadPending("general-1", "msg-pending-test")
	if pending != nil {
		t.Fatal("pending should be removed only after validated ack")
	}
}

// =============================================================
// NotificationRef JSON round-trip
// =============================================================

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
	if err := WriteHomeIdentity(home, "general-main", RankGeneral); err != nil {
		t.Fatalf("WriteHomeIdentity: %v", err)
	}
	path := filepath.Join(home, captainMarkerName)
	if _, err := os.Stat(path); err == nil {
		t.Error("non-captain WriteHomeIdentity should not create marker file")
	}
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

// --- NotifyReceiver contract test ---

func TestNotifyReceiver_UsesEncode(t *testing.T) {
	ref := NotificationRef{MessageID: "test-msg", SenderIdentity: "test-sender"}
	encoded := ref.Encode()
	if strings.HasPrefix(encoded, "notification:") {
		t.Error("notification text must use canonical Encode, not ad-hoc format")
	}
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
