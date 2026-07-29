//go:build integration

package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Envelope creation and validation ---

func TestNewMessageID_ProducesHex(t *testing.T) {
	id, err := NewMessageID()
	if err != nil {
		t.Fatalf("NewMessageID: %v", err)
	}
	if len(id) != 32 {
		t.Errorf("message ID length=%d, want 32", len(id))
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex char %c in message ID", c)
			break
		}
	}
}

func TestPayloadHashHex(t *testing.T) {
	hash := PayloadHashHex("hello")
	if len(hash) != 64 {
		t.Errorf("hash length=%d, want 64", len(hash))
	}
	if PayloadHashHex("hello") != PayloadHashHex("hello") {
		t.Error("hash not deterministic")
	}
	if PayloadHashHex("hello") == PayloadHashHex("world") {
		t.Error("hash collision")
	}
}

func TestValidRank(t *testing.T) {
	if !ValidRank(RankGeneral) {
		t.Error("general should be valid")
	}
	if !ValidRank(RankCaptain) {
		t.Error("captain should be valid")
	}
	if !ValidRank(RankSoldier) {
		t.Error("soldier should be valid")
	}
	if ValidRank(Rank("invalid")) {
		t.Error("invalid rank should not be valid")
	}
}

func TestValidateEnvelope_ValidTransitions(t *testing.T) {
	tests := []struct {
		name      string
		sender    Rank
		receiver  Rank
		wantError bool
	}{
		{"general->captain", RankGeneral, RankCaptain, false},
		{"captain->general", RankCaptain, RankGeneral, false},
		{"captain->soldier", RankCaptain, RankSoldier, false},
		{"soldier->captain", RankSoldier, RankCaptain, false},
		{"general->soldier", RankGeneral, RankSoldier, true},
		{"general->general", RankGeneral, RankGeneral, true},
		{"captain->captain", RankCaptain, RankCaptain, true},
		{"soldier->general", RankSoldier, RankGeneral, true},
		{"soldier->soldier", RankSoldier, RankSoldier, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := &Envelope{
				MessageID:      "test-id-1234567890abcdef",
				SenderRank:     tt.sender,
				SenderIdentity: "sender-1",
				ReceiverRank:   tt.receiver,
				ReceiverID:     "receiver-1",
				Payload:        "test message",
				PayloadHash:    PayloadHashHex("test message"),
			}
			err := ValidateEnvelope(env)
			if tt.wantError && err == nil {
				t.Error("expected validation error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("unexpected validation error: %v", err)
			}
		})
	}
}

// --- Immutability ---

func TestEnvelope_ImmutableStruct(t *testing.T) {
	// The Envelope struct has no mutable delivery/ack fields.
	// This test verifies the struct shape: no DeliveryStatus, State, etc.
	env := &Envelope{
		MessageID:      "test-id",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "task:1",
		Key:            "my-key",
		Payload:        "done: work",
		PayloadHash:    PayloadHashHex("done: work"),
		CreatedAt:      1000,
	}
	// Verify that a round-trip through JSON preserves all fields and adds none.
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.MessageID != env.MessageID {
		t.Errorf("MessageID mismatch")
	}
	if decoded.SenderRank != env.SenderRank {
		t.Errorf("SenderRank mismatch")
	}
	if decoded.SenderIdentity != env.SenderIdentity {
		t.Errorf("SenderIdentity mismatch")
	}
	if decoded.ReceiverRank != env.ReceiverRank {
		t.Errorf("ReceiverRank mismatch")
	}
	if decoded.ReceiverID != env.ReceiverID {
		t.Errorf("ReceiverID mismatch")
	}
	if decoded.TaskID != env.TaskID {
		t.Errorf("TaskID mismatch")
	}
	if decoded.Key != env.Key {
		t.Errorf("Key mismatch")
	}
	if decoded.Payload != env.Payload {
		t.Errorf("Payload mismatch")
	}
	if decoded.PayloadHash != env.PayloadHash {
		t.Errorf("PayloadHash mismatch")
	}
	if decoded.CreatedAt != env.CreatedAt {
		t.Errorf("CreatedAt mismatch")
	}
	// SchemaVersion is preserved through JSON round-trip.
	if decoded.SchemaVersion != "" {
		t.Logf("SchemaVersion in raw struct: %q (OK if empty)", decoded.SchemaVersion)
	}
	// Verify no fields named delivery_status, state, etc. exist in output.
	forbidden := []string{"delivery_status", "state", "delivery_attempts", "last_attempt_at", "acked_at"}
	for _, f := range forbidden {
		if strings.Contains(string(data), f) {
			t.Errorf("output contains forbidden field %q", f)
		}
	}
}

// --- Store.WriteEnvelope / ReadEnvelope ---

func TestStore_WriteEnvelope_WritesToReceiverInbox(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-task-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-munsu",
		Payload:        "done: task complete",
	}
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	inboxDir := filepath.Join(home, "state", InboxDir, env.SenderIdentity)
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		t.Fatalf("reading inbox dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 inbox entry, got %d", len(entries))
	}
	if !strings.HasSuffix(entries[0].Name(), ".json") {
		t.Errorf("expected .json file, got %s", entries[0].Name())
	}

	got, err := store.ReadEnvelope(env.SenderIdentity, env.MessageID)
	if err != nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	if got == nil {
		t.Fatal("envelope not found")
	}
	if got.Payload != "done: task complete" {
		t.Errorf("payload=%q", got.Payload)
	}
	if got.CreatedAt == 0 {
		t.Error("CreatedAt should be set")
	}
}

func TestStore_WriteEnvelope_AutoGeneratesID(t *testing.T) {
	store := NewStore(t.TempDir())
	env := &Envelope{
		SenderRank:     RankCaptain,
		SenderIdentity: "captain-1",
		ReceiverRank:   RankGeneral,
		ReceiverID:     "general-main",
		Payload:        "report",
	}
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	if env.MessageID == "" {
		t.Fatal("MessageID should be auto-generated")
	}
}

func TestStore_ReadEnvelope_NotFound(t *testing.T) {
	store := NewStore(t.TempDir())
	got, err := store.ReadEnvelope("sender-1", "nonexistent-id")
	if err != nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for nonexistent envelope")
	}
}

// --- Atomic write (no partial JSON) ---

func TestStore_AtomicWrite_NoPartialJSON(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "complete payload",
	}
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	// Read the file directly and verify it's valid JSON.
	path := filepath.Join(home, "state", InboxDir, "soldier-1", env.MessageID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if !json.Valid(data) {
		t.Fatal("written file is not valid JSON")
	}

	// Verify no temp files remain.
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("stale temp file found: %s", e.Name())
		}
	}

	// Verify a concurrent write doesn't produce partial content.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			e := &Envelope{
				SenderRank:     RankSoldier,
				SenderIdentity: "soldier-1",
				ReceiverRank:   RankCaptain,
				ReceiverID:     "captain-1",
				Payload:        "payload",
			}
			store.WriteEnvelope(e) // error intentionally ignored in goroutine
		}(i)
	}
	wg.Wait()

	// All envelope files must be valid JSON.
	inboxDir := filepath.Join(home, "state", InboxDir, "soldier-1")
	entries, err = os.ReadDir(inboxDir)
	if err != nil {
		t.Fatalf("reading inbox: %v", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(inboxDir, e.Name()))
		if err != nil {
			t.Errorf("reading %s: %v", e.Name(), err)
			continue
		}
		if !json.Valid(data) {
			t.Errorf("file %s is not valid JSON", e.Name())
		}
	}
}

// --- Identity path isolation ---

func TestStore_Pending_IdentityPathIsolation(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	// Two different sender identities share the same home.
	env1 := &Envelope{
		MessageID:      "msg-1",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-alpha",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "report from alpha",
	}
	env2 := &Envelope{
		MessageID:      "msg-2",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-beta",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "report from beta",
	}

	if err := store.WritePending(env1); err != nil {
		t.Fatalf("WritePending alpha: %v", err)
	}
	if err := store.WritePending(env2); err != nil {
		t.Fatalf("WritePending beta: %v", err)
	}

	// Each identity should have exactly one pending in its own directory.
	alphaPending, err := store.ListPending("soldier-alpha")
	if err != nil {
		t.Fatalf("ListPending alpha: %v", err)
	}
	if len(alphaPending) != 1 || alphaPending[0].SenderIdentity != "soldier-alpha" {
		t.Errorf("alpha pending: expected 1 for alpha, got %d", len(alphaPending))
	}

	betaPending, err := store.ListPending("soldier-beta")
	if err != nil {
		t.Fatalf("ListPending beta: %v", err)
	}
	if len(betaPending) != 1 || betaPending[0].SenderIdentity != "soldier-beta" {
		t.Errorf("beta pending: expected 1 for beta, got %d", len(betaPending))
	}

	// Write ack files so RemovePendingAfterAck can validate.
	pending1, _ := store.ReadPending("soldier-alpha", "msg-1")
	ack1 := &ProcessingAck{
		MessageID: pending1.MessageID, SenderRank: pending1.SenderRank,
		SenderIdentity: pending1.SenderIdentity, ReceiverRank: pending1.ReceiverRank,
		ReceiverID: pending1.ReceiverID, PayloadHash: pending1.PayloadHash,
		ProcessedAt: time.Now().UnixNano(), Outcome: "done",
	}
	store.WriteAck(ack1)

	// Removing alpha's pending should not affect beta's.
	if err := store.RemovePendingAfterAck("soldier-alpha", "msg-1", ack1); err != nil {
		t.Fatalf("RemovePendingAfterAck alpha: %v", err)
	}
	alphaPending, _ = store.ListPending("soldier-alpha")
	if len(alphaPending) != 0 {
		t.Error("alpha pending should be empty after remove")
	}
	betaPending, _ = store.ListPending("soldier-beta")
	if len(betaPending) != 1 {
		t.Error("beta pending should still have 1 after alpha remove")
	}
}

// --- ProcessingAck ---

func TestStore_WriteAck_AndRead(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	ack := &ProcessingAck{
		MessageID:      "msg-1",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "task:1",
		Key:            "my-key",
		PayloadHash:    PayloadHashHex("done: work"),
		ProcessedAt:    time.Now().UnixNano(),
		Outcome:        "done",
	}
	if err := store.WriteAck(ack); err != nil {
		t.Fatalf("WriteAck: %v", err)
	}

	got, err := store.ReadAck("soldier-1", "msg-1")
	if err != nil {
		t.Fatalf("ReadAck: %v", err)
	}
	if got == nil {
		t.Fatal("ack not found")
	}
	if got.MessageID != ack.MessageID {
		t.Errorf("MessageID: %q != %q", got.MessageID, ack.MessageID)
	}
	if got.Outcome != "done" {
		t.Errorf("Outcome: %q != %q", got.Outcome, "done")
	}
	if got.SchemaVersion != AckSchemaVersion {
		t.Errorf("SchemaVersion: %q != %q", got.SchemaVersion, AckSchemaVersion)
	}
}

func TestStore_IsAcked(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	ack := &ProcessingAck{
		MessageID:      "msg-1",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		PayloadHash:    PayloadHashHex("done"),
		ProcessedAt:    time.Now().UnixNano(),
		Outcome:        "done",
	}

	if store.IsAcked("soldier-1", "msg-1") {
		t.Error("should not be acked before WriteAck")
	}

	if err := store.WriteAck(ack); err != nil {
		t.Fatalf("WriteAck: %v", err)
	}

	if !store.IsAcked("soldier-1", "msg-1") {
		t.Error("should be acked after WriteAck")
	}
}

// --- Exact ack validation ---

func TestValidateAck_ExactMatch(t *testing.T) {
	env := &Envelope{
		MessageID:      "msg-1",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "task:1",
		Key:            "my-key",
		Payload:        "done: work",
		PayloadHash:    PayloadHashHex("done: work"),
	}
	ack := &ProcessingAck{
		MessageID:      "msg-1",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "task:1",
		Key:            "my-key",
		PayloadHash:    PayloadHashHex("done: work"),
		Outcome:        "done",
	}
	if err := ValidateAck(env, ack); err != nil {
		t.Fatalf("ValidateAck exact match: %v", err)
	}
}

func TestValidateAck_WrongMessageID(t *testing.T) {
	env := &Envelope{MessageID: "msg-1", PayloadHash: PayloadHashHex("x")}
	ack := &ProcessingAck{MessageID: "msg-2", PayloadHash: PayloadHashHex("x")}
	if err := ValidateAck(env, ack); err == nil || !strings.Contains(err.Error(), "message ID") {
		t.Errorf("expected message ID mismatch error, got: %v", err)
	}
}

func TestValidateAck_WrongSenderRank(t *testing.T) {
	env := &Envelope{MessageID: "m", SenderRank: RankSoldier, SenderIdentity: "s", ReceiverRank: RankCaptain, ReceiverID: "r", PayloadHash: PayloadHashHex("x")}
	ack := &ProcessingAck{MessageID: "m", SenderRank: RankCaptain, SenderIdentity: "s", ReceiverRank: RankCaptain, ReceiverID: "r", PayloadHash: PayloadHashHex("x")}
	if err := ValidateAck(env, ack); err == nil || !strings.Contains(err.Error(), "sender rank") {
		t.Errorf("expected sender rank error, got: %v", err)
	}
}

func TestValidateAck_WrongSenderIdentity(t *testing.T) {
	env := &Envelope{MessageID: "m", SenderRank: RankSoldier, SenderIdentity: "soldier-1", ReceiverRank: RankCaptain, ReceiverID: "r", PayloadHash: PayloadHashHex("x")}
	ack := &ProcessingAck{MessageID: "m", SenderRank: RankSoldier, SenderIdentity: "soldier-2", ReceiverRank: RankCaptain, ReceiverID: "r", PayloadHash: PayloadHashHex("x")}
	if err := ValidateAck(env, ack); err == nil || !strings.Contains(err.Error(), "sender identity") {
		t.Errorf("expected sender identity error, got: %v", err)
	}
}

func TestValidateAck_WrongReceiverRank(t *testing.T) {
	env := &Envelope{MessageID: "m", SenderRank: RankSoldier, SenderIdentity: "s", ReceiverRank: RankCaptain, ReceiverID: "r", PayloadHash: PayloadHashHex("x")}
	ack := &ProcessingAck{MessageID: "m", SenderRank: RankSoldier, SenderIdentity: "s", ReceiverRank: RankGeneral, ReceiverID: "r", PayloadHash: PayloadHashHex("x")}
	if err := ValidateAck(env, ack); err == nil || !strings.Contains(err.Error(), "receiver rank") {
		t.Errorf("expected receiver rank error, got: %v", err)
	}
}

func TestValidateAck_WrongReceiverID(t *testing.T) {
	env := &Envelope{MessageID: "m", SenderRank: RankSoldier, SenderIdentity: "s", ReceiverRank: RankCaptain, ReceiverID: "captain-1", PayloadHash: PayloadHashHex("x")}
	ack := &ProcessingAck{MessageID: "m", SenderRank: RankSoldier, SenderIdentity: "s", ReceiverRank: RankCaptain, ReceiverID: "captain-2", PayloadHash: PayloadHashHex("x")}
	if err := ValidateAck(env, ack); err == nil || !strings.Contains(err.Error(), "receiver ID") {
		t.Errorf("expected receiver ID error, got: %v", err)
	}
}

func TestValidateAck_WrongTaskID(t *testing.T) {
	env := &Envelope{MessageID: "m", SenderRank: RankSoldier, SenderIdentity: "s", ReceiverRank: RankCaptain, ReceiverID: "r", TaskID: "task:1", PayloadHash: PayloadHashHex("x")}
	ack := &ProcessingAck{MessageID: "m", SenderRank: RankSoldier, SenderIdentity: "s", ReceiverRank: RankCaptain, ReceiverID: "r", TaskID: "task:2", PayloadHash: PayloadHashHex("x")}
	if err := ValidateAck(env, ack); err == nil || !strings.Contains(err.Error(), "task ID") {
		t.Errorf("expected task ID error, got: %v", err)
	}
}

func TestValidateAck_WrongKey(t *testing.T) {
	env := &Envelope{MessageID: "m", SenderRank: RankSoldier, SenderIdentity: "s", ReceiverRank: RankCaptain, ReceiverID: "r", Key: "key-a", PayloadHash: PayloadHashHex("x")}
	ack := &ProcessingAck{MessageID: "m", SenderRank: RankSoldier, SenderIdentity: "s", ReceiverRank: RankCaptain, ReceiverID: "r", Key: "key-b", PayloadHash: PayloadHashHex("x")}
	if err := ValidateAck(env, ack); err == nil || !strings.Contains(err.Error(), "key") {
		t.Errorf("expected key error, got: %v", err)
	}
}

func TestValidateAck_WrongPayloadHash(t *testing.T) {
	env := &Envelope{MessageID: "m", SenderRank: RankSoldier, SenderIdentity: "s", ReceiverRank: RankCaptain, ReceiverID: "r", Payload: "hello", PayloadHash: PayloadHashHex("hello")}
	ack := &ProcessingAck{MessageID: "m", SenderRank: RankSoldier, SenderIdentity: "s", ReceiverRank: RankCaptain, ReceiverID: "r", PayloadHash: PayloadHashHex("world")}
	if err := ValidateAck(env, ack); err == nil || !strings.Contains(err.Error(), "payload hash") {
		t.Errorf("expected payload hash error, got: %v", err)
	}
}

// --- ValidOutcome ---

func TestValidOutcome(t *testing.T) {
	if !ValidOutcome("done") {
		t.Error("done should be valid")
	}
	if !ValidOutcome("failed") {
		t.Error("failed should be valid")
	}
	if !ValidOutcome("needs-decision") {
		t.Error("needs-decision should be valid")
	}
	if !ValidOutcome("blocked") {
		t.Error("blocked should be valid")
	}
	if !ValidOutcome("paused") {
		t.Error("paused should be valid")
	}
	if ValidOutcome("") {
		t.Error("empty should not be valid")
	}
	if ValidOutcome("invalid") {
		t.Error("invalid should not be valid")
	}
}

// --- ValidateProcessingAck ---

func TestValidateProcessingAck_Valid(t *testing.T) {
	ack := &ProcessingAck{
		ProcessedAt: 1000,
		Outcome:     "done",
	}
	if err := ValidateProcessingAck(ack); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateProcessingAck_ProcessedAtZero(t *testing.T) {
	ack := &ProcessingAck{
		ProcessedAt: 0,
		Outcome:     "done",
	}
	if err := ValidateProcessingAck(ack); err == nil || !strings.Contains(err.Error(), "processed_at") {
		t.Errorf("expected processed_at error, got: %v", err)
	}
}

func TestValidateProcessingAck_ProcessedAtNegative(t *testing.T) {
	ack := &ProcessingAck{
		ProcessedAt: -1,
		Outcome:     "done",
	}
	if err := ValidateProcessingAck(ack); err == nil || !strings.Contains(err.Error(), "processed_at") {
		t.Errorf("expected processed_at error, got: %v", err)
	}
}

func TestValidateProcessingAck_InvalidOutcome(t *testing.T) {
	ack := &ProcessingAck{
		ProcessedAt: 1000,
		Outcome:     "invalid",
	}
	if err := ValidateProcessingAck(ack); err == nil || !strings.Contains(err.Error(), "outcome") {
		t.Errorf("expected outcome error, got: %v", err)
	}
}

func TestValidateProcessingAck_EmptyOutcome(t *testing.T) {
	ack := &ProcessingAck{
		ProcessedAt: 1000,
		Outcome:     "",
	}
	if err := ValidateProcessingAck(ack); err == nil || !strings.Contains(err.Error(), "outcome") {
		t.Errorf("expected outcome error, got: %v", err)
	}
}

// --- ValidatePathComponent ---

func TestValidatePathComponent_Valid(t *testing.T) {
	tests := []string{
		"alpha",
		"soldier-1",
		"captain_main",
		"msg.id",
		"a",
		"0123456789abcdef0123456789abcdef",
	}
	for _, tc := range tests {
		t.Run(tc, func(t *testing.T) {
			if err := ValidatePathComponent(tc, "test"); err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

func TestValidatePathComponent_Slash(t *testing.T) {
	if err := ValidatePathComponent("a/b", "test"); err == nil || !strings.Contains(err.Error(), "slash") {
		t.Errorf("expected slash error, got: %v", err)
	}
}

func TestValidatePathComponent_Backslash(t *testing.T) {
	if err := ValidatePathComponent("a\\b", "test"); err == nil || !strings.Contains(err.Error(), "backslash") {
		t.Errorf("expected backslash error, got: %v", err)
	}
}

func TestValidatePathComponent_DotDot(t *testing.T) {
	if err := ValidatePathComponent("..", "test"); err == nil || !strings.Contains(err.Error(), "relative path") {
		t.Errorf("expected relative path error, got: %v", err)
	}
}

func TestValidatePathComponent_Empty(t *testing.T) {
	if err := ValidatePathComponent("", "test"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected empty error, got: %v", err)
	}
}

func TestValidatePathComponent_Colon(t *testing.T) {
	if err := ValidatePathComponent("a:b", "test"); err == nil || !strings.Contains(err.Error(), "colon") {
		t.Errorf("expected colon error, got: %v", err)
	}
}

// --- Idempotent envelope write ---

func TestStore_WriteEnvelope_Idempotent_SameContent(t *testing.T) {
	store := NewStore(t.TempDir())

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "done: same content",
	}
	// Write twice with same content — must be idempotent.
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("first WriteEnvelope: %v", err)
	}
	msgID := env.MessageID
	env2 := &Envelope{
		MessageID:      msgID,
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "done: same content",
	}
	if err := store.WriteEnvelope(env2); err != nil {
		t.Fatalf("second WriteEnvelope (same content): %v", err)
	}
}

func TestStore_WriteEnvelope_Idempotent_Conflict(t *testing.T) {
	store := NewStore(t.TempDir())

	env := &Envelope{
		MessageID:      "fixed-id",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "first content",
	}
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("first WriteEnvelope: %v", err)
	}

	// Same message ID, different payload — must fail.
	env2 := &Envelope{
		MessageID:      "fixed-id",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "different content",
	}
	if err := store.WriteEnvelope(env2); err == nil {
		t.Fatal("expected conflict error, got nil")
	} else if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("expected conflict error, got: %v", err)
	}

	// Different receiver ID with same message ID — must fail.
	env3 := &Envelope{
		MessageID:      "fixed-id",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-2",
		Payload:        "first content",
	}
	if err := store.WriteEnvelope(env3); err == nil {
		t.Fatal("expected conflict error for different receiver ID, got nil")
	} else if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("expected conflict error, got: %v", err)
	}

	// Different sender rank with same message ID — must fail.
	env4 := &Envelope{
		MessageID:      "fixed-id",
		SenderRank:     RankCaptain,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankGeneral,
		ReceiverID:     "captain-1",
		Payload:        "first content",
	}
	if err := store.WriteEnvelope(env4); err == nil {
		t.Fatal("expected conflict error for different rank, got nil")
	} else if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("expected conflict error, got: %v", err)
	}
}

// --- Idempotent ack ---

func TestStore_WriteAck_Conflict(t *testing.T) {
	store := NewStore(t.TempDir())

	ack := &ProcessingAck{
		MessageID:      "msg-conflict",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		PayloadHash:    PayloadHashHex("first"),
		ProcessedAt:    time.Now().UnixNano(),
		Outcome:        "done",
	}
	if err := store.WriteAck(ack); err != nil {
		t.Fatalf("first WriteAck: %v", err)
	}

	// Different outcome — must fail.
	ack2 := &ProcessingAck{
		MessageID:      "msg-conflict",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		PayloadHash:    PayloadHashHex("first"),
		ProcessedAt:    time.Now().UnixNano(),
		Outcome:        "failed",
	}
	if err := store.WriteAck(ack2); err == nil {
		t.Fatal("expected conflict error, got nil")
	} else if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("expected conflict error, got: %v", err)
	}
}

func TestStore_WriteAck_Idempotent(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	ack := &ProcessingAck{
		MessageID:      "msg-1",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		PayloadHash:    PayloadHashHex("work"),
		ProcessedAt:    time.Now().UnixNano(),
		Outcome:        "done",
	}
	// Write same ack twice.
	if err := store.WriteAck(ack); err != nil {
		t.Fatalf("first WriteAck: %v", err)
	}
	if err := store.WriteAck(ack); err != nil {
		t.Fatalf("second WriteAck: %v", err)
	}

	got, err := store.ReadAck("soldier-1", "msg-1")
	if err != nil {
		t.Fatalf("ReadAck: %v", err)
	}
	if got == nil {
		t.Fatal("ack not found after second write")
	}
	if got.MessageID != "msg-1" {
		t.Errorf("MessageID: %q", got.MessageID)
	}
}

// --- ListInbox excludes acked ---

func TestStore_ListInbox_ExcludesAcked(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env1 := &Envelope{SenderRank: RankSoldier, SenderIdentity: "soldier-1", ReceiverRank: RankCaptain, ReceiverID: "captain-1", Payload: "first"}
	env2 := &Envelope{SenderRank: RankSoldier, SenderIdentity: "soldier-1", ReceiverRank: RankCaptain, ReceiverID: "captain-1", Payload: "second"}
	if err := store.WriteEnvelope(env1); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteEnvelope(env2); err != nil {
		t.Fatal(err)
	}

	// Ack env1.
	ack := &ProcessingAck{
		MessageID: env1.MessageID, SenderRank: RankSoldier,
		SenderIdentity: "soldier-1", ReceiverRank: RankCaptain,
		ReceiverID: "captain-1", PayloadHash: env1.PayloadHash,
		ProcessedAt: time.Now().UnixNano(), Outcome: "done",
	}
	if err := store.WriteAck(ack); err != nil {
		t.Fatalf("WriteAck: %v", err)
	}

	pending, err := store.ListInbox("soldier-1")
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending (env2), got %d", len(pending))
	}
	if pending[0].MessageID != env2.MessageID {
		t.Errorf("expected env2, got %s", pending[0].MessageID)
	}
}

// --- v1 read compatibility ---

func TestStore_ReadEnvelope_V1Compat(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	// Write a v1-style envelope with extra mutable fields.
	v1Data := `{
		"schema_version": "munsu.mailbox-envelope/v1",
		"message_id": "v1-msg",
		"sender_rank": "soldier",
		"sender_identity": "soldier-1",
		"receiver_rank": "captain",
		"receiver_id": "captain-1",
		"receiver_home": "",
		"task_id": "task:1",
		"key": "",
		"state": "done",
		"payload": "done: legacy",
		"payload_hash": "` + PayloadHashHex("done: legacy") + `",
		"created_at": 1000,
		"delivery_status": "acked",
		"delivery_attempts": 1,
		"last_attempt_at": 1001,
		"acked_at": 1002
	}`
	inboxDir := filepath.Join(home, "state", InboxDir, "soldier-1")
	if err := os.MkdirAll(inboxDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, "v1-msg.json"), []byte(v1Data), 0644); err != nil {
		t.Fatal(err)
	}

	env, err := store.ReadEnvelope("soldier-1", "v1-msg")
	if err != nil {
		t.Fatalf("ReadEnvelope v1: %v", err)
	}
	if env == nil {
		t.Fatal("envelope not found")
	}
	if env.MessageID != "v1-msg" {
		t.Errorf("MessageID: %q", env.MessageID)
	}
	if env.SenderRank != RankSoldier {
		t.Errorf("SenderRank: %q", env.SenderRank)
	}
	if env.SenderIdentity != "soldier-1" {
		t.Errorf("SenderIdentity: %q", env.SenderIdentity)
	}
	if env.Payload != "done: legacy" {
		t.Errorf("Payload: %q", env.Payload)
	}
	if env.PayloadHash != PayloadHashHex("done: legacy") {
		t.Errorf("PayloadHash mismatch")
	}
	if env.CreatedAt != 1000 {
		t.Errorf("CreatedAt: %d", env.CreatedAt)
	}
	// v1 schema version is preserved on read (not dropped).
	if env.SchemaVersion != "munsu.mailbox-envelope/v1" {
		t.Errorf("SchemaVersion should match v1 on read: %q", env.SchemaVersion)
	}
}

func TestStore_ReadPending_V1Compat(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	v1Data := `{
		"schema_version": "munsu.mailbox-envelope/v1",
		"message_id": "v1-pending",
		"sender_rank": "soldier",
		"sender_identity": "soldier-1",
		"receiver_rank": "captain",
		"receiver_id": "captain-1",
		"payload": "done: legacy pending",
		"payload_hash": "` + PayloadHashHex("done: legacy pending") + `",
		"created_at": 2000,
		"delivery_status": "pending",
		"delivery_attempts": 0
	}`
	pendingDir := filepath.Join(home, "state", OutboxDir, "soldier-1")
	if err := os.MkdirAll(pendingDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pendingDir, "v1-pending.pending"), []byte(v1Data), 0644); err != nil {
		t.Fatal(err)
	}

	env, err := store.ReadPending("soldier-1", "v1-pending")
	if err != nil {
		t.Fatalf("ReadPending v1: %v", err)
	}
	if env == nil {
		t.Fatal("pending not found")
	}
	if env.MessageID != "v1-pending" {
		t.Errorf("MessageID: %q", env.MessageID)
	}
	if env.SenderIdentity != "soldier-1" {
		t.Errorf("SenderIdentity: %q", env.SenderIdentity)
	}
	if env.Payload != "done: legacy pending" {
		t.Errorf("Payload: %q", env.Payload)
	}
	if env.CreatedAt != 2000 {
		t.Errorf("CreatedAt: %d", env.CreatedAt)
	}
}

// --- Pending: round-trip write → read → remove ---

func TestStore_Pending_RoundTrip(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := &Envelope{
		MessageID:      "pending-1",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "done: pending test",
		PayloadHash:    PayloadHashHex("done: pending test"),
	}
	if err := store.WritePending(env); err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	got, err := store.ReadPending("soldier-1", "pending-1")
	if err != nil {
		t.Fatalf("ReadPending: %v", err)
	}
	if got == nil {
		t.Fatal("pending not found")
	}
	if got.Payload != "done: pending test" {
		t.Errorf("Payload: %q", got.Payload)
	}

	list, err := store.ListPending("soldier-1")
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(list))
	}

	// Write matching ack so RemovePendingAfterAck can validate.
	ack := &ProcessingAck{
		MessageID: "pending-1", SenderRank: RankSoldier,
		SenderIdentity: "soldier-1", ReceiverRank: RankCaptain,
		ReceiverID: "captain-1", PayloadHash: PayloadHashHex("done: pending test"),
		ProcessedAt: time.Now().UnixNano(), Outcome: "done",
	}
	store.WriteAck(ack)

	if err := store.RemovePendingAfterAck("soldier-1", "pending-1", ack); err != nil {
		t.Fatalf("RemovePendingAfterAck: %v", err)
	}
	got, _ = store.ReadPending("soldier-1", "pending-1")
	if got != nil {
		t.Error("pending should be nil after remove")
	}
}

// --- Full flow: envelope → pending → ack → validate → remove pending ---

func TestStore_FullFlow(t *testing.T) {
	receiverHome := t.TempDir()
	senderHome := t.TempDir()
	receiverStore := NewStore(receiverHome)
	senderStore := NewStore(senderHome)

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "task:1",
		Key:            "report-key",
		Payload:        "done: full flow",
	}

	// Write envelope to receiver's inbox.
	if err := receiverStore.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	// Write pending on sender side.
	if err := senderStore.WritePending(env); err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	// Verify envelope can be read.
	got, err := receiverStore.ReadEnvelope("soldier-1", env.MessageID)
	if err != nil || got == nil {
		t.Fatal("envelope not found after WriteEnvelope")
	}

	// Write ack on receiver side.
	ack := &ProcessingAck{
		MessageID:      env.MessageID,
		SenderRank:     env.SenderRank,
		SenderIdentity: env.SenderIdentity,
		ReceiverRank:   env.ReceiverRank,
		ReceiverID:     env.ReceiverID,
		TaskID:         env.TaskID,
		Key:            env.Key,
		PayloadHash:    env.PayloadHash,
		ProcessedAt:    time.Now().UnixNano(),
		Outcome:        "done",
	}
	if err := receiverStore.WriteAck(ack); err != nil {
		t.Fatalf("WriteAck: %v", err)
	}

	// Validate ack on sender side.
	readAck, err := receiverStore.ReadAck("soldier-1", env.MessageID)
	if err != nil {
		t.Fatalf("ReadAck: %v", err)
	}
	if readAck == nil {
		t.Fatal("ack not found")
	}
	if err := ValidateAck(env, readAck); err != nil {
		t.Fatalf("ValidateAck: %v", err)
	}

	// Remove pending after validated ack.
	if err := senderStore.RemovePendingAfterAck("soldier-1", env.MessageID, readAck); err != nil {
		t.Fatalf("RemovePendingAfterAck: %v", err)
	}
	pending, _ := senderStore.ListPending("soldier-1")
	if len(pending) != 0 {
		t.Error("pending should be empty after validated ack and remove")
	}
}

// --- ListAllPending across identities ---

func TestStore_ListAllPending(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	envs := []*Envelope{
		{MessageID: "a-1", SenderIdentity: "alpha", Payload: "a1"},
		{MessageID: "a-2", SenderIdentity: "alpha", Payload: "a2"},
		{MessageID: "b-1", SenderIdentity: "beta", Payload: "b1"},
	}
	for _, e := range envs {
		e.SenderRank = RankSoldier
		e.ReceiverRank = RankCaptain
		e.ReceiverID = "captain-1"
		if err := store.WritePending(e); err != nil {
			t.Fatal(err)
		}
	}

	all, err := store.ListAllPending()
	if err != nil {
		t.Fatalf("ListAllPending: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 pending total, got %d", len(all))
	}
}

// --- Recovery ---

func TestRecoverInbox_AlreadyAcked(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := &Envelope{
		SenderRank: RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: RankCaptain, ReceiverID: "captain-1",
		MessageID: "test-id", Payload: "done: test",
	}
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatal(err)
	}
	ack := &ProcessingAck{
		MessageID: env.MessageID, SenderRank: RankSoldier,
		SenderIdentity: "soldier-1", ReceiverRank: RankCaptain,
		ReceiverID: "captain-1", PayloadHash: env.PayloadHash,
		ProcessedAt: time.Now().UnixNano(), Outcome: "done",
	}
	if err := store.WriteAck(ack); err != nil {
		t.Fatal(err)
	}

	attempt := RecoverInboxWithSender(&deliverySender{}, home, env)
	if !attempt.AlreadyAck {
		t.Error("expected AlreadyAck for acked envelope")
	}
	if attempt.Delivered {
		t.Error("should not deliver already-acked envelope")
	}
}

func TestRecoverInbox_SkipOnMarker(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := &Envelope{
		SenderRank: RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: RankCaptain, ReceiverID: "captain-1",
		MessageID: "test-id-skip", Payload: "done: test",
	}
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatal(err)
	}

	markerPath := RecoveryMarkerPath(home, env.MessageID)
	os.WriteFile(markerPath, []byte("recovered\n"), 0644)

	attempt := RecoverInboxWithSender(&deliverySender{}, home, env)
	if !attempt.Skipped {
		t.Error("expected Skipped when marker exists")
	}
}

func TestRecoverInbox_SkipsUplinkEnvelope(t *testing.T) {
	for _, env := range []*Envelope{
		{MessageID: "soldier-up", Kind: "uplink-report", SenderRank: RankSoldier, ReceiverRank: RankCaptain},
		{MessageID: "captain-up", Kind: "uplink-report", SenderRank: RankCaptain, ReceiverRank: RankGeneral},
	} {
		attempt := RecoverInboxWithSender(&deliverySender{}, t.TempDir(), env)
		if !attempt.Skipped || attempt.Delivered {
			t.Fatalf("attempt=%+v, want skipped uplink", attempt)
		}
	}
}

func TestRecoverAllInboxes_EmptyDir(t *testing.T) {
	attempts, err := RecoverAllInboxesWithSender(&deliverySender{}, t.TempDir())
	if err != nil {
		t.Fatalf("RecoverAllInboxes: %v", err)
	}
	if len(attempts) != 0 {
		t.Errorf("expected 0 attempts, got %d", len(attempts))
	}
}

func TestCleanRecoveryMarkers(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	os.WriteFile(filepath.Join(stateDir, ".recovered-msg1"), []byte("ok"), 0644)
	os.WriteFile(filepath.Join(stateDir, ".recovered-msg2"), []byte("ok"), 0644)

	if err := CleanRecoveryMarkers(home, 0); err != nil {
		t.Fatalf("CleanRecoveryMarkers: %v", err)
	}
	entries, _ := os.ReadDir(stateDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".recovered-") {
			t.Errorf("marker %s should have been cleaned", e.Name())
		}
	}
}

// --- Legacy standalone helpers delegation ---

func TestLegacyGetInboxEnvelope(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := &Envelope{
		SenderRank: RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: RankCaptain, ReceiverID: "captain-1",
		Payload: "legacy test",
	}
	store.WriteEnvelope(env)

	got, err := GetInboxEnvelope(home, "soldier-1", env.MessageID)
	if err != nil {
		t.Fatalf("GetInboxEnvelope: %v", err)
	}
	if got == nil || got.Payload != "legacy test" {
		t.Error("GetInboxEnvelope delegation failed")
	}
}

func TestLegacySaveSenderPending(t *testing.T) {
	home := t.TempDir()
	env := &Envelope{
		MessageID: "legacy-pending", SenderRank: RankSoldier,
		SenderIdentity: "soldier-1", ReceiverRank: RankCaptain,
		ReceiverID: "captain-1", Payload: "test",
		PayloadHash: PayloadHashHex("test"),
	}
	path, err := SaveSenderPending(home, env)
	if err != nil {
		t.Fatalf("SaveSenderPending: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("pending file not created: %v", err)
	}
}

func TestLegacyListSenderPending(t *testing.T) {
	home := t.TempDir()
	env := &Envelope{
		MessageID: "list-test", SenderRank: RankSoldier,
		SenderIdentity: "soldier-1", ReceiverRank: RankCaptain,
		ReceiverID: "captain-1", Payload: "test",
		PayloadHash: PayloadHashHex("test"),
	}
	NewStore(home).WritePending(env)

	pending, err := ListSenderPending(home, "soldier-1")
	if err != nil {
		t.Fatalf("ListSenderPending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}
}

func TestLegacyRemoveSenderPending(t *testing.T) {
	home := t.TempDir()
	env := &Envelope{
		MessageID: "remove-test", SenderRank: RankSoldier,
		SenderIdentity: "soldier-1", ReceiverRank: RankCaptain,
		ReceiverID: "captain-1", Payload: "test",
		PayloadHash: PayloadHashHex("test"),
	}
	store := NewStore(home)
	store.WritePending(env)
	// Write matching ack so RemoveSenderPending (which delegates to
	// RemovePendingAfterAck) can validate.
	ack := &ProcessingAck{
		MessageID: "remove-test", SenderRank: RankSoldier,
		SenderIdentity: "soldier-1", ReceiverRank: RankCaptain,
		ReceiverID: "captain-1", PayloadHash: PayloadHashHex("test"),
		ProcessedAt: time.Now().UnixNano(), Outcome: "done",
	}
	store.WriteAck(ack)
	if err := RemoveSenderPending(home, "soldier-1", "remove-test"); err != nil {
		t.Fatalf("RemoveSenderPending: %v", err)
	}
	got, _ := store.ReadPending("soldier-1", "remove-test")
	if got != nil {
		t.Error("pending should be nil after remove")
	}
}

func TestLegacyNewEnvelope(t *testing.T) {
	home := t.TempDir()
	env := &Envelope{
		SenderRank: RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: RankCaptain, ReceiverID: "captain-1",
		Payload: "legacy new",
	}
	if err := NewEnvelope(home, env); err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	got, _ := NewStore(home).ReadEnvelope("soldier-1", env.MessageID)
	if got == nil || got.Payload != "legacy new" {
		t.Error("NewEnvelope delegation failed")
	}
}

func TestLegacyListPendingInbox(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)
	store.WriteEnvelope(&Envelope{
		SenderRank: RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: RankCaptain, ReceiverID: "captain-1",
		Payload: "inbox test",
	})
	pending, err := ListPendingInbox(home, "soldier-1")
	if err != nil {
		t.Fatalf("ListPendingInbox: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending inbox, got %d", len(pending))
	}
}

func TestLegacyIsAcked(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	env := &Envelope{
		SenderRank: RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: RankCaptain, ReceiverID: "captain-1",
		Payload: "ack test",
	}
	store.WriteEnvelope(env)

	if IsAcked(home, "soldier-1", env.MessageID) {
		t.Error("should not be acked before WriteAck")
	}

	store.WriteAck(&ProcessingAck{
		MessageID: env.MessageID, SenderRank: RankSoldier,
		SenderIdentity: "soldier-1", ReceiverRank: RankCaptain,
		ReceiverID: "captain-1", PayloadHash: env.PayloadHash,
		ProcessedAt: time.Now().UnixNano(), Outcome: "done",
	})

	if !IsAcked(home, "soldier-1", env.MessageID) {
		t.Error("should be acked after WriteAck")
	}
}
