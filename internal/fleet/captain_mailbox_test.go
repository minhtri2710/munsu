package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
)

// --- Test helpers ---

type captainTestMailboxSender struct {
	acknowledged bool
	lastPayload  string
}

func (captainTestMailboxSender) Alive(string, map[string]string) (bool, error) { return true, nil }
func (s *captainTestMailboxSender) Send(_ string, _ map[string]string, payload string) home.BoundSendResult {
	s.lastPayload = payload
	return home.BoundSendResult{Status: "submitted", Acknowledged: s.acknowledged}
}

// setupTestHomes creates a parent (General) home and a captain home with
// valid provenance and task meta.
func setupTestHomes(t *testing.T) (parentHome, captainHome, captainID string) {
	t.Helper()

	parentHome = filepath.Join(t.TempDir(), "general-main")
	if err := os.MkdirAll(parentHome, 0755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if _, err := home.Init(parentHome); err != nil {
		t.Fatalf("init parent home: %v", err)
	}

	captainID = "test-captain"
	captainHome = filepath.Join(t.TempDir(), captainID)
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatalf("mkdir captain: %v", err)
	}

	// Set up typed documents in parent home so ConfigPush works.
	// The Backend is an explicit fixture literal ("tmux"): ResolveProject
	// during Seed/ConfigPush fails closed on an empty backend identity.
	base := config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config: config.ProjectOverlay{
			SoldierHarness: "pi",
			Backend:        "tmux",
		},
	}
	if err := config.StoreFleetBase(parentHome, base); err != nil {
		t.Fatalf("StoreFleetBase: %v", err)
	}
	// Register captain with a project BEFORE seeding, so ConfigPush inside
	// SeedCaptain can resolve the project for the published snapshot.
	if err := Register(parentHome, captainID, captainHome, "", "test-project"); err != nil {
		t.Fatalf("pre-register: %v", err)
	}

	// Seed captain home.
	if err := Seed(captainID, captainHome, ""); err != nil {
		// Seed requires parentHome for charter; seed with explicit parent.
		if err := SeedCaptain(CaptainSeedOptions{ID: captainID, Home: captainHome, ParentHome: parentHome, Integration: fakeIntegrationPort{}}); err != nil {
			t.Fatalf("Seed: %v", err)
		}
	}

	// Register captain (idempotent; ensures registry entry exists).
	if err := Register(parentHome, captainID, captainHome, "", "test-project"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Write task meta.
	taskID := taskIDForCaptain(captainID)
	captainCanon, err := canonicalCaptainHome(captainHome)
	if err != nil {
		t.Fatalf("canonicalCaptainHome: %v", err)
	}
	meta := map[string]string{
		"kind":    "captain",
		"sm_id":   captainID,
		"home":    captainCanon,
		"window":  "test-window",
		"backend": "test",
	}
	if err := home.WriteMeta(parentHome, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	return parentHome, captainHome, captainID
}

// TestSendMailboxToCaptain_HappyPath verifies that a successful mailbox send:
// 1. Writes an envelope to the captain's inbox
// 2. Writes a pending record in the General's outbox
// 3. Sends a NotificationRef (not the raw line)
// 4. Returns acknowledged=true
// 5. Leaves pending intact (not removed)
func TestSendMailboxToCaptain_HappyPath(t *testing.T) {
	sender := &captainTestMailboxSender{acknowledged: true}
	parentHome, captainHome, captainID := setupTestHomes(t)

	sm := Info{ID: captainID, Home: captainHome}
	line := "report status"

	result := SendMailboxToCaptain(sm, parentHome, line, sender)
	if result.Err != nil {
		t.Fatalf("SendMailboxToCaptain: %v", result.Err)
	}
	if !result.Acknowledged {
		t.Fatal("expected acknowledged=true")
	}
	if result.MessageID == "" {
		t.Fatal("expected non-empty MessageID")
	}

	// Verify notification text was NotificationRef, not the raw line.
	if sender.lastPayload == "" {
		t.Fatal("no notification text sent")
	}
	if strings.Contains(sender.lastPayload, line) {
		t.Error("notification text must NOT contain the raw line (payload)")
	}
	var ref home.NotificationRef
	if err := json.Unmarshal([]byte(sender.lastPayload), &ref); err != nil {
		t.Fatalf("notification text must be valid NotificationRef JSON: %v", err)
	}
	if ref.MessageID != result.MessageID {
		t.Errorf("ref MessageID=%q, want %q", ref.MessageID, result.MessageID)
	}

	// Verify envelope was written to captain inbox.
	captainStore := home.NewStore(captainHome)
	env, err := captainStore.ReadEnvelope(ref.SenderIdentity, ref.MessageID)
	if err != nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	if env == nil {
		t.Fatal("envelope not found in captain inbox")
	}
	if env.Payload != home.MarkFromGeneral(line) {
		t.Errorf("envelope payload=%q, want marked=%q", env.Payload, home.MarkFromGeneral(line))
	}

	// Verify pending record was written in General outbox.
	parentStore := home.NewStore(parentHome)
	// Sender identity is derived from parent home basename (no marker).
	generalIdentity := filepath.Base(parentHome)
	pending, err := parentStore.ReadPending(generalIdentity, result.MessageID)
	if err != nil {
		t.Fatalf("ReadPending: %v", err)
	}
	if pending == nil {
		t.Fatal("pending record not found in General outbox")
	}

	// Verify pending still exists after acknowledgment (not removed).
	pending2, err := parentStore.ReadPending(generalIdentity, result.MessageID)
	if err != nil {
		t.Fatalf("ReadPending after ack: %v", err)
	}
	if pending2 == nil {
		t.Fatal("pending must NOT be removed on acknowledgment alone")
	}
}

// TestSendMailboxToCaptain_DeadPane verifies that a dead/busy pane retains
// the pending record and returns an error.
func TestSendMailboxToCaptain_DeadPane(t *testing.T) {
	parentHome, captainHome, captainID := setupTestHomes(t)

	sm := Info{ID: captainID, Home: captainHome}
	result := SendMailboxToCaptain(sm, parentHome, "report status", &captainTestMailboxSender{})

	if result.Err == nil {
		t.Fatal("expected error for unacknowledged prompt")
	}
	if !strings.Contains(result.Err.Error(), "not acknowledged") {
		t.Errorf("expected 'not acknowledged' error, got: %v", result.Err)
	}
	if result.Acknowledged {
		t.Fatal("expected acknowledged=false")
	}
	if result.MessageID == "" {
		t.Fatal("expected message ID even on failure")
	}

	// Verify envelope was still written (write happens before notification).
	captainStore := home.NewStore(captainHome)
	env, err := captainStore.ReadEnvelope(filepath.Base(parentHome), result.MessageID)
	if err != nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	if env == nil {
		t.Fatal("envelope must exist even when notification fails")
	}

	// Verify pending was written and NOT removed.
	parentStore := home.NewStore(parentHome)
	pending, err := parentStore.ReadPending(filepath.Base(parentHome), result.MessageID)
	if err != nil {
		t.Fatalf("ReadPending: %v", err)
	}
	if pending == nil {
		t.Fatal("pending must remain after unacknowledged send")
	}
}

// TestSendMailboxToCaptain_InvalidMeta verifies that invalid task meta
// fails closed.
func TestSendMailboxToCaptain_InvalidMeta(t *testing.T) {
	parentHome, captainHome, captainID := setupTestHomes(t)

	// Corrupt the meta: change kind to something else.
	taskID := taskIDForCaptain(captainID)
	meta, _ := home.ReadMeta(parentHome, taskID)
	meta["kind"] = "ship"
	home.WriteMeta(parentHome, taskID, meta)

	sm := Info{ID: captainID, Home: captainHome}
	result := SendMailboxToCaptain(sm, parentHome, "line", &captainTestMailboxSender{acknowledged: true})

	if result.Err == nil {
		t.Fatal("expected error for non-captain meta")
	}
	if !strings.Contains(result.Err.Error(), "kind") {
		t.Errorf("expected kind error, got: %v", result.Err)
	}
}

// TestReconcileMailboxPending_ExactAckRemovesPending verifies that when
// the captain has written an exact ProcessingAck, reconcile removes the
// pending record.
func TestReconcileMailboxPending_ExactAckRemovesPending(t *testing.T) {
	parentHome, captainHome, captainID := setupTestHomes(t)

	// Write an envelope to captain's inbox and pending to General's outbox.
	env := &home.Envelope{
		SenderRank:     home.RankGeneral,
		SenderIdentity: filepath.Base(parentHome),
		ReceiverRank:   home.RankCaptain,
		ReceiverID:     captainID,
		Payload:        "do: work",
	}
	captainStore := home.NewStore(captainHome)
	if err := captainStore.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	parentStore := home.NewStore(parentHome)
	if err := parentStore.WritePending(env); err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	// Write an ack in the captain's inbox (simulate captain agent processing).
	ack := &home.ProcessingAck{
		MessageID: env.MessageID, SenderRank: env.SenderRank,
		SenderIdentity: env.SenderIdentity, ReceiverRank: env.ReceiverRank,
		ReceiverID: env.ReceiverID, PayloadHash: env.PayloadHash,
		ProcessedAt: time.Now().UnixNano(), Outcome: "done",
	}
	if err := captainStore.WriteAck(ack); err != nil {
		t.Fatalf("WriteAck: %v", err)
	}

	// Run reconcile.
	sm := Info{ID: captainID, Home: captainHome}
	if err := ReconcileMailboxPending(parentHome, sm, &captainTestMailboxSender{acknowledged: true}); err != nil {
		t.Fatalf("ReconcileMailboxPending: %v", err)
	}

	// Verify pending is removed.
	pending, err := parentStore.ReadPending(filepath.Base(parentHome), env.MessageID)
	if err != nil {
		t.Fatalf("ReadPending: %v", err)
	}
	if pending != nil {
		t.Error("pending should be removed after exact ack reconcile")
	}
}

// TestReconcileMailboxPending_WrongAckFailsClosed verifies that a mismatched
// ack causes reconcile to fail closed.
func TestReconcileMailboxPending_WrongAckFailsClosed(t *testing.T) {
	parentHome, captainHome, captainID := setupTestHomes(t)

	// Write envelope and pending.
	env := &home.Envelope{
		SenderRank:     home.RankGeneral,
		SenderIdentity: filepath.Base(parentHome),
		ReceiverRank:   home.RankCaptain,
		ReceiverID:     captainID,
		Payload:        "do: work",
		PayloadHash:    home.PayloadHashHex("do: work"),
	}
	captainStore := home.NewStore(captainHome)
	parentStore := home.NewStore(parentHome)
	if err := captainStore.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	if err := parentStore.WritePending(env); err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	// Write a WRONG ack (different payload hash).
	ack := &home.ProcessingAck{
		MessageID: env.MessageID, SenderRank: env.SenderRank,
		SenderIdentity: env.SenderIdentity, ReceiverRank: env.ReceiverRank,
		ReceiverID: env.ReceiverID, PayloadHash: home.PayloadHashHex("wrong payload"),
		ProcessedAt: time.Now().UnixNano(), Outcome: "done",
	}
	if err := captainStore.WriteAck(ack); err != nil {
		t.Fatalf("WriteAck: %v", err)
	}

	// Run reconcile — should fail closed.
	sm := Info{ID: captainID, Home: captainHome}
	err := ReconcileMailboxPending(parentHome, sm, &captainTestMailboxSender{acknowledged: true})
	if err == nil {
		t.Fatal("expected error for wrong ack")
	}
	if !strings.Contains(err.Error(), "ack validation failed") {
		t.Errorf("expected ack validation error, got: %v", err)
	}

	// Verify pending still exists.
	pending, _ := parentStore.ReadPending(filepath.Base(parentHome), env.MessageID)
	if pending == nil {
		t.Error("pending should still exist after failed reconcile")
	}
}

// TestReconcileMailboxPending_DuplicateNotification verifies that when no
// ack exists, reconcile retries the NotificationRef (no error returned when
// backed by a real backend — requires backend integration).
func TestReconcileMailboxPending_NoAckRetries(t *testing.T) {
	sender := &captainTestMailboxSender{acknowledged: true}
	parentHome, captainHome, captainID := setupTestHomes(t)

	// Write envelope and pending.
	env := &home.Envelope{
		SenderRank:     home.RankGeneral,
		SenderIdentity: filepath.Base(parentHome),
		ReceiverRank:   home.RankCaptain,
		ReceiverID:     captainID,
		Payload:        "do: work",
	}
	captainStore := home.NewStore(captainHome)
	parentStore := home.NewStore(parentHome)
	if err := captainStore.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	if err := parentStore.WritePending(env); err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	// Set up backend that acknowledges the resend.

	// Run reconcile — no ack yet, but backend is alive and acknowledges.
	sm := Info{ID: captainID, Home: captainHome}
	if err := ReconcileMailboxPending(parentHome, sm, sender); err != nil {
		t.Fatalf("ReconcileMailboxPending: %v", err)
	}

	// Verify the notification was sent (duplicate notification idempotent).
	if sender.lastPayload == "" {
		t.Fatal("expected notification text on retry")
	}
	var ref home.NotificationRef
	if err := json.Unmarshal([]byte(sender.lastPayload), &ref); err != nil {
		t.Fatalf("invalid NotificationRef: %v", err)
	}
	if ref.MessageID != env.MessageID {
		t.Errorf("ref MessageID=%q, want %q", ref.MessageID, env.MessageID)
	}

	// Pending should still exist (no ack yet).
	pending, _ := parentStore.ReadPending(filepath.Base(parentHome), env.MessageID)
	if pending == nil {
		t.Error("pending should still exist when no ack was written")
	}
}

// TestInboxAckCmd_AckRef tests that the captain can ack a NotificationRef
// and the ack outcome is "accepted".
func TestInboxAckCmd_AckRef(t *testing.T) {
	captainHome := filepath.Join(t.TempDir(), "test-captain")
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Set up captain home with identity home.
	if err := home.WriteHomeIdentity(captainHome, "test-captain", home.RankCaptain); err != nil {
		t.Fatalf("WriteHomeIdentity: %v", err)
	}

	// Write an envelope in the captain's inbox (as if General sent it).
	env := &home.Envelope{
		SenderRank:     home.RankGeneral,
		SenderIdentity: "general-main",
		ReceiverRank:   home.RankCaptain,
		ReceiverID:     "test-captain",
		Payload:        "do: work",
	}
	captainStore := home.NewStore(captainHome)
	if err := captainStore.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	// Create receiver and ack the notification ref.
	recv, err := home.NewReceiver(captainHome)
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	ref := home.NotificationRef{
		MessageID:      env.MessageID,
		SenderIdentity: "general-main",
	}
	ack, err := recv.Ack(ref)
	if err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if ack == nil {
		t.Fatal("expected non-nil ack")
	}
	if ack.Outcome != home.OutcomeAccepted {
		t.Errorf("ack outcome=%q, want %q", ack.Outcome, home.OutcomeAccepted)
	}

	// Verify ack file was written on disk.
	ack2, err := captainStore.ReadAck("general-main", env.MessageID)
	if err != nil {
		t.Fatalf("ReadAck: %v", err)
	}
	if ack2 == nil {
		t.Fatal("ack not found on disk")
	}
	if ack2.MessageID != env.MessageID {
		t.Errorf("ack MessageID=%q", ack2.MessageID)
	}
	if ack2.Outcome != home.OutcomeAccepted {
		t.Errorf("ack outcome=%q, want %q", ack2.Outcome, home.OutcomeAccepted)
	}

	// Acking the same ref again must be idempotent.
	ack3, err := recv.Ack(ref)
	if err != nil {
		t.Fatalf("second Ack: %v", err)
	}
	// Original timestamp preserved.
	if ack3.ProcessedAt != ack.ProcessedAt {
		t.Errorf("timestamp not preserved: %d vs %d", ack3.ProcessedAt, ack.ProcessedAt)
	}
}

// TestInboxAckCmd_InvalidRef verifies that acking an invalid
// NotificationRef fails closed.
func TestInboxAckCmd_InvalidRef(t *testing.T) {
	captainHome := filepath.Join(t.TempDir(), "test-captain")
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := home.WriteHomeIdentity(captainHome, "test-captain", home.RankCaptain); err != nil {
		t.Fatalf("WriteHomeIdentity: %v", err)
	}

	recv, err := home.NewReceiver(captainHome)
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}

	// Empty ref should fail.
	_, err = recv.Ack(home.NotificationRef{})
	if err == nil {
		t.Fatal("expected error for empty ref")
	}
}

// TestSendMailboxToCaptain_MarkerInPayload verifies that the marker is in
// the envelope payload, not in the notification text.
func TestSendMailboxToCaptain_MarkerInPayload(t *testing.T) {
	sender := &captainTestMailboxSender{acknowledged: true}
	parentHome, captainHome, captainID := setupTestHomes(t)

	sm := Info{ID: captainID, Home: captainHome}
	line := "report status"

	result := SendMailboxToCaptain(sm, parentHome, line, sender)
	if result.Err != nil {
		t.Fatalf("SendMailboxToCaptain: %v", result.Err)
	}

	// Verify notification text does NOT contain the home.
	if strings.Contains(sender.lastPayload, home.FromGeneralLabel) {
		t.Error("notification text must NOT contain the marker")
	}

	// Verify envelope payload DOES contain the home.
	captainStore := home.NewStore(captainHome)
	env, err := captainStore.ReadEnvelope(filepath.Base(parentHome), result.MessageID)
	if err != nil || env == nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	if !strings.HasPrefix(env.Payload, home.FromGeneralMark) {
		t.Errorf("envelope payload should start with marker, got: %q", env.Payload)
	}
	// Verify payload has the command after the home.
	if !strings.Contains(env.Payload, line) {
		t.Errorf("envelope payload should contain the original line %q, got %q", line, env.Payload)
	}
}

// TestSendMailboxToCaptain_UnmarkedCaptainHome fails when captain has no
// provenance marker (validation inside SendMailboxToCaptain).
func TestSendMailboxToCaptain_UnmarkedCaptainHome(t *testing.T) {
	parentHome := filepath.Join(t.TempDir(), "general-main")
	os.MkdirAll(parentHome, 0755)

	captainHome := filepath.Join(t.TempDir(), "unmarked")
	os.MkdirAll(captainHome, 0755)

	// No provenance marker written; captain is not registered.
	sm := Info{ID: "unmarked", Home: captainHome}

	// Set up backend.

	// Write task meta using canonical path (as Launch would).
	canonHome, _ := canonicalCaptainHome(captainHome)
	taskID := taskIDForCaptain("unmarked")
	home.WriteMeta(parentHome, taskID, map[string]string{
		"kind": "captain", "sm_id": "unmarked", "home": canonHome,
		"window": "test-window", "backend": "test",
	})

	result := SendMailboxToCaptain(sm, parentHome, "line", &captainTestMailboxSender{acknowledged: true})
	if result.Err == nil {
		t.Fatal("expected error for unmarked captain home")
	}
	if !strings.Contains(result.Err.Error(), "no .munsu-captain-home") {
		t.Errorf("expected provenance marker error, got: %v", result.Err)
	}
}

// TestReconcileMailboxPending_Idempotent verifies that repeated reconcile
// with no pending is a no-op.
func TestReconcileMailboxPending_Idempotent(t *testing.T) {
	parentHome, captainHome, captainID := setupTestHomes(t)

	sm := Info{ID: captainID, Home: captainHome}

	// First call with no pending — must be no-op.
	if err := ReconcileMailboxPending(parentHome, sm, &captainTestMailboxSender{acknowledged: true}); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	// Second call also no-op.
	if err := ReconcileMailboxPending(parentHome, sm, &captainTestMailboxSender{acknowledged: true}); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
}
