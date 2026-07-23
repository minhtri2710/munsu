package mailbox

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

	// Removing alpha's pending should not affect beta's.
	if err := store.RemovePending("soldier-alpha", "msg-1"); err != nil {
		t.Fatalf("RemovePending alpha: %v", err)
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

// --- Idempotent ack ---

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
		Outcome: "done",
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

	if err := store.RemovePending("soldier-1", "pending-1"); err != nil {
		t.Fatalf("RemovePending: %v", err)
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
	if err := senderStore.RemovePending("soldier-1", env.MessageID); err != nil {
		t.Fatalf("RemovePending: %v", err)
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
		Outcome: "done",
	}
	if err := store.WriteAck(ack); err != nil {
		t.Fatal(err)
	}

	attempt := RecoverInbox(home, env)
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

	attempt := RecoverInbox(home, env)
	if !attempt.Skipped {
		t.Error("expected Skipped when marker exists")
	}
}

func TestRecoverAllInboxes_EmptyDir(t *testing.T) {
	attempts, err := RecoverAllInboxes(t.TempDir())
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
	NewStore(home).WritePending(env)
	if err := RemoveSenderPending(home, "soldier-1", "remove-test"); err != nil {
		t.Fatalf("RemoveSenderPending: %v", err)
	}
	got, _ := NewStore(home).ReadPending("soldier-1", "remove-test")
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
		Outcome: "done",
	})

	if !IsAcked(home, "soldier-1", env.MessageID) {
		t.Error("should be acked after WriteAck")
	}
}
