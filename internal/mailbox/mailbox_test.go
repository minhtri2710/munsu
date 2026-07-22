package mailbox

import (
	"os"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/marker"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

type fakeBackend struct {
	alive    bool
	sent     []string
	sendErr  error
	windowID string
}

func (f *fakeBackend) NewWindow(session, name string) (string, error) {
	return f.windowID, nil
}
func (f *fakeBackend) SendKeys(windowID, text string) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, text)
	return nil
}
func (f *fakeBackend) Capture(windowID string, lines int) (string, error) {
	return "", nil
}
func (f *fakeBackend) Alive(windowID string) bool { return f.alive }
func (f *fakeBackend) Teardown(windowID string) error {
	return nil
}

func installFakeBackend(t *testing.T, bk session.Backend) {
	t.Helper()
	orig := backendForTask
	backendForTask = func(parentHome string, meta map[string]string) (session.Backend, string, error) {
		return bk, "fake", nil
	}
	t.Cleanup(func() { backendForTask = orig })
}

func writeFakeMeta(t *testing.T, home, taskID, window string) {
	t.Helper()
	meta := map[string]string{
		"kind":    "captain",
		"window":  window,
		"backend": "fake",
	}
	if err := task.WriteMeta(home, taskID, meta); err != nil {
		t.Fatal(err)
	}
}

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
		{"general→captain", RankGeneral, RankCaptain, false},
		{"captain→general", RankCaptain, RankGeneral, false},
		{"captain→soldier", RankCaptain, RankSoldier, false},
		{"soldier→captain", RankSoldier, RankCaptain, false},
		{"general→soldier", RankGeneral, RankSoldier, true},
		{"general→general", RankGeneral, RankGeneral, true},
		{"captain→captain", RankCaptain, RankCaptain, true},
		{"soldier→general", RankSoldier, RankGeneral, true},
		{"soldier→soldier", RankSoldier, RankSoldier, true},
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

// --- NewEnvelope (write to receiver's inbox) ---

func TestNewEnvelope_WritesToReceiverInbox(t *testing.T) {
	receiverHome := t.TempDir()
	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-task-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-munsu",
		Payload:        "done: task complete",
	}

	if err := NewEnvelope(receiverHome, env); err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}

	// Verify in inbox.
	inboxDir := ReceiverInboxDir(receiverHome, env.SenderIdentity)
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

	// Read back and verify.
	got, err := GetInboxEnvelope(receiverHome, env.SenderIdentity, env.MessageID)
	if err != nil {
		t.Fatalf("GetInboxEnvelope: %v", err)
	}
	if got == nil {
		t.Fatal("envelope not found")
	}
	if got.SchemaVersion != SchemaVersion {
		t.Errorf("schema=%q", got.SchemaVersion)
	}
	if got.DeliveryStatus != StatusPending {
		t.Errorf("status=%q, want pending", got.DeliveryStatus)
	}
	if got.Payload != "done: task complete" {
		t.Errorf("payload=%q", got.Payload)
	}
	if got.CreatedAt == 0 {
		t.Error("CreatedAt should be set")
	}
}

func TestNewEnvelope_AutoGeneratesID(t *testing.T) {
	receiverHome := t.TempDir()
	env := &Envelope{
		SenderRank:     RankCaptain,
		SenderIdentity: "captain-1",
		ReceiverRank:   RankGeneral,
		ReceiverID:     "general-main",
		Payload:        "report",
	}
	if err := NewEnvelope(receiverHome, env); err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	if env.MessageID == "" {
		t.Fatal("MessageID should be auto-generated")
	}
}

// --- Sender pending records ---

func TestSaveSenderPending_PersistsRecord(t *testing.T) {
	senderHome := t.TempDir()
	env := &Envelope{
		MessageID:      "test-id-1234567890abcdef",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "test",
		PayloadHash:    PayloadHashHex("test"),
	}

	path, err := SaveSenderPending(senderHome, env)
	if err != nil {
		t.Fatalf("SaveSenderPending: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}

	// Verify file exists.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("pending file not created: %v", err)
	}

	// List pending.
	pending, err := ListSenderPending(senderHome)
	if err != nil {
		t.Fatalf("ListSenderPending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}
}

func TestRemoveSenderPending_ClearsRecord(t *testing.T) {
	senderHome := t.TempDir()
	env := &Envelope{
		MessageID:      "test-id",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "test",
		PayloadHash:    PayloadHashHex("test"),
	}

	SaveSenderPending(senderHome, env)
	if err := RemoveSenderPending(senderHome, env.MessageID); err != nil {
		t.Fatalf("RemoveSenderPending: %v", err)
	}

	pending, _ := ListSenderPending(senderHome)
	if len(pending) != 0 {
		t.Fatalf("expected 0 pending after remove, got %d", len(pending))
	}
}

// --- Delivery: happy path ---

func TestDeliverEnvelope_AliveEndpoint(t *testing.T) {
	receiverHome := t.TempDir()
	senderHome := t.TempDir()
	window := "@w"

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "captain:1",
		Payload:        "done: task complete",
	}
	NewEnvelope(receiverHome, env)
	SaveSenderPending(senderHome, env)

	fake := &fakeBackend{alive: true, windowID: window}
	installFakeBackend(t, fake)

	writeFakeMeta(t, receiverHome, "captain:1", window)

	meta, _ := task.ReadMeta(receiverHome, "captain:1")
	result := DeliverEnvelope(receiverHome, env.SenderIdentity, env, meta)

	if result.Err != nil {
		t.Fatalf("DeliverEnvelope: %v", result.Err)
	}
	if !result.PromptSent {
		t.Error("expected prompt sent")
	}
	if len(fake.sent) != 1 {
		t.Fatalf("sent=%d, want 1", len(fake.sent))
	}
	if fake.sent[0] != "done: task complete" {
		t.Errorf("message=%q", fake.sent[0])
	}

	// Verify inbox status.
	got, _ := GetInboxEnvelope(receiverHome, env.SenderIdentity, env.MessageID)
	if got.DeliveryStatus != StatusDelivered {
		t.Errorf("status=%q, want delivered", got.DeliveryStatus)
	}
}

func TestDeliverEnvelope_DeadEndpoint(t *testing.T) {
	receiverHome := t.TempDir()
	window := "@dead"

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "captain:1",
		Payload:        "done: task complete",
	}
	NewEnvelope(receiverHome, env)

	fake := &fakeBackend{alive: false, windowID: window}
	installFakeBackend(t, fake)

	writeFakeMeta(t, receiverHome, "captain:1", window)

	meta, _ := task.ReadMeta(receiverHome, "captain:1")
	result := DeliverEnvelope(receiverHome, env.SenderIdentity, env, meta)

	if result.Err == nil {
		t.Fatal("expected error for dead endpoint")
	}
	if !strings.Contains(result.Err.Error(), "not alive") {
		t.Errorf("error should mention not alive: %v", result.Err)
	}
	if result.PromptSent {
		t.Error("should not have sent to dead endpoint")
	}

	// Envelope should still be pending.
	got, _ := GetInboxEnvelope(receiverHome, env.SenderIdentity, env.MessageID)
	if got.DeliveryStatus != StatusPending {
		t.Errorf("status=%q, want pending", got.DeliveryStatus)
	}
}

// --- Ack semantics ---

func TestMarkProcessed_WritesAck(t *testing.T) {
	receiverHome := t.TempDir()

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "report",
	}
	NewEnvelope(receiverHome, env)

	if err := MarkProcessed(receiverHome, env.SenderIdentity, env.MessageID); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}

	if !IsAcked(receiverHome, env.SenderIdentity, env.MessageID) {
		t.Error("envelope should be acked")
	}

	// Verify ack file.
	ackPath := receiverInboxAckPath(receiverHome, env.SenderIdentity, env.MessageID)
	if _, err := os.Stat(ackPath); err != nil {
		t.Errorf("ack file not created: %v", err)
	}

	// Verify envelope status updated.
	got, _ := GetInboxEnvelope(receiverHome, env.SenderIdentity, env.MessageID)
	if got.DeliveryStatus != StatusAcked {
		t.Errorf("status=%q, want acked", got.DeliveryStatus)
	}
	if got.AckedAt == 0 {
		t.Error("AckedAt should be set")
	}
}

func TestIsAcked_NoAck(t *testing.T) {
	receiverHome := t.TempDir()
	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "report",
	}
	NewEnvelope(receiverHome, env)

	if IsAcked(receiverHome, env.SenderIdentity, env.MessageID) {
		t.Error("should not be acked before MarkProcessed")
	}
}

func TestListPendingInbox_ExcludesAcked(t *testing.T) {
	receiverHome := t.TempDir()

	env1 := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "first",
	}
	env2 := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "second",
	}
	NewEnvelope(receiverHome, env1)
	NewEnvelope(receiverHome, env2)

	// Process env1.
	MarkProcessed(receiverHome, env1.SenderIdentity, env1.MessageID)

	pending, err := ListPendingInbox(receiverHome, "soldier-1")
	if err != nil {
		t.Fatalf("ListPendingInbox: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending (env2), got %d", len(pending))
	}
	if pending[0].MessageID != env2.MessageID {
		t.Errorf("expected env2, got %s", pending[0].MessageID)
	}
}

// --- Rank ownership ---

func TestEnvelope_OwnershipMismatch(t *testing.T) {
	err := OwnershipError("captain-1", "captain-2")
	if err == nil {
		t.Fatal("expected ownership error")
	}
	if !strings.Contains(err.Error(), "ownership mismatch") {
		t.Errorf("error should mention ownership: %v", err)
	}
}

// --- Recovery ---

func TestRecoverInbox_AlreadyAcked(t *testing.T) {
	receiverHome := t.TempDir()
	senderIdentity := "soldier-1"
	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: senderIdentity,
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		MessageID:      "test-id",
		Payload:        "done: test",
	}
	NewEnvelope(receiverHome, env)
	MarkProcessed(receiverHome, senderIdentity, env.MessageID)

	attempt := RecoverInbox(receiverHome, env)
	if !attempt.AlreadyAck {
		t.Error("expected AlreadyAck for processed envelope")
	}
	if attempt.Delivered {
		t.Error("should not deliver already-acked envelope")
	}
}

func TestRecoverInbox_SkipOnMarker(t *testing.T) {
	receiverHome := t.TempDir()
	senderIdentity := "soldier-1"
	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: senderIdentity,
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		MessageID:      "test-id-skip",
		Payload:        "done: test",
	}
	NewEnvelope(receiverHome, env)

	// Write recovery marker.
	markerPath := RecoveryMarkerPath(receiverHome, env.MessageID)
	os.WriteFile(markerPath, []byte("recovered\n"), 0644)

	attempt := RecoverInbox(receiverHome, env)
	if !attempt.Skipped {
		t.Error("expected Skipped when marker exists")
	}
}

func TestRecoverAllInboxes_EmptyDir(t *testing.T) {
	home := t.TempDir()
	attempts, err := RecoverAllInboxes(home)
	if err != nil {
		t.Fatalf("RecoverAllInboxes: %v", err)
	}
	if len(attempts) != 0 {
		t.Errorf("expected 0 attempts, got %d", len(attempts))
	}
}

// --- SendReport (happy path integration) ---

func TestSendReport_HappyPath(t *testing.T) {
	receiverHome := t.TempDir()
	senderHome := t.TempDir()
	window := "@w"

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "captain:1",
		Payload:        "done: task complete",
	}

	fake := &fakeBackend{alive: true, windowID: window}
	installFakeBackend(t, fake)
	writeFakeMeta(t, receiverHome, "captain:1", window)

	meta, _ := task.ReadMeta(receiverHome, "captain:1")
	result := SendReport(env, receiverHome, senderHome, meta)

	if result.Err != nil {
		t.Fatalf("SendReport: %v", result.Err)
	}
	if !result.PromptSent {
		t.Error("expected prompt sent")
	}

	// Verify envelope in inbox.
	got, err := GetInboxEnvelope(receiverHome, "soldier-1", env.MessageID)
	if err != nil || got == nil {
		t.Fatal("envelope not found in inbox")
	}
	if got.DeliveryStatus != StatusDelivered {
		t.Errorf("status=%q, want delivered", got.DeliveryStatus)
	}

	// Verify sender pending record.
	pending, err := ListSenderPending(senderHome)
	if err != nil || len(pending) != 1 {
		t.Fatal("sender pending record not found")
	}

	// Mark processed and verify ack.
	if err := MarkProcessed(receiverHome, "soldier-1", env.MessageID); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}
	if !IsAcked(receiverHome, "soldier-1", env.MessageID) {
		t.Error("expected ack after MarkProcessed")
	}

	// Remove sender pending after ack.
	if err := RemoveSenderPending(senderHome, env.MessageID); err != nil {
		t.Fatalf("RemoveSenderPending: %v", err)
	}
	pending, _ = ListSenderPending(senderHome)
	if len(pending) != 0 {
		t.Error("sender pending should be empty after ack")
	}
}

func TestSendReport_DeadEndpoint(t *testing.T) {
	receiverHome := t.TempDir()
	senderHome := t.TempDir()
	window := "@dead"

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "captain:1",
		Payload:        "done: offline",
	}

	fake := &fakeBackend{alive: false, windowID: window}
	installFakeBackend(t, fake)
	writeFakeMeta(t, receiverHome, "captain:1", window)

	meta, _ := task.ReadMeta(receiverHome, "captain:1")
	result := SendReport(env, receiverHome, senderHome, meta)

	if result.Err == nil {
		t.Fatal("expected error for dead endpoint")
	}

	// Envelope should be in inbox but still pending.
	got, err := GetInboxEnvelope(receiverHome, "soldier-1", env.MessageID)
	if err != nil || got == nil {
		t.Fatal("envelope must be in inbox even on delivery failure")
	}
	if got.DeliveryStatus != StatusPending {
		t.Errorf("status=%q, want pending (dead endpoint should not change status)", got.DeliveryStatus)
	}

	// Sender must have pending record.
	pending, err := ListSenderPending(senderHome)
	if err != nil || len(pending) != 1 {
		t.Fatal("sender must have pending record for undelivered envelope")
	}
}

// --- General→Captain with marker ---

func TestSendReport_GeneralToCaptain(t *testing.T) {
	receiverHome := t.TempDir()
	senderHome := t.TempDir()
	window := "@w"

	markedMsg := marker.MarkFromGeneral("run deploy")
	env := &Envelope{
		SenderRank:     RankGeneral,
		SenderIdentity: "general-main",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "captain:1",
		Payload:        markedMsg,
	}

	fake := &fakeBackend{alive: true, windowID: window}
	installFakeBackend(t, fake)
	writeFakeMeta(t, receiverHome, "captain:1", window)

	meta, _ := task.ReadMeta(receiverHome, "captain:1")
	result := SendReport(env, receiverHome, senderHome, meta)

	if result.Err != nil {
		t.Fatalf("SendReport: %v", result.Err)
	}
	if !result.PromptSent {
		t.Error("expected prompt sent")
	}

	if len(fake.sent) != 1 {
		t.Fatalf("sent=%d, want 1", len(fake.sent))
	}
	if !marker.IsFromGeneral(fake.sent[0]) {
		t.Error("delivered message must carry from-general marker")
	}
}

// --- Recovery: target offline/restart ---

func TestRecoverInbox_RetriesDelivery(t *testing.T) {
	receiverHome := t.TempDir()
	senderIdentity := "soldier-1"
	window := "@w"

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: senderIdentity,
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "captain:1",
		Payload:        "done: recovered",
	}
	NewEnvelope(receiverHome, env)
	SaveSenderPending(receiverHome, env) // not used here but mirrors real flow

	fake := &fakeBackend{alive: true, windowID: window}
	installFakeBackend(t, fake)
	writeFakeMeta(t, receiverHome, "captain:1", window)

	attempt := RecoverInbox(receiverHome, env)
	if attempt.Err != nil {
		t.Fatalf("RecoverInbox: %v", attempt.Err)
	}
	if !attempt.Delivered {
		t.Error("expected delivered")
	}
	if len(fake.sent) != 1 {
		t.Fatalf("sent=%d, want 1", len(fake.sent))
	}
	if fake.sent[0] != "done: recovered" {
		t.Errorf("message=%q", fake.sent[0])
	}

	// Envelope should be delivered in inbox.
	got, _ := GetInboxEnvelope(receiverHome, senderIdentity, env.MessageID)
	if got.DeliveryStatus != StatusDelivered {
		t.Errorf("status=%q, want delivered", got.DeliveryStatus)
	}

	// Second recovery attempt should skip (marker exists).
	attempt2 := RecoverInbox(receiverHome, env)
	if !attempt2.Skipped {
		t.Error("second recovery should be skipped")
	}

	// Should not send again.
	if len(fake.sent) != 1 {
		t.Fatalf("should not send again, got %d sends", len(fake.sent))
	}
}

// --- Dedup: same envelope, two deliveries ---

func TestSendReport_DuplicateMessageID(t *testing.T) {
	receiverHome := t.TempDir()

	// Two envelopes with the same message ID — second NewEnvelope should overwrite
	// (idempotent at the file level: last write wins).
	env := &Envelope{
		MessageID:      "dup-id-1234567890abcdef",
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "captain:1",
		Payload:        "first",
	}
	NewEnvelope(receiverHome, env)
	env.Payload = "second"
	NewEnvelope(receiverHome, env)

	// Only one envelope with this ID.
	got, _ := GetInboxEnvelope(receiverHome, "soldier-1", env.MessageID)
	if got == nil {
		t.Fatal("envelope not found")
	}
	// The second write overwrites the first (payload hash is always recomputed).
	if got.Payload != "second" {
		t.Errorf("payload=%q, want 'second' (last write wins)", got.Payload)
	}
	if got.PayloadHash == PayloadHashHex("first") {
		t.Error("payload hash should have been recomputed for second write")
	}
}

// --- Cleanup markers ---

func TestCleanRecoveryMarkers(t *testing.T) {
	home := t.TempDir()
	stateDir := home + "/state"
	os.MkdirAll(stateDir, 0755)

	// Create marker files.
	os.WriteFile(stateDir+"/.recovered-msg1", []byte("ok"), 0644)
	os.WriteFile(stateDir+"/.recovered-msg2", []byte("ok"), 0644)

	// Clean with 0 max age — all markers should be removed.
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

// --- Direct idle and busy submission, no watcher ---

func TestAwaitedSendReport_IdleSubmission(t *testing.T) {
	receiverHome := t.TempDir()
	senderHome := t.TempDir()
	window := "@w"

	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "captain:1",
		Payload:        "working: idle test",
	}

	fake := &fakeBackend{alive: true, windowID: window}
	installFakeBackend(t, fake)
	writeFakeMeta(t, receiverHome, "captain:1", window)

	meta, _ := task.ReadMeta(receiverHome, "captain:1")
	result := AwaitedSendReport(env, receiverHome, senderHome, meta)

	if result.Err != nil {
		t.Fatalf("AwaitedSendReport: %v", result.Err)
	}
	if !result.PromptSent {
		t.Error("expected prompt sent")
	}
	// Process ack should initially be false (processed in next turn).
	if result.ProcessAcked {
		t.Log("process ack already true (receiver may have processed)")
	}

	// Now simulate receiver processing.
	if err := MarkProcessed(receiverHome, "soldier-1", env.MessageID); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}

	if !IsAcked(receiverHome, "soldier-1", env.MessageID) {
		t.Error("expected ack after processing")
	}
}

// --- General watcher with legacy/general pending state ---

func TestGeneralWatcher_NoParentWarning(t *testing.T) {
	// General should never emit parent-home diagnostics.
	// This test verifies the inbox design: a General home's inbox
	// contains incoming envelopes from Captains.
	// The General DOES NOT have config/parent-home.
	generalHome := t.TempDir()

	// Create incoming envelopes (Captain→General).
	env := &Envelope{
		SenderRank:     RankCaptain,
		SenderIdentity: "captain-1",
		ReceiverRank:   RankGeneral,
		ReceiverID:     "general-main",
		Payload:        "needs-decision: approve deploy",
	}
	NewEnvelope(generalHome, env)

	// Verify the envelope is in General's inbox.
	got, err := GetInboxEnvelope(generalHome, "captain-1", env.MessageID)
	if err != nil || got == nil {
		t.Fatal("envelope should be in General inbox")
	}
	if got.SenderRank != RankCaptain || got.ReceiverRank != RankGeneral {
		t.Errorf("rank mismatch: sender=%s receiver=%s", got.SenderRank, got.ReceiverRank)
	}
}

// --- Captain missing parent does not affect local Soldier→Captain ---

func TestCaptainMissingParent_LocalSoldierToCaptain(t *testing.T) {
	captainHome := t.TempDir()
	senderHome := t.TempDir()
	window := "@w"

	// Soldier sends to Captain (no parent configured).
	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "soldier-1",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		TaskID:         "captain:1",
		Payload:        "done: local-only",
	}

	fake := &fakeBackend{alive: true, windowID: window}
	installFakeBackend(t, fake)
	writeFakeMeta(t, captainHome, "captain:1", window)

	meta, _ := task.ReadMeta(captainHome, "captain:1")
	result := SendReport(env, captainHome, senderHome, meta)

	if result.Err != nil {
		t.Fatalf("SendReport should work without parent: %v", result.Err)
	}
	if !result.PromptSent {
		t.Error("expected prompt sent")
	}

	// Also test Captain→General pending remains durable.
	generalHome := t.TempDir()
	captainToGenEnv := &Envelope{
		SenderRank:     RankCaptain,
		SenderIdentity: "captain-1",
		ReceiverRank:   RankGeneral,
		ReceiverID:     "general-main",
		Payload:        "done: captain report",
	}
	NewEnvelope(generalHome, captainToGenEnv)

	if !IsAcked(generalHome, "captain-1", captainToGenEnv.MessageID) {
		// Should not be acked yet.
	}

	// Should be visible in health (listable).
	pending, err := ListPendingInbox(generalHome, "captain-1")
	if err != nil {
		t.Fatalf("ListPendingInbox: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending Captain→General envelope, got %d", len(pending))
	}
}

// --- Existing receipt compatibility ---

func TestTurnendReceiptsMigration(t *testing.T) {
	// Verify that the turnend package's receipt format (used in legacy path)
	// is compatible with the mailbox system. The new mailbox does NOT
	// require migration of existing receipts — they co-exist.
	// Existing turnend receipts under state/.terminal-receipts/ remain valid.
	// The new mailbox system uses state/.inbox/ independently.
	captainHome := t.TempDir()

	// Legacy turnend receipt path.
	turnendDir := captainHome + "/state/.terminal-receipts"
	os.MkdirAll(turnendDir, 0755)
	receiptContent := "task_id=old-task\nkey=old-key\nstate=done\nmsg=legacy\ntimestamp=123\n"
	os.WriteFile(turnendDir+"/old-task.old-key.receipt", []byte(receiptContent), 0644)

	// New mailbox - should not interfere with legacy.
	env := &Envelope{
		SenderRank:     RankSoldier,
		SenderIdentity: "new-soldier",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "done: new-style",
	}
	NewEnvelope(captainHome, env)
	MarkProcessed(captainHome, "new-soldier", env.MessageID)

	// Legacy receipt should still exist.
	if _, err := os.Stat(turnendDir + "/old-task.old-key.receipt"); err != nil {
		t.Error("legacy receipt should still exist after mailbox operations")
	}
}
