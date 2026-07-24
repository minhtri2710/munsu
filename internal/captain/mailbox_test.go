package captain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/mailbox"
	"github.com/minhtri2710/munsu/internal/marker"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

// --- Test helpers ---

// fakeSessionBackend is a minimal session.Backend for testing SubmitPrompt.
type fakeSessionBackend struct {
	session.Backend // embed nil to satisfy interface TODO: actually test
	alive           bool
	acknowledged    bool // whether SubmitPrompt returns acknowledged
	lastText        string
}

func (f *fakeSessionBackend) Alive(string) bool { return f.alive }

// fakeSubmitPromptBackend implements PromptSubmitter.
type fakeSubmitPromptBackend struct {
	alive        bool
	acknowledged bool
	lastText     string
}

func (f *fakeSubmitPromptBackend) Alive(string) bool { return f.alive }
func (f *fakeSubmitPromptBackend) NewWindow(string, string) (string, error) {
	return "test-window", nil
}
func (f *fakeSubmitPromptBackend) SendKeys(string, string) error { return nil }
func (f *fakeSubmitPromptBackend) Capture(string, int) (string, error) {
	return "", nil
}
func (f *fakeSubmitPromptBackend) Teardown(string) error { return nil }
func (f *fakeSubmitPromptBackend) AgentPrompt(windowID, text string) session.PromptResult {
	f.lastText = text
	if f.acknowledged {
		return session.PromptResult{Status: session.PromptSubmitted}
	}
	return session.PromptResult{Status: session.PromptStalled}
}

// Override the package-level backendForTask for testing.
func setTestBackend(be session.Backend) func() {
	orig := backendForTask
	backendForTask = func(homeDir string, meta map[string]string) (session.Backend, string, error) {
		return be, "test", nil
	}
	return func() { backendForTask = orig }
}

// setupTestHomes creates a parent (General) home and a captain home with
// valid provenance and task meta.
func setupTestHomes(t *testing.T) (parentHome, captainHome, captainID string) {
	t.Helper()

	parentHome = filepath.Join(t.TempDir(), "general-main")
	if err := os.MkdirAll(parentHome, 0755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}

	captainID = "test-captain"
	captainHome = filepath.Join(t.TempDir(), captainID)
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatalf("mkdir captain: %v", err)
	}

	// Seed captain home.
	if err := Seed(captainID, captainHome, ""); err != nil {
		// Seed requires parentHome for charter; seed with explicit parent.
		if err := SeedWithParent(captainID, captainHome, parentHome, ""); err != nil {
			t.Fatalf("Seed: %v", err)
		}
	}

	// Register captain.
	if err := Register(parentHome, captainID, captainHome, "", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Write task meta.
	taskID := taskIDForCaptain(captainID)
	captainCanon, err := canonicalHome(captainHome)
	if err != nil {
		t.Fatalf("canonicalHome: %v", err)
	}
	meta := map[string]string{
		"kind":    "captain",
		"sm_id":   captainID,
		"home":    captainCanon,
		"window":  "test-window",
		"backend": "test",
	}
	if err := task.WriteMeta(parentHome, taskID, meta); err != nil {
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
	parentHome, captainHome, captainID := setupTestHomes(t)

	be := &fakeSubmitPromptBackend{alive: true, acknowledged: true}
	restore := setTestBackend(be)
	defer restore()

	sm := Info{ID: captainID, Home: captainHome}
	line := "report status"

	result := SendMailboxToCaptain(sm, parentHome, line)
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
	if be.lastText == "" {
		t.Fatal("no notification text sent")
	}
	if strings.Contains(be.lastText, line) {
		t.Error("notification text must NOT contain the raw line (payload)")
	}
	var ref mailbox.NotificationRef
	if err := json.Unmarshal([]byte(be.lastText), &ref); err != nil {
		t.Fatalf("notification text must be valid NotificationRef JSON: %v", err)
	}
	if ref.MessageID != result.MessageID {
		t.Errorf("ref MessageID=%q, want %q", ref.MessageID, result.MessageID)
	}

	// Verify envelope was written to captain inbox.
	captainStore := mailbox.NewStore(captainHome)
	env, err := captainStore.ReadEnvelope(ref.SenderIdentity, ref.MessageID)
	if err != nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	if env == nil {
		t.Fatal("envelope not found in captain inbox")
	}
	if env.Payload != marker.MarkFromGeneral(line) {
		t.Errorf("envelope payload=%q, want marked=%q", env.Payload, marker.MarkFromGeneral(line))
	}

	// Verify pending record was written in General outbox.
	parentStore := mailbox.NewStore(parentHome)
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

	be := &fakeSubmitPromptBackend{alive: true, acknowledged: false}
	restore := setTestBackend(be)
	defer restore()

	sm := Info{ID: captainID, Home: captainHome}
	result := SendMailboxToCaptain(sm, parentHome, "report status")

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
	captainStore := mailbox.NewStore(captainHome)
	env, err := captainStore.ReadEnvelope(filepath.Base(parentHome), result.MessageID)
	if err != nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	if env == nil {
		t.Fatal("envelope must exist even when notification fails")
	}

	// Verify pending was written and NOT removed.
	parentStore := mailbox.NewStore(parentHome)
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
	meta, _ := task.ReadMeta(parentHome, taskID)
	meta["kind"] = "ship"
	task.WriteMeta(parentHome, taskID, meta)

	sm := Info{ID: captainID, Home: captainHome}
	result := SendMailboxToCaptain(sm, parentHome, "line")

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
	env := &mailbox.Envelope{
		SenderRank:     mailbox.RankGeneral,
		SenderIdentity: filepath.Base(parentHome),
		ReceiverRank:   mailbox.RankCaptain,
		ReceiverID:     captainID,
		Payload:        "do: work",
	}
	captainStore := mailbox.NewStore(captainHome)
	if err := captainStore.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	parentStore := mailbox.NewStore(parentHome)
	if err := parentStore.WritePending(env); err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	// Write an ack in the captain's inbox (simulate captain agent processing).
	ack := &mailbox.ProcessingAck{
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
	if err := ReconcileMailboxPending(parentHome, sm); err != nil {
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
	env := &mailbox.Envelope{
		SenderRank:     mailbox.RankGeneral,
		SenderIdentity: filepath.Base(parentHome),
		ReceiverRank:   mailbox.RankCaptain,
		ReceiverID:     captainID,
		Payload:        "do: work",
		PayloadHash:    mailbox.PayloadHashHex("do: work"),
	}
	captainStore := mailbox.NewStore(captainHome)
	parentStore := mailbox.NewStore(parentHome)
	if err := captainStore.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	if err := parentStore.WritePending(env); err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	// Write a WRONG ack (different payload hash).
	ack := &mailbox.ProcessingAck{
		MessageID: env.MessageID, SenderRank: env.SenderRank,
		SenderIdentity: env.SenderIdentity, ReceiverRank: env.ReceiverRank,
		ReceiverID: env.ReceiverID, PayloadHash: mailbox.PayloadHashHex("wrong payload"),
		ProcessedAt: time.Now().UnixNano(), Outcome: "done",
	}
	if err := captainStore.WriteAck(ack); err != nil {
		t.Fatalf("WriteAck: %v", err)
	}

	// Run reconcile — should fail closed.
	sm := Info{ID: captainID, Home: captainHome}
	err := ReconcileMailboxPending(parentHome, sm)
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
	parentHome, captainHome, captainID := setupTestHomes(t)

	// Write envelope and pending.
	env := &mailbox.Envelope{
		SenderRank:     mailbox.RankGeneral,
		SenderIdentity: filepath.Base(parentHome),
		ReceiverRank:   mailbox.RankCaptain,
		ReceiverID:     captainID,
		Payload:        "do: work",
	}
	captainStore := mailbox.NewStore(captainHome)
	parentStore := mailbox.NewStore(parentHome)
	if err := captainStore.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	if err := parentStore.WritePending(env); err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	// Set up backend that acknowledges the resend.
	be := &fakeSubmitPromptBackend{alive: true, acknowledged: true}
	restore := setTestBackend(be)
	defer restore()

	// Run reconcile — no ack yet, but backend is alive and acknowledges.
	sm := Info{ID: captainID, Home: captainHome}
	if err := ReconcileMailboxPending(parentHome, sm); err != nil {
		t.Fatalf("ReconcileMailboxPending: %v", err)
	}

	// Verify the notification was sent (duplicate notification idempotent).
	if be.lastText == "" {
		t.Fatal("expected notification text on retry")
	}
	var ref mailbox.NotificationRef
	if err := json.Unmarshal([]byte(be.lastText), &ref); err != nil {
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

	// Set up captain home with identity marker.
	if err := mailbox.WriteHomeIdentity(captainHome, "test-captain", mailbox.RankCaptain); err != nil {
		t.Fatalf("WriteHomeIdentity: %v", err)
	}

	// Write an envelope in the captain's inbox (as if General sent it).
	env := &mailbox.Envelope{
		SenderRank:     mailbox.RankGeneral,
		SenderIdentity: "general-main",
		ReceiverRank:   mailbox.RankCaptain,
		ReceiverID:     "test-captain",
		Payload:        "do: work",
	}
	captainStore := mailbox.NewStore(captainHome)
	if err := captainStore.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	// Create receiver and ack the notification ref.
	recv, err := mailbox.NewReceiver(captainHome)
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	ref := mailbox.NotificationRef{
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
	if ack.Outcome != mailbox.OutcomeAccepted {
		t.Errorf("ack outcome=%q, want %q", ack.Outcome, mailbox.OutcomeAccepted)
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
	if ack2.Outcome != mailbox.OutcomeAccepted {
		t.Errorf("ack outcome=%q, want %q", ack2.Outcome, mailbox.OutcomeAccepted)
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
	if err := mailbox.WriteHomeIdentity(captainHome, "test-captain", mailbox.RankCaptain); err != nil {
		t.Fatalf("WriteHomeIdentity: %v", err)
	}

	recv, err := mailbox.NewReceiver(captainHome)
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}

	// Empty ref should fail.
	_, err = recv.Ack(mailbox.NotificationRef{})
	if err == nil {
		t.Fatal("expected error for empty ref")
	}
}

// TestSendMailboxToCaptain_MarkerInPayload verifies that the marker is in
// the envelope payload, not in the notification text.
func TestSendMailboxToCaptain_MarkerInPayload(t *testing.T) {
	parentHome, captainHome, captainID := setupTestHomes(t)

	be := &fakeSubmitPromptBackend{alive: true, acknowledged: true}
	restore := setTestBackend(be)
	defer restore()

	sm := Info{ID: captainID, Home: captainHome}
	line := "report status"

	result := SendMailboxToCaptain(sm, parentHome, line)
	if result.Err != nil {
		t.Fatalf("SendMailboxToCaptain: %v", result.Err)
	}

	// Verify notification text does NOT contain the marker.
	if strings.Contains(be.lastText, marker.FromGeneralLabel) {
		t.Error("notification text must NOT contain the marker")
	}

	// Verify envelope payload DOES contain the marker.
	captainStore := mailbox.NewStore(captainHome)
	env, err := captainStore.ReadEnvelope(filepath.Base(parentHome), result.MessageID)
	if err != nil || env == nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	if !strings.HasPrefix(env.Payload, marker.FromGeneralMark) {
		t.Errorf("envelope payload should start with marker, got: %q", env.Payload)
	}
	// Verify payload has the command after the marker.
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
	be := &fakeSubmitPromptBackend{alive: true, acknowledged: true}
	restore := setTestBackend(be)
	defer restore()

	// Write task meta using canonical path (as Launch would).
	canonHome, _ := canonicalHome(captainHome)
	taskID := taskIDForCaptain("unmarked")
	task.WriteMeta(parentHome, taskID, map[string]string{
		"kind": "captain", "sm_id": "unmarked", "home": canonHome,
		"window": "test-window", "backend": "test",
	})

	result := SendMailboxToCaptain(sm, parentHome, "line")
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
	if err := ReconcileMailboxPending(parentHome, sm); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	// Second call also no-op.
	if err := ReconcileMailboxPending(parentHome, sm); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
}
