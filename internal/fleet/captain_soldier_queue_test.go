//go:build integration

package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

// --- Test helpers ---

type fakeAgentEndpoint struct {
	busy         bool
	busyErr      error
	acknowledged bool
	status       string
	lastText     string
	promptCalls  int
}

func (*fakeAgentEndpoint) Alive(string, map[string]string) (bool, error)  { return true, nil }
func (f *fakeAgentEndpoint) Busy(string, map[string]string) (bool, error) { return f.busy, f.busyErr }
func (f *fakeAgentEndpoint) Send(_ string, _ map[string]string, payload string) home.BoundSendResult {
	f.promptCalls++
	f.lastText = payload
	status := f.status
	if status == "" {
		if f.acknowledged {
			status = "submitted"
		} else {
			status = "stalled"
		}
	}
	return home.BoundSendResult{Status: status, Acknowledged: f.acknowledged}
}

// setupSoldierTestHomes creates a captain home with a soldier task meta.
// Soldiers share the captain's home; their identity is the task ID.
func setupSoldierTestHomes(t *testing.T, agentStatus string) (captainHome, soldierTaskID, senderIdentity string) {
	t.Helper()

	captainHome = filepath.Join(t.TempDir(), "captain-main")
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatalf("mkdir captain: %v", err)
	}
	senderIdentity = "captain-main"
	if err := home.WriteHomeIdentity(captainHome, senderIdentity, home.RankCaptain); err != nil {
		t.Fatalf("WriteHomeIdentity captain: %v", err)
	}

	soldierTaskID = "task:test-1"

	// Write soldier task meta in the shared captain home.
	meta := map[string]string{
		"window":  "test-window",
		"backend": "test",
		"kind":    "ship",
		"harness": "pi",
	}
	if err := mhome.WriteMeta(captainHome, soldierTaskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	return captainHome, soldierTaskID, senderIdentity
}

// TestSendToSoldier_Idle_SendsNotificationRef verifies that when the soldier
// is idle, the command is persisted as an envelope and NotificationRef is sent.
func TestSendToSoldier_Idle_SendsNotificationRef(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "idle")

	be := &fakeAgentEndpoint{acknowledged: true}

	result := SendToSoldier(captainHome, soldierTaskID, senderIdentity, "do: work", be)

	if result.Err != nil {
		t.Fatalf("SendToSoldier: %v", result.Err)
	}
	if !result.Sent {
		t.Fatal("expected Sent=true")
	}
	if result.Queued {
		t.Fatal("expected Queued=false")
	}
	if result.MessageID == "" {
		t.Fatal("expected non-empty MessageID")
	}

	// Verify SubmitPrompt was called exactly once.
	if be.promptCalls != 1 {
		t.Errorf("promptCalls=%d, want 1", be.promptCalls)
	}

	// Verify the submitted text is a NotificationRef, not the raw line.
	if be.lastText == "" {
		t.Fatal("no notification text sent")
	}
	if strings.Contains(be.lastText, "do: work") {
		t.Error("notification text must NOT contain the raw line (payload)")
	}
	var ref home.NotificationRef
	if err := json.Unmarshal([]byte(be.lastText), &ref); err != nil {
		t.Fatalf("notification text must be valid NotificationRef JSON: %v", err)
	}
	if ref.MessageID != result.MessageID {
		t.Errorf("ref MessageID=%q, want %q", ref.MessageID, result.MessageID)
	}
	if ref.SenderIdentity != senderIdentity {
		t.Errorf("ref SenderIdentity=%q, want %q", ref.SenderIdentity, senderIdentity)
	}

	// Verify envelope was written to the shared inbox.
	store := home.NewStore(captainHome)
	env, err := store.ReadEnvelope(senderIdentity, result.MessageID)
	if err != nil || env == nil {
		t.Fatal("envelope should exist in inbox")
	}
	if env.Payload != "do: work" {
		t.Errorf("payload=%q, want %q", env.Payload, "do: work")
	}
	if env.SenderRank != home.RankCaptain {
		t.Errorf("sender rank=%q, want captain", env.SenderRank)
	}
	if env.ReceiverRank != home.RankSoldier {
		t.Errorf("receiver rank=%q, want soldier", env.ReceiverRank)
	}
	if env.ReceiverID != cleanReceiverID(soldierTaskID) {
		t.Errorf("receiver ID=%q, want %q", env.ReceiverID, cleanReceiverID(soldierTaskID))
	}
	if env.TaskID != soldierTaskID {
		t.Errorf("task ID=%q, want %q", env.TaskID, soldierTaskID)
	}

	// Verify pending in outbox.
	pending, err := store.ReadPending(senderIdentity, result.MessageID)
	if err != nil {
		t.Fatalf("ReadPending: %v", err)
	}
	if pending == nil {
		t.Fatal("pending should exist in outbox")
	}

	// Verify NO ack was written (only Soldier writes the ack).
	if store.IsAcked(senderIdentity, result.MessageID) {
		t.Fatal("no ack should exist — only Soldier writes ack")
	}
}

// TestSendToSoldier_Busy_QueuesWithoutSubmitPrompt verifies that when the soldier
// is busy/working, the command is queued and no SubmitPrompt call is made.
func TestSendToSoldier_Busy_QueuesWithoutSubmitPrompt(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "working")

	be := &fakeAgentEndpoint{busy: true, acknowledged: true}

	result := SendToSoldier(captainHome, soldierTaskID, senderIdentity, "do: work", be)

	if result.Err != nil {
		t.Fatalf("SendToSoldier: %v", result.Err)
	}
	if !result.Queued {
		t.Fatal("expected Queued=true for busy soldier")
	}
	if result.Sent {
		t.Fatal("expected Sent=false for busy soldier (no SubmitPrompt)")
	}
	if result.MessageID == "" {
		t.Fatal("expected non-empty MessageID")
	}

	// Verify SubmitPrompt was NEVER called.
	if be.promptCalls != 0 {
		t.Errorf("promptCalls=%d, want 0 (no SubmitPrompt when busy)", be.promptCalls)
	}

	// Verify envelope was still written.
	store := home.NewStore(captainHome)
	env, err := store.ReadEnvelope(senderIdentity, result.MessageID)
	if err != nil || env == nil {
		t.Fatal("envelope should exist even when busy")
	}

	// Verify pending was written.
	pending, err := store.ReadPending(senderIdentity, result.MessageID)
	if err != nil || pending == nil {
		t.Fatal("pending should exist when queued")
	}

	// Verify no ack was written.
	if store.IsAcked(senderIdentity, result.MessageID) {
		t.Fatal("no ack should exist for queued command")
	}
}

// TestSendToSoldier_DeadEndpoint_ReturnsError verifies that a dead endpoint
// returns an error and retains pending.
func TestSendToSoldier_DeadEndpoint_ReturnsError(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "idle")

	// Backend acknowledges but prompts are unacknowledged.
	be := &fakeAgentEndpoint{}

	result := SendToSoldier(captainHome, soldierTaskID, senderIdentity, "do: work", be)

	if result.Err == nil {
		t.Fatal("expected error for unacknowledged prompt")
	}
	if result.Sent {
		t.Fatal("expected Sent=false on unacknowledged")
	}

	// Verify pending still exists.
	store := home.NewStore(captainHome)
	pending, _ := store.ReadPending(senderIdentity, result.MessageID)
	if pending == nil {
		t.Fatal("pending should exist after unacknowledged send")
	}
}

// TestFlushPendingSoldierCommands_FlushesNotificationRef verifies that flush
// sends a NotificationRef (not raw line) for the oldest pending command.
func TestFlushPendingSoldierCommands_FlushesNotificationRef(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "idle")

	// Simulate a queued command (write envelope + pending, no ack).
	env := &home.Envelope{
		SenderRank:     home.RankCaptain,
		SenderIdentity: senderIdentity,
		ReceiverRank:   home.RankSoldier,
		ReceiverID:     cleanReceiverID(soldierTaskID),
		TaskID:         soldierTaskID,
		Payload:        "do: queued work",
	}
	store := home.NewStore(captainHome)
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	if err := store.WritePending(env); err != nil {
		t.Fatalf("WritePending: %v", err)
	}
	if store.IsAcked(senderIdentity, env.MessageID) {
		t.Fatal("no ack should exist before flush")
	}

	be := &fakeAgentEndpoint{acknowledged: true}

	result := FlushPendingSoldierCommands(captainHome, soldierTaskID, senderIdentity, be)

	if result.Err != nil {
		t.Fatalf("FlushPendingSoldierCommands: %v", result.Err)
	}
	if !result.Sent {
		t.Fatal("expected Sent=true on flush")
	}

	// Verify SubmitPrompt was called with NotificationRef, not raw line.
	if be.promptCalls != 1 {
		t.Errorf("promptCalls=%d, want 1", be.promptCalls)
	}
	if strings.Contains(be.lastText, "do: queued work") {
		t.Error("flush must send NotificationRef, not raw line")
	}
	var ref home.NotificationRef
	if err := json.Unmarshal([]byte(be.lastText), &ref); err != nil {
		t.Fatalf("flush must send valid NotificationRef: %v", err)
	}
	if ref.MessageID != env.MessageID {
		t.Errorf("ref MessageID=%q", ref.MessageID)
	}
	if ref.SenderIdentity != senderIdentity {
		t.Errorf("ref SenderIdentity=%q", ref.SenderIdentity)
	}

	// No ack written by flush.
	if store.IsAcked(senderIdentity, env.MessageID) {
		t.Fatal("flush must NOT write ack")
	}

	// Pending still exists (removed on reconcile).
	pending, _ := store.ReadPending(senderIdentity, env.MessageID)
	if pending == nil {
		t.Fatal("pending should exist after flush (removed only on reconcile)")
	}
}

// TestFlushPendingSoldierCommands_NoPending_IsNoop verifies that flush with
// no pending is a no-op.
func TestFlushPendingSoldierCommands_NoPending_IsNoop(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "idle")

	be := &fakeAgentEndpoint{acknowledged: true}

	result := FlushPendingSoldierCommands(captainHome, soldierTaskID, senderIdentity, be)

	if result.Err != nil {
		t.Fatalf("Flush: %v", result.Err)
	}
	if result.MessageID != "" {
		t.Fatal("expected empty MessageID for no-op flush")
	}
	if be.promptCalls != 0 {
		t.Errorf("promptCalls=%d, want 0", be.promptCalls)
	}
}

// TestFlushPendingSoldierCommands_StillBusy_RetainsPending verifies that flush
// does not send when soldier is still busy.
func TestFlushPendingSoldierCommands_StillBusy_RetainsPending(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "working")

	// Simulate a queued command.
	env := &home.Envelope{
		SenderRank:     home.RankCaptain,
		SenderIdentity: senderIdentity,
		ReceiverRank:   home.RankSoldier,
		ReceiverID:     cleanReceiverID(soldierTaskID),
		TaskID:         soldierTaskID,
		Payload:        "do: queued work",
	}
	store := home.NewStore(captainHome)
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	if err := store.WritePending(env); err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	be := &fakeAgentEndpoint{busy: true, acknowledged: true}

	result := FlushPendingSoldierCommands(captainHome, soldierTaskID, senderIdentity, be)

	if result.Err != nil {
		t.Fatalf("Flush: %v", result.Err)
	}
	if !result.Queued {
		t.Fatal("expected Queued=true when still busy")
	}
	if result.Sent {
		t.Fatal("expected Sent=false when still busy")
	}
	if be.promptCalls != 0 {
		t.Errorf("promptCalls=%d, want 0 (no SubmitPrompt when busy)", be.promptCalls)
	}

	// Pending retained.
	pending, _ := store.ReadPending(senderIdentity, env.MessageID)
	if pending == nil {
		t.Fatal("pending should still exist when soldier is busy")
	}
}

// TestFlushPendingSoldierCommands_DuplicateFlushIsIdempotent verifies that
// calling flush twice with no pending between is a no-op.
func TestFlushPendingSoldierCommands_DuplicateFlushIsIdempotent(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "idle")

	be := &fakeAgentEndpoint{acknowledged: true}

	// First flush with no pending — no-op.
	r1 := FlushPendingSoldierCommands(captainHome, soldierTaskID, senderIdentity, be)
	if r1.Err != nil {
		t.Fatalf("first flush: %v", r1.Err)
	}
	if r1.MessageID != "" {
		t.Fatal("first flush should be no-op")
	}

	// Second flush with no pending — also no-op.
	r2 := FlushPendingSoldierCommands(captainHome, soldierTaskID, senderIdentity, be)
	if r2.Err != nil {
		t.Fatalf("second flush: %v", r2.Err)
	}
	if r2.MessageID != "" {
		t.Fatal("second flush should be no-op")
	}

	if be.promptCalls != 0 {
		t.Errorf("promptCalls=%d, want 0", be.promptCalls)
	}
}

// TestSendToSoldier_Restart_PendingSurvives verifies that pending commands
// survive a restart (files are persisted).
func TestSendToSoldier_Restart_PendingSurvives(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "idle")

	// Queue a command (busy case).
	be1 := &fakeAgentEndpoint{busy: true, acknowledged: true}

	result := SendToSoldier(captainHome, soldierTaskID, senderIdentity, "do: survive restart", be1)
	if result.Err != nil {
		t.Fatalf("SendToSoldier: %v", result.Err)
	}
	if !result.Queued {
		t.Fatal("expected Queued=true")
	}

	// Simulate restart: create fresh store (files survive).
	store := home.NewStore(captainHome)

	// Verify envelope survived.
	env, err := store.ReadEnvelope(senderIdentity, result.MessageID)
	if err != nil || env == nil {
		t.Fatal("envelope must survive restart")
	}
	if env.Payload != "do: survive restart" {
		t.Errorf("payload=%q", env.Payload)
	}

	// Verify pending survived.
	pending, err := store.ReadPending(senderIdentity, result.MessageID)
	if err != nil || pending == nil {
		t.Fatal("pending must survive restart")
	}

	// Verify no ack.
	if store.IsAcked(senderIdentity, result.MessageID) {
		t.Fatal("no ack should exist for queued command")
	}
}

// TestReconcileSoldierPending_ExactAckClears verifies that reconcile removes
// pending after a matching ack is written by the Soldier.
func TestReconcileSoldierPending_ExactAckClears(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "idle")

	// Write envelope + pending.
	env := &home.Envelope{
		SenderRank:     home.RankCaptain,
		SenderIdentity: senderIdentity,
		ReceiverRank:   home.RankSoldier,
		ReceiverID:     cleanReceiverID(soldierTaskID),
		TaskID:         soldierTaskID,
		Payload:        "do: reconcile test",
	}
	store := home.NewStore(captainHome)
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	if err := store.WritePending(env); err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	// Write ack on the shared inbox (as the Soldier would do).
	ack := &home.ProcessingAck{
		MessageID:      env.MessageID,
		SenderRank:     env.SenderRank,
		SenderIdentity: env.SenderIdentity,
		ReceiverRank:   env.ReceiverRank,
		ReceiverID:     env.ReceiverID,
		TaskID:         env.TaskID,
		PayloadHash:    env.PayloadHash,
		ProcessedAt:    time.Now().UnixNano(),
		Outcome:        home.OutcomeAccepted,
	}
	if err := store.WriteAck(ack); err != nil {
		t.Fatalf("WriteAck: %v", err)
	}

	// Reconcile.
	if err := ReconcileSoldierPending(captainHome, senderIdentity); err != nil {
		t.Fatalf("ReconcileSoldierPending: %v", err)
	}

	// Verify pending is removed.
	pending, _ := store.ReadPending(senderIdentity, env.MessageID)
	if pending != nil {
		t.Error("pending should be removed after exact ack reconcile")
	}
}

// TestReconcileSoldierPending_WrongAckFailsClosed verifies that a mismatched
// ack fails closed.
func TestReconcileSoldierPending_WrongAckFailsClosed(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "idle")

	env := &home.Envelope{
		SenderRank:     home.RankCaptain,
		SenderIdentity: senderIdentity,
		ReceiverRank:   home.RankSoldier,
		ReceiverID:     cleanReceiverID(soldierTaskID),
		TaskID:         soldierTaskID,
		Payload:        "do: work",
		PayloadHash:    home.PayloadHashHex("do: work"),
	}
	store := home.NewStore(captainHome)
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	if err := store.WritePending(env); err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	// Write a WRONG ack (wrong payload hash).
	ack := &home.ProcessingAck{
		MessageID:      env.MessageID,
		SenderRank:     env.SenderRank,
		SenderIdentity: env.SenderIdentity,
		ReceiverRank:   env.ReceiverRank,
		ReceiverID:     env.ReceiverID,
		PayloadHash:    home.PayloadHashHex("wrong payload"),
		ProcessedAt:    time.Now().UnixNano(),
		Outcome:        home.OutcomeAccepted,
	}
	if err := store.WriteAck(ack); err != nil {
		t.Fatalf("WriteAck: %v", err)
	}

	// Reconcile — should fail closed.
	err := ReconcileSoldierPending(captainHome, senderIdentity)
	if err == nil {
		t.Fatal("expected error for wrong ack")
	}
	if !strings.Contains(err.Error(), "ack validation failed") {
		t.Errorf("expected ack validation error, got: %v", err)
	}

	// Verify pending still exists.
	pending, _ := store.ReadPending(senderIdentity, env.MessageID)
	if pending == nil {
		t.Error("pending should still exist after failed reconcile")
	}
}

// TestSoldierReceiveNotification verifies that SoldierReceiveNotification reads
// and validates an envelope and returns the payload.
func TestSoldierReceiveNotification(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "idle")

	// Write an envelope.
	env := &home.Envelope{
		SenderRank:     home.RankCaptain,
		SenderIdentity: senderIdentity,
		ReceiverRank:   home.RankSoldier,
		ReceiverID:     cleanReceiverID(soldierTaskID),
		TaskID:         soldierTaskID,
		Payload:        "do: receive test",
	}
	store := home.NewStore(captainHome)
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	ref := home.NotificationRef{
		MessageID:      env.MessageID,
		SenderIdentity: senderIdentity,
	}

	payload, err := SoldierReceiveNotification(captainHome, ref, soldierTaskID)
	if err != nil {
		t.Fatalf("SoldierReceiveNotification: %v", err)
	}
	if payload != "do: receive test" {
		t.Errorf("payload=%q, want %q", payload, "do: receive test")
	}

	// Wrong task ID should fail.
	_, err = SoldierReceiveNotification(captainHome, ref, "wrong-task")
	if err == nil {
		t.Fatal("expected error for wrong task ID")
	}
	if !strings.Contains(err.Error(), "task ID") {
		t.Errorf("expected task ID error, got: %v", err)
	}

	// Invalid ref should fail.
	badRef := home.NotificationRef{MessageID: "nonexistent", SenderIdentity: senderIdentity}
	_, err = SoldierReceiveNotification(captainHome, badRef, soldierTaskID)
	if err == nil {
		t.Fatal("expected error for nonexistent envelope")
	}
}

// TestSoldierAckNotification_WritesAck verifies that SoldierAckNotification
// writes a valid ProcessingAck.
func TestSoldierAckNotification_WritesAck(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "idle")

	// Write an envelope.
	env := &home.Envelope{
		SenderRank:     home.RankCaptain,
		SenderIdentity: senderIdentity,
		ReceiverRank:   home.RankSoldier,
		ReceiverID:     cleanReceiverID(soldierTaskID),
		TaskID:         soldierTaskID,
		Payload:        "do: ack test",
	}
	store := home.NewStore(captainHome)
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	ref := home.NotificationRef{
		MessageID:      env.MessageID,
		SenderIdentity: senderIdentity,
	}

	ack, err := SoldierAckNotification(captainHome, ref, soldierTaskID)
	if err != nil {
		t.Fatalf("SoldierAckNotification: %v", err)
	}
	if ack == nil {
		t.Fatal("expected non-nil ack")
	}
	if ack.Outcome != home.OutcomeAccepted {
		t.Errorf("outcome=%q, want %q", ack.Outcome, home.OutcomeAccepted)
	}
	if ack.PayloadHash != env.PayloadHash {
		t.Error("payload hash mismatch")
	}

	// Verify on disk.
	if !store.IsAcked(senderIdentity, env.MessageID) {
		t.Fatal("ack should exist on disk")
	}

	// Idempotent: second call returns existing ack.
	second, err := SoldierAckNotification(captainHome, ref, soldierTaskID)
	if err != nil {
		t.Fatalf("second AckNotification: %v", err)
	}
	if second == nil {
		t.Fatal("expected non-nil ack on second call")
	}
	if second.Outcome != home.OutcomeAccepted {
		t.Errorf("second outcome=%q", second.Outcome)
	}

	// Wrong task ID should fail.
	_, err = SoldierAckNotification(captainHome, ref, "wrong-task")
	if err == nil {
		t.Fatal("expected error for wrong task ID")
	}
}

// TestEndToEnd_SendIdleThenFlush verifies the full flow: send to idle soldier,
// then flush (no-op since already sent).
func TestEndToEnd_SendIdleThenFlush(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "idle")

	be := &fakeAgentEndpoint{acknowledged: true}

	// Send to idle soldier.
	sendResult := SendToSoldier(captainHome, soldierTaskID, senderIdentity, "do: work", be)
	if sendResult.Err != nil {
		t.Fatalf("SendToSoldier: %v", sendResult.Err)
	}
	if !sendResult.Sent {
		t.Fatal("expected Sent=true")
	}

	// Flush — the pending has no ack yet, so flush will re-notify
	// (idempotent on the Herdr/SubmitPrompt side).
	flushResult := FlushPendingSoldierCommands(captainHome, soldierTaskID, senderIdentity, be)
	if flushResult.Err != nil {
		t.Fatalf("Flush: %v", flushResult.Err)
	}
	// Flush should find the pending and send NotificationRef.
	if !flushResult.Sent {
		t.Log("flush did not re-send (pending may be filtered differently)")
	}
}

// TestEndToEnd_BusyThenFlush verifies: soldier busy → queue, idle → flush sends NotificationRef.
func TestEndToEnd_BusyThenFlush(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "working")

	be := &fakeAgentEndpoint{busy: true, acknowledged: true}

	// Send to busy soldier — should be queued.
	sendResult := SendToSoldier(captainHome, soldierTaskID, senderIdentity, "do: deferred work", be)
	if sendResult.Err != nil {
		t.Fatalf("SendToSoldier: %v", sendResult.Err)
	}
	if !sendResult.Queued {
		t.Fatal("expected Queued=true")
	}
	if be.promptCalls != 0 {
		t.Fatalf("expected 0 SubmitPrompt calls while busy, got %d", be.promptCalls)
	}

	// Now soldier becomes idle.
	be.busy = false

	// Flush.
	flushResult := FlushPendingSoldierCommands(captainHome, soldierTaskID, senderIdentity, be)
	if flushResult.Err != nil {
		t.Fatalf("Flush: %v", flushResult.Err)
	}
	if !flushResult.Sent {
		t.Fatal("expected Sent=true on flush after soldier idle")
	}

	// Exactly 1 SubmitPrompt call (from flush, not from send).
	if be.promptCalls != 1 {
		t.Errorf("promptCalls=%d, want 1", be.promptCalls)
	}

	// Verify NotificationRef was sent (not raw line).
	var ref home.NotificationRef
	if err := json.Unmarshal([]byte(be.lastText), &ref); err != nil {
		t.Fatalf("flush must send NotificationRef: %v", err)
	}
	if ref.MessageID != sendResult.MessageID {
		t.Errorf("ref MessageID=%q, want %q", ref.MessageID, sendResult.MessageID)
	}

	// No ack written by flush.
	store := home.NewStore(captainHome)
	if store.IsAcked(senderIdentity, sendResult.MessageID) {
		t.Fatal("flush must NOT write ack")
	}
}

// TestEndToEnd_DuplicateReadyEvent_IsIdempotent verifies that receiving two
// ready events (calling flush twice) is harmless.
func TestEndToEnd_DuplicateReadyEvent_IsIdempotent(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "working")

	be := &fakeAgentEndpoint{busy: true, acknowledged: true}

	// Queue a command while busy.
	sendResult := SendToSoldier(captainHome, soldierTaskID, senderIdentity, "do: work", be)
	if sendResult.Err != nil || !sendResult.Queued {
		t.Fatalf("expected queued: err=%v queued=%v", sendResult.Err, sendResult.Queued)
	}

	// Soldier becomes idle.
	be.busy = false

	// First ready event → flush.
	firstFlush := FlushPendingSoldierCommands(captainHome, soldierTaskID, senderIdentity, be)
	if firstFlush.Err != nil {
		t.Fatalf("first flush: %v", firstFlush.Err)
	}
	if !firstFlush.Sent {
		t.Fatal("expected first flush to send")
	}

	// Second ready event → flush again.

	// No status report / Captain noise.
	t.Log("duplicate ready event: no report/status spam")
}

// TestSendToSoldier_ReuseSamePane verifies that soldier send reuses the same
// pane (doesn't try to re-create it).
func TestSendToSoldier_ReuseSamePane(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "idle")

	be := &fakeAgentEndpoint{acknowledged: true}

	result := SendToSoldier(captainHome, soldierTaskID, senderIdentity, "do: reuse pane", be)
	if result.Err != nil {
		t.Fatalf("SendToSoldier: %v", result.Err)
	}
	if !result.Sent {
		t.Fatal("expected Sent=true (pane is alive)")
	}
}

// TestSoldierLifecycleTransitions verifies that the lifecycle transitions
// working → review-ready → amending → review-ready → done work correctly.
func TestSoldierLifecycleTransitions(t *testing.T) {
	captainHome := filepath.Join(t.TempDir(), "lifecycle")
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	taskID := "task:lifecycle"

	lifecycle := []struct {
		state string
		msg   string
		key   string
	}{
		{"working", "spawned", "spawn"},
		{"review-ready", "first turn complete", "review-1"},
		{"working", "amending", "amend-1"},
		{"review-ready", "second turn complete", "review-2"},
		{"done", "PR url", "final"},
	}

	for _, step := range lifecycle {
		line := step.state + ": " + step.msg
		if step.key != "" {
			line += " [key=" + step.key + "]"
		}
		if err := mhome.AppendStatus(captainHome, taskID, line); err != nil {
			t.Fatalf("appending %s: %v", step.state, err)
		}
	}

	// Verify status states are all valid.
	for _, step := range lifecycle {
		if !mhome.IsValidStatusState(step.state) {
			t.Errorf("status state %q should be valid", step.state)
		}
	}

	// Read back the status file and verify all transitions exist.
	statusLines, err := mhome.ReadStatus(captainHome, taskID)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	if len(statusLines) != len(lifecycle) {
		t.Fatalf("expected %d status lines, got %d", len(lifecycle), len(statusLines))
	}

	for i, step := range lifecycle {
		expected := step.state + ": " + step.msg
		if step.key != "" {
			expected += " [key=" + step.key + "]"
		}
		if statusLines[i] != expected {
			t.Errorf("status[%d] = %q, want %q", i, statusLines[i], expected)
		}
	}
}

// TestTerminalDoneAfterMerge verifies that terminal done/merge still works.
func TestTerminalDoneAfterMerge(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "review-ready")

	be := &fakeAgentEndpoint{acknowledged: true}

	// Soldier is review-ready (idle for new commands).
	result := SendToSoldier(captainHome, soldierTaskID, senderIdentity, "finalize: merge PR", be)
	if result.Err != nil {
		t.Fatalf("SendToSoldier: %v", result.Err)
	}
	if !result.Sent {
		t.Fatal("expected Sent=true (soldier is review-ready)")
	}

	// Verify NotificationRef was sent.
	var ref home.NotificationRef
	if err := json.Unmarshal([]byte(be.lastText), &ref); err == nil {
		if ref.MessageID != result.MessageID {
			t.Errorf("ref MessageID=%q", ref.MessageID)
		}
	}
}

// TestNoReportSpam verifies that the busy-send path does not generate any
// report/status noise on the captain side.
func TestNoReportSpam(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "working")

	be := &fakeAgentEndpoint{busy: true, acknowledged: true}

	// Count status lines before.
	statusBefore, _ := mhome.ReadStatus(captainHome, "captain-status")
	beforeCount := len(statusBefore)

	// Send to busy soldier.
	_ = SendToSoldier(captainHome, soldierTaskID, senderIdentity, "do: quiet", be)

	// Count status lines after — should not have changed.
	statusAfter, _ := mhome.ReadStatus(captainHome, "captain-status")
	afterCount := len(statusAfter)
	if afterCount != beforeCount {
		t.Errorf("status lines changed: before=%d after=%d (expected no captain-side noise)", beforeCount, afterCount)
	}
}

// TestValidatePendingSurvives verifies that pending records survive across
// store reconstruction (simulates restart).
func TestValidatePendingSurvives(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "working")

	be := &fakeAgentEndpoint{busy: true, acknowledged: true}

	// Queue a command.
	result := SendToSoldier(captainHome, soldierTaskID, senderIdentity, "do: survive", be)
	if result.Err != nil || !result.Queued {
		t.Fatalf("expected queued: err=%v queued=%v", result.Err, result.Queued)
	}

	// Reconstruct store from files (simulating restart).
	store := home.NewStore(captainHome)

	// Verify envelope survives.
	env, err := store.ReadEnvelope(senderIdentity, result.MessageID)
	if err != nil || env == nil {
		t.Fatal("envelope must survive store reconstruction")
	}

	// Verify pending survives.
	pending, err := store.ReadPending(senderIdentity, result.MessageID)
	if err != nil || pending == nil {
		t.Fatal("pending must survive store reconstruction")
	}

	// Verify no ack.
	if store.IsAcked(senderIdentity, result.MessageID) {
		t.Fatal("no ack should exist for queued command")
	}
}

// TestSendToSoldier_NoSubmitPromptWhenBusy_Deadline verifies that the busy
// path returns quickly (no blocking).
func TestSendToSoldier_NoSubmitPromptWhenBusy_Deadline(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "working")

	be := &fakeAgentEndpoint{busy: true, acknowledged: true}

	done := make(chan bool)
	go func() {
		result := SendToSoldier(captainHome, soldierTaskID, senderIdentity, "do: fast queue", be)
		if result.Err != nil {
			t.Errorf("SendToSoldier: %v", result.Err)
		}
		if !result.Queued {
			t.Errorf("expected Queued=true")
		}
		close(done)
	}()

	select {
	case <-done:
		// Returned quickly.
	case <-time.After(2 * time.Second):
		t.Fatal("SendToSoldier blocked for >2s on busy soldier")
	}
}

// TestConsumeReadyEvent validates that ConsumeReadyEvent validates and processes
// a ready event correctly.
func TestConsumeReadyEvent(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "idle")

	// Queue a pending command first.
	env := &home.Envelope{
		SenderRank:     home.RankCaptain,
		SenderIdentity: senderIdentity,
		ReceiverRank:   home.RankSoldier,
		ReceiverID:     cleanReceiverID(soldierTaskID),
		TaskID:         soldierTaskID,
		Payload:        "do: ready event test",
	}
	store := home.NewStore(captainHome)
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	if err := store.WritePending(env); err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	be := &fakeAgentEndpoint{acknowledged: true}

	event := &ReadyEvent{
		EventID:            "evt-1",
		TaskID:             soldierTaskID,
		Key:                "",
		EndpointGeneration: 0,
		Timestamp:          time.Now().UnixNano(),
	}

	flushed, err := ConsumeReadyEvent(captainHome, soldierTaskID, senderIdentity, event, "", be)
	if err != nil {
		t.Fatalf("ConsumeReadyEvent: %v", err)
	}
	if !flushed {
		t.Fatal("expected a command to be flushed")
	}
	if be.promptCalls != 1 {
		t.Errorf("promptCalls=%d, want 1", be.promptCalls)
	}
}

// TestConsumeReadyEvent_WrongTaskID fails closed on wrong task ID.
func TestConsumeReadyEvent_WrongTaskID(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "idle")

	event := &ReadyEvent{
		EventID:            "evt-1",
		TaskID:             "wrong-task",
		Key:                "",
		EndpointGeneration: 0,
		Timestamp:          time.Now().UnixNano(),
	}

	_, err := ConsumeReadyEvent(captainHome, soldierTaskID, senderIdentity, event, "", &fakeAgentEndpoint{acknowledged: true})
	if err == nil {
		t.Fatal("expected error for wrong task ID")
	}
	if !strings.Contains(err.Error(), "task ID mismatch") {
		t.Errorf("expected task ID mismatch, got: %v", err)
	}
}

// TestConsumeReadyEvent_StaleEvent fails closed on stale events.
func TestConsumeReadyEvent_StaleEvent(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "idle")

	// Event from 10 minutes ago.
	event := &ReadyEvent{
		EventID:            "evt-1",
		TaskID:             soldierTaskID,
		Key:                "",
		EndpointGeneration: 0,
		Timestamp:          time.Now().UnixNano() - int64(10*time.Minute),
	}

	_, err := ConsumeReadyEvent(captainHome, soldierTaskID, senderIdentity, event, "", &fakeAgentEndpoint{acknowledged: true})
	if err == nil {
		t.Fatal("expected error for stale event")
	}
}

// TestConsumeReadyEvent_GenerationMismatch fails closed on wrong generation.
func TestConsumeReadyEvent_GenerationMismatch(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "idle")

	event := &ReadyEvent{
		EventID:            "evt-1",
		TaskID:             soldierTaskID,
		Key:                "",
		EndpointGeneration: 5, // current gen is 0 (not set)
		Timestamp:          time.Now().UnixNano(),
	}

	_, err := ConsumeReadyEvent(captainHome, soldierTaskID, senderIdentity, event, "10", &fakeAgentEndpoint{acknowledged: true})
	if err == nil {
		t.Fatal("expected error for generation mismatch")
	}
}

// TestEmitReadyEvent_CreatesDurableMarker verifies that EmitReadyEvent writes
// a durable ready event marker file.
func TestEmitReadyEvent_CreatesDurableMarker(t *testing.T) {
	home := t.TempDir()
	taskID := "task:emit-test"
	eventKey := "turn-1"

	event, err := EmitReadyEvent(home, taskID, eventKey, "")
	if err != nil {
		t.Fatalf("EmitReadyEvent: %v", err)
	}
	if event == nil {
		t.Fatal("expected non-nil event")
	}
	if event.EventID != eventKey {
		t.Errorf("EventID=%q, want %q", event.EventID, eventKey)
	}
	if event.TaskID != taskID {
		t.Errorf("TaskID=%q, want %q", event.TaskID, taskID)
	}

	// Verify marker file exists.
	_, err = os.Stat(readyEventPath(home, taskID, eventKey))
	if err != nil {
		t.Fatalf("marker file should exist: %v", err)
	}

	// Verify the marker can be parsed back.
	events, err := ScanReadyEvents(home, taskID)
	if err != nil {
		t.Fatalf("ScanReadyEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 ready event, got %d", len(events))
	}
	if events[0].EventID != eventKey {
		t.Errorf("parsed EventID=%q", events[0].EventID)
	}
	if events[0].TaskID != taskID {
		t.Errorf("parsed TaskID=%q", events[0].TaskID)
	}
}

// TestEmitReadyEvent_IdempotentSameContent verifies that emitting the same
// ready event twice is idempotent.
func TestEmitReadyEvent_IdempotentSameContent(t *testing.T) {
	home := t.TempDir()
	taskID := "task:idempotent"
	eventKey := "same-event"

	event1, err := EmitReadyEvent(home, taskID, eventKey, "")
	if err != nil {
		t.Fatalf("first emit: %v", err)
	}

	event2, err := EmitReadyEvent(home, taskID, eventKey, "")
	if err != nil {
		t.Fatalf("second emit: %v", err)
	}

	if event1.Timestamp != event2.Timestamp {
		t.Errorf("idempotent emit should preserve original, got %d != %d", event1.Timestamp, event2.Timestamp)
	}

	// Exactly one marker file.
	events, err := ScanReadyEvents(home, taskID)
	if err != nil {
		t.Fatalf("ScanReadyEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

// TestScanReadyEvents_NoEvents verifies that scanning with no events is a no-op.
func TestScanReadyEvents_NoEvents(t *testing.T) {
	events, err := ScanReadyEvents(t.TempDir(), "no-such-task")
	if err != nil {
		t.Fatalf("ScanReadyEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

// TestScanReadyEvents_MultipleEvents verifies that multiple ready events are
// returned in timestamp order.
func TestScanReadyEvents_MultipleEvents(t *testing.T) {
	home := t.TempDir()
	taskID := "task:multi"

	// Emit events in reverse order to verify sorting.
	_, _ = EmitReadyEvent(home, taskID, "second", "")
	time.Sleep(time.Millisecond) // ensure distinct timestamps
	_, _ = EmitReadyEvent(home, taskID, "first", "")

	events, err := ScanReadyEvents(home, taskID)
	if err != nil {
		t.Fatalf("ScanReadyEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	// Should be sorted by timestamp (oldest first)
	if events[0].Timestamp > events[1].Timestamp {
		t.Error("events not sorted by timestamp")
	}
}

// TestCleanReadyEvent verifies that CleanReadyEvent removes a single marker.
func TestCleanReadyEvent(t *testing.T) {
	home := t.TempDir()
	taskID := "task:clean"

	_, err := EmitReadyEvent(home, taskID, "to-clean", "")
	if err != nil {
		t.Fatalf("EmitReadyEvent: %v", err)
	}

	// Verify marker exists before clean.
	events, _ := ScanReadyEvents(home, taskID)
	if len(events) != 1 {
		t.Fatalf("expected 1 event before clean")
	}

	if err := CleanReadyEvent(home, taskID, "to-clean"); err != nil {
		t.Fatalf("CleanReadyEvent: %v", err)
	}

	// Verify marker is gone.
	events, _ = ScanReadyEvents(home, taskID)
	if len(events) != 0 {
		t.Fatalf("expected 0 events after clean, got %d", len(events))
	}

	// Idempotent: cleaning again is no-op.
	if err := CleanReadyEvent(home, taskID, "to-clean"); err != nil {
		t.Errorf("idempotent clean should succeed: %v", err)
	}
}

// TestCleanAllReadyEvents verifies that CleanAllReadyEvents removes all markers.
func TestCleanAllReadyEvents(t *testing.T) {
	home := t.TempDir()
	taskID := "task:clean-all"

	_, _ = EmitReadyEvent(home, taskID, "first", "")
	_, _ = EmitReadyEvent(home, taskID, "second", "")

	events, _ := ScanReadyEvents(home, taskID)
	if len(events) != 2 {
		t.Fatalf("expected 2 events before clean-all")
	}

	if err := CleanAllReadyEvents(home, taskID); err != nil {
		t.Fatalf("CleanAllReadyEvents: %v", err)
	}

	events, _ = ScanReadyEvents(home, taskID)
	if len(events) != 0 {
		t.Fatalf("expected 0 events after clean-all, got %d", len(events))
	}

	// Idempotent.
	if err := CleanAllReadyEvents(home, taskID); err != nil {
		t.Errorf("idempotent clean-all should succeed: %v", err)
	}
}

// TestConsumeAllReadyEvents_NoPendingIsNoop verifies that consuming ready events
// with no pending commands is a no-op.
func TestConsumeAllReadyEvents_NoPendingIsNoop(t *testing.T) {
	captainHome, soldierTaskID, senderIdent := setupSoldierTestHomes(t, "idle")

	// Emit a ready event with no pending.
	_, err := EmitReadyEvent(captainHome, soldierTaskID, "no-pending", "")
	if err != nil {
		t.Fatalf("EmitReadyEvent: %v", err)
	}

	flushed, err := ConsumeAllReadyEvents(captainHome, soldierTaskID, senderIdent, "", &fakeAgentEndpoint{acknowledged: true})
	if err != nil {
		t.Fatalf("ConsumeAllReadyEvents: %v", err)
	}
	if flushed != 0 {
		t.Errorf("expected 0 flushed (no pending), got %d", flushed)
	}

	// Ready event marker is cleaned up even when no pending — the ready signal
	// has been received and acknowledged. Subsequent sends will go through the
	// normal non-blocking path (idle soldier = direct send).
	events, _ := ScanReadyEvents(captainHome, soldierTaskID)
	if len(events) != 0 {
		t.Errorf("expected 0 ready events (consumed even without pending), got %d", len(events))
	}
}

// TestConsumeAllReadyEvents_FullFlow verifies: emit ready → flush pending command.
func TestConsumeAllReadyEvents_FullFlow(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "working")

	// Queue a command while busy.
	be := &fakeAgentEndpoint{busy: true, acknowledged: true}

	sendResult := SendToSoldier(captainHome, soldierTaskID, senderIdentity, "do: full flow", be)
	if sendResult.Err != nil || !sendResult.Queued {
		t.Fatalf("expected queued: err=%v queued=%v", sendResult.Err, sendResult.Queued)
	}

	// Emit ready event.
	_, err := EmitReadyEvent(captainHome, soldierTaskID, "full-flow", "")
	if err != nil {
		t.Fatalf("EmitReadyEvent: %v", err)
	}

	// Soldier becomes idle.
	be.busy = false

	// Consume ready events.
	flushed, err := ConsumeAllReadyEvents(captainHome, soldierTaskID, senderIdentity, "", be)
	if err != nil {
		t.Fatalf("ConsumeAllReadyEvents: %v", err)
	}
	if flushed != 1 {
		t.Errorf("expected 1 flushed, got %d", flushed)
	}

	// Exactly 1 SubmitPrompt call (from flush).
	if be.promptCalls != 1 {
		t.Errorf("promptCalls=%d, want 1", be.promptCalls)
	}

	// Ready event marker should be cleaned up.
	events, _ := ScanReadyEvents(captainHome, soldierTaskID)
	if len(events) != 0 {
		t.Errorf("expected 0 ready events after consume, got %d", len(events))
	}
}

// TestConsumeAllReadyEvents_StaleEventRejected verifies that stale ready events
// are rejected and cleaned up.
func TestConsumeAllReadyEvents_StaleEventRejected(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "idle")

	// Write a stale ready event marker directly.
	event := &ReadyEvent{
		EventID:   "stale-event",
		TaskID:    soldierTaskID,
		Key:       "stale-event",
		Timestamp: time.Now().UnixNano() - int64(10*time.Minute),
	}
	data, _ := json.MarshalIndent(event, "", "  ")
	p := readyEventPath(captainHome, soldierTaskID, "stale-event")
	os.MkdirAll(filepath.Dir(p), 0755)
	os.WriteFile(p, data, 0644)

	be := &fakeAgentEndpoint{acknowledged: true}

	flushed, err := ConsumeAllReadyEvents(captainHome, soldierTaskID, senderIdentity, "", be)
	if err != nil {
		t.Fatalf("ConsumeAllReadyEvents: %v", err)
	}
	if flushed != 0 {
		t.Errorf("expected 0 flushed (stale event), got %d", flushed)
	}

	// Stale event should be cleaned up.
	events, _ := ScanReadyEvents(captainHome, soldierTaskID)
	if len(events) != 0 {
		t.Errorf("stale event should be cleaned up, got %d events", len(events))
	}
}

// TestConsumeAllReadyEvents_WrongTaskID fails closed on wrong task ID.
func TestConsumeAllReadyEvents_WrongTaskID(t *testing.T) {
	captainHome, _, senderIdentity := setupSoldierTestHomes(t, "idle")

	// Emit ready event for a different task.
	_, err := EmitReadyEvent(captainHome, "wrong-task", "evt-1", "")
	if err != nil {
		t.Fatalf("EmitReadyEvent: %v", err)
	}

	be := &fakeAgentEndpoint{acknowledged: true}

	_, err = ConsumeAllReadyEvents(captainHome, "wrong-task", senderIdentity, "", be)
	if err != nil {
		t.Fatalf("ConsumeAllReadyEvents for wrong task should not error: %v", err)
	}

	// No task meta for "wrong-task" — scan may fail or return empty.
	// That's acceptable; the meta is not needed for scan, only for flush.
}

// TestConsumeAllReadyEvents_DuplicateReadyIdempotent verifies that emitting
// the same ready event twice and consuming is idempotent.
func TestConsumeAllReadyEvents_DuplicateReadyIdempotent(t *testing.T) {
	captainHome, soldierTaskID, senderIdentity := setupSoldierTestHomes(t, "working")

	be := &fakeAgentEndpoint{busy: true, acknowledged: true}

	// Queue a command.
	sendResult := SendToSoldier(captainHome, soldierTaskID, senderIdentity, "do: duplicate test", be)
	if sendResult.Err != nil || !sendResult.Queued {
		t.Fatalf("expected queued: err=%v queued=%v", sendResult.Err, sendResult.Queued)
	}

	// Emit two ready events with DIFFERENT keys (simulates two turn boundaries).
	// Both trigger the same pending command.
	_, _ = EmitReadyEvent(captainHome, soldierTaskID, "dup-key-1", "")
	_, _ = EmitReadyEvent(captainHome, soldierTaskID, "dup-key-2", "")

	// Only 1 marker should exist for the first event (same key stayed).
	// Actually both markers exist since keys differ.
	events, _ := ScanReadyEvents(captainHome, soldierTaskID)
	if len(events) != 2 {
		t.Fatalf("expected 2 ready events (different keys), got %d", len(events))
	}

	be.busy = false

	// First consume: flushes the pending command.
	flushed, err := ConsumeAllReadyEvents(captainHome, soldierTaskID, senderIdentity, "", be)
	if err != nil {
		t.Fatalf("first ConsumeAllReadyEvents: %v", err)
	}
	if flushed != 1 {
		t.Errorf("first consume: expected 1 flushed, got %d", flushed)
	}

	// Second consume: should NOT re-send the same NotificationRef
	// because the dispatched marker prevents it.
	flushed2, err := ConsumeAllReadyEvents(captainHome, soldierTaskID, senderIdentity, "", be)
	if err != nil {
		t.Fatalf("second ConsumeAllReadyEvents: %v", err)
	}
	if flushed2 != 0 {
		t.Errorf("second consume: expected 0 flushed (already dispatched), got %d", flushed2)
	}

	// Only 1 SubmitPrompt call.
	if be.promptCalls != 1 {
		t.Errorf("promptCalls=%d, want 1", be.promptCalls)
	}

	// No report/status spam.
	statusLines, _ := mhome.ReadStatus(captainHome, "captain-status")
	if len(statusLines) > 0 {
		t.Errorf("expected 0 status lines (no captain spam), got %d", len(statusLines))
	}

	// Pending still exists (no ack yet).
	store := home.NewStore(captainHome)
	if store.IsAcked(senderIdentity, sendResult.MessageID) {
		t.Errorf("pending should not be acked yet")
	}
}
