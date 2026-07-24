package captain

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/mailbox"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

// SendToSoldierResult describes the outcome of sending a command to a soldier.
type SendToSoldierResult struct {
	MessageID string
	Queued    bool // true when the soldier was busy and the command was queued
	Sent      bool // true when the soldier was idle and the notification ref was sent
	Err       error
}

// IsEndpointBusy checks whether the soldier endpoint is busy processing
// (agent status is "working" or similar). Returns false, nil when the
// endpoint is idle/ready. Returns true, nil when busy. Returns false, err
// when the status cannot be determined.
//
// Detection order:
//  1. Backend implements session.BusyChecker → use AgentBusy.
//  2. Backend is *session.HerdrBackend → use IsRecognizedAgent.
//  3. Otherwise → assume not busy. Unknown status fails closed.
func IsEndpointBusy(bk session.Backend, windowID string) (bool, error) {
	// 1. Check for BusyChecker interface.
	if bc, ok := bk.(session.BusyChecker); ok {
		busy, err := bc.AgentBusy(windowID)
		if err != nil {
			return false, fmt.Errorf("busy check failed: %w", err)
		}
		return busy, nil
	}

	// 2. Check for HerdrBackend specifically.
	if hb, ok := bk.(*session.HerdrBackend); ok {
		recognized, status := hb.IsRecognizedAgent(windowID)
		if !recognized {
			if !bk.Alive(windowID) {
				return false, fmt.Errorf("endpoint not alive")
			}
			// Alive but not recognized — unknown, fail closed.
			return false, fmt.Errorf("endpoint status unknown: not a recognized agent")
		}
		switch status {
		case "working":
			return true, nil
		case "idle", "ready", "review-ready":
			return false, nil
		default:
			// Unknown status: fail closed.
			return false, fmt.Errorf("endpoint status unknown: %q", status)
		}
	}

	// 3. Non-Herdr backends without BusyChecker — assume not busy.
	return false, nil
}

// cleanReceiverID sanitizes an identifier for use as mailbox ReceiverID.
// Task IDs often contain colons (e.g., "task:test-1") which are rejected
// by ValidatePathComponent. Since ReceiverID is not used in file paths
// (paths use SenderIdentity + MessageID), we replace unsafechars with
// underscores.
func cleanReceiverID(raw string) string {
	return strings.NewReplacer(":", "_", "/", "_", "\\", "_", "..", "_").Replace(raw)
}

// SendToSoldier sends a command to a soldier using the mailbox envelope pattern.
//
// Flow:
//  1. Persist the command as a mailbox Envelope in the shared home inbox
//     (state/.inbox/<sender-identity>/<msg-id>.json)
//  2. Write a pending record in the sender's outbox
//     (state/.outbox/<sender-identity>/<msg-id>.pending)
//  3. Check if the soldier endpoint is busy
//  4. If not busy: send NotificationRef via SubmitPrompt (NOT the raw line)
//  5. If busy: return queued (no SubmitPrompt, no ack)
//
// IMPORTANT: No ProcessingAck is written here. The ack is written only by the
// Soldier agent when it accepts the command into context via inbox ack.
// The pending record is retained until reconciled against a matching ack.
//
// senderHome is the captain home directory (same as soldierHome; the shared
// home where state/.inbox and state/.outbox live).
// soldierTaskID is the task identifier (MUNSU_TASK_ID).
// senderIdentity is the captain's identity (from ReadHomeIdentity or basename).
// line is the command text to send.
func SendToSoldier(senderHome, soldierTaskID, senderIdentity, line string) *SendToSoldierResult {
	result := &SendToSoldierResult{}

	// 1. Read soldier task meta for window and backend.
	meta, err := task.ReadMeta(senderHome, soldierTaskID)
	if err != nil {
		result.Err = fmt.Errorf("reading soldier meta: %w", err)
		return result
	}
	windowID := meta["window"]
	if windowID == "" {
		result.Err = fmt.Errorf("soldier %s has no window endpoint", soldierTaskID)
		return result
	}

	// 2. Create the mailbox Envelope (captain→soldier).
	// ReceiverID is the sanitized soldier task ID; ReceiverRank is RankSoldier.
	receiverID := cleanReceiverID(soldierTaskID)
	env := &mailbox.Envelope{
		SenderRank:     mailbox.RankCaptain,
		SenderIdentity: senderIdentity,
		ReceiverRank:   mailbox.RankSoldier,
		ReceiverID:     receiverID, // sanitized task ID
		TaskID:         soldierTaskID,
		Payload:        line,
	}

	// 3. Write envelope to the shared inbox (state/.inbox/<sender>/<msg-id>.json).
	store := mailbox.NewStore(senderHome)
	if err := store.WriteEnvelope(env); err != nil {
		result.Err = fmt.Errorf("writing inbox envelope: %w", err)
		return result
	}
	result.MessageID = env.MessageID

	// 4. Write pending (state/.outbox/<sender>/<msg-id>.pending).
	if err := store.WritePending(env); err != nil {
		result.Err = fmt.Errorf("writing pending: %w", err)
		return result
	}

	// 5. Resolve backend and check busy status.
	bk, _, err := backendForTask(senderHome, meta)
	if err != nil {
		result.Err = fmt.Errorf("resolving backend: %w", err)
		return result
	}

	busy, busyErr := IsEndpointBusy(bk, windowID)
	if busyErr != nil {
		// Unknown status: fail closed, retain pending.
		result.Err = fmt.Errorf("checking soldier status: %w", busyErr)
		return result
	}

	if busy {
		// 6a. Soldier is busy — return queued. Pending is retained.
		result.Queued = true
		return result
	}

	// 6b. Soldier is idle — send NotificationRef via SubmitPrompt.
	ref := mailbox.NotificationRef{
		MessageID:      env.MessageID,
		SenderIdentity: senderIdentity,
	}
	refText := ref.Encode()

	promptResult := session.SubmitPrompt(bk, windowID, refText)
	if !promptResult.Acknowledged() {
		// Not acknowledged — pending retained for retry.
		result.Err = fmt.Errorf("send not acknowledged (status=%s)", promptResult.Status)
		return result
	}

	result.Sent = true
	return result
}

// FlushPendingSoldierCommands reads pending envelopes for a specific soldier
// and sends the oldest still-unacked command via NotificationRef SubmitPrompt.
//
// Flow:
//  1. List pending for sender identity
//  2. Filter to only envelopes targeting this soldier (ReceiverID == soldierTaskID)
//  3. Skip if already acked (remove pending via reconcile)
//  4. Check if soldier is busy — if yes, retain pending
//  5. Send NotificationRef via SubmitPrompt
//  6. NO ProcessingAck written here
//
// Only one command is flushed per call (FIFO order).
// Idempotent: calling with no pending or all acked is a no-op.
func FlushPendingSoldierCommands(senderHome, soldierTaskID, senderIdentity string) *SendToSoldierResult {
	result := &SendToSoldierResult{}

	// Read pending envelopes for this sender.
	store := mailbox.NewStore(senderHome)
	pending, err := store.ListPending(senderIdentity)
	if err != nil {
		result.Err = fmt.Errorf("listing pending: %w", err)
		return result
	}

	// Filter to only pending targeting this soldier (ReceiverID matches).
	receiverID := cleanReceiverID(soldierTaskID)
	var targetEnv *mailbox.Envelope
	for _, env := range pending {
		if env.ReceiverID == receiverID {
			targetEnv = env
			break
		}
	}
	if targetEnv == nil {
		return result // no pending for this soldier
	}

	// Read soldier meta for window/backend.
	meta, err := task.ReadMeta(senderHome, soldierTaskID)
	if err != nil {
		result.Err = fmt.Errorf("reading soldier meta: %w", err)
		return result
	}
	windowID := meta["window"]
	if windowID == "" {
		result.Err = fmt.Errorf("soldier %s has no window endpoint", soldierTaskID)
		return result
	}

	result.MessageID = targetEnv.MessageID

	// Resolve backend and check if endpoint is alive + not busy.
	bk, _, err := backendForTask(senderHome, meta)
	if err != nil {
		result.Err = fmt.Errorf("resolving backend: %w", err)
		return result
	}

	busy, busyErr := IsEndpointBusy(bk, windowID)
	if busyErr != nil {
		result.Err = fmt.Errorf("checking soldier status: %w", busyErr)
		return result
	}
	if busy {
		result.Queued = true
		return result // still busy, retain pending
	}

	// Already acked? If so, remove pending and return.
	if store.IsAcked(senderIdentity, targetEnv.MessageID) {
		ack, ackErr := store.ReadAck(senderIdentity, targetEnv.MessageID)
		if ackErr == nil && ack != nil {
			_ = store.RemovePendingAfterAck(senderIdentity, targetEnv.MessageID, ack)
		}
		return result
	}

	// Send NotificationRef via SubmitPrompt.
	ref := mailbox.NotificationRef{
		MessageID:      targetEnv.MessageID,
		SenderIdentity: senderIdentity,
	}
	refText := ref.Encode()

	promptResult := session.SubmitPrompt(bk, windowID, refText)
	if !promptResult.Acknowledged() {
		result.Err = fmt.Errorf("flush not acknowledged (status=%s)", promptResult.Status)
		return result
	}

	result.Sent = true
	return result
}

// ReconcileSoldierPending checks for acks on pending envelopes and removes
// matching pending records when a valid ack exists.
func ReconcileSoldierPending(senderHome, senderIdentity string) error {
	store := mailbox.NewStore(senderHome)

	pending, err := store.ListPending(senderIdentity)
	if err != nil {
		return fmt.Errorf("listing pending: %w", err)
	}
	for _, env := range pending {
		ack, ackErr := store.ReadAck(env.SenderIdentity, env.MessageID)
		if ackErr != nil {
			return fmt.Errorf("reading ack for %s: %w", env.MessageID, ackErr)
		}
		if ack == nil {
			continue // not yet acked
		}
		if err := store.RemovePendingAfterAck(senderIdentity, env.MessageID, ack); err != nil {
			return fmt.Errorf("removing pending for %s: %w", env.MessageID, err)
		}
	}
	return nil
}

// SoldierInboxPath returns the path to the inbox directory for a sender.
func SoldierInboxPath(senderHome, senderIdentity string) string {
	return filepath.Join(senderHome, "state", mailbox.InboxDir, senderIdentity)
}

// SoldierOutboxPath returns the path to the pending records directory for a sender.
func SoldierOutboxPath(senderHome, senderIdentity string) string {
	return filepath.Join(senderHome, "state", mailbox.OutboxDir, senderIdentity)
}

// SoldierReceiveNotification reads and validates an envelope from the shared
// inbox using a NotificationRef. The caller (typically a soldier agent) provides
// the expected TaskID and RankSoldier receiver identity validation.
//
// Returns the envelope payload. Writes NO ack.
// Returns an error if the envelope is not found or validation fails.
func SoldierReceiveNotification(senderHome string, ref mailbox.NotificationRef, expectedTaskID string) (string, error) {
	if err := ref.Validate(); err != nil {
		return "", fmt.Errorf("invalid ref: %w", err)
	}

	store := mailbox.NewStore(senderHome)
	env, err := store.ReadEnvelope(ref.SenderIdentity, ref.MessageID)
	if err != nil {
		return "", fmt.Errorf("reading envelope: %w", err)
	}
	if env == nil {
		return "", fmt.Errorf("envelope not found: sender=%s msg=%s", ref.SenderIdentity, ref.MessageID)
	}

	// Validate the envelope.
	if err := mailbox.ValidateEnvelope(env); err != nil {
		return "", fmt.Errorf("invalid envelope: %w", err)
	}

	// Validate it targets this soldier's task.
	if env.TaskID != expectedTaskID {
		return "", fmt.Errorf("envelope task ID %q does not match expected %q", env.TaskID, expectedTaskID)
	}
	if env.ReceiverRank != mailbox.RankSoldier {
		return "", fmt.Errorf("envelope receiver rank %q, expected soldier", env.ReceiverRank)
	}
	if env.SenderIdentity != ref.SenderIdentity {
		return "", fmt.Errorf("sender identity mismatch: envelope %q vs ref %q", env.SenderIdentity, ref.SenderIdentity)
	}
	if env.MessageID != ref.MessageID {
		return "", fmt.Errorf("message ID mismatch: envelope %q vs ref %q", env.MessageID, ref.MessageID)
	}

	return env.Payload, nil
}

// SoldierAckNotification writes a ProcessingAck (accepted) for the given
// NotificationRef on the soldier side (shared home inbox).
//
// Validates that the envelope exists, matches expected task ID, and writes
// an "accepted" ack. Idempotent: calling with the same ref returns the
// existing ack with preserved timestamp.
func SoldierAckNotification(senderHome string, ref mailbox.NotificationRef, expectedTaskID string) (*mailbox.ProcessingAck, error) {
	if err := ref.Validate(); err != nil {
		return nil, fmt.Errorf("invalid ref: %w", err)
	}

	store := mailbox.NewStore(senderHome)
	env, err := store.ReadEnvelope(ref.SenderIdentity, ref.MessageID)
	if err != nil {
		return nil, fmt.Errorf("reading envelope: %w", err)
	}
	if env == nil {
		return nil, fmt.Errorf("envelope not found: sender=%s msg=%s", ref.SenderIdentity, ref.MessageID)
	}

	if err := mailbox.ValidateEnvelope(env); err != nil {
		return nil, fmt.Errorf("invalid envelope: %w", err)
	}
	if env.TaskID != expectedTaskID {
		return nil, fmt.Errorf("envelope task ID %q does not match expected %q", env.TaskID, expectedTaskID)
	}

	// Check for existing ack (idempotent).
	existing, err := store.ReadAck(ref.SenderIdentity, ref.MessageID)
	if err != nil {
		return nil, fmt.Errorf("reading existing ack: %w", err)
	}
	if existing != nil {
		if existing.Outcome == mailbox.OutcomeAccepted {
			return existing, nil // idempotent
		}
		return nil, fmt.Errorf("ack conflict: existing outcome %q", existing.Outcome)
	}

	ack := &mailbox.ProcessingAck{
		MessageID:      env.MessageID,
		SenderRank:     env.SenderRank,
		SenderIdentity: env.SenderIdentity,
		ReceiverRank:   env.ReceiverRank,
		ReceiverID:     env.ReceiverID,
		TaskID:         env.TaskID,
		Key:            env.Key,
		PayloadHash:    env.PayloadHash,
		ProcessedAt:    time.Now().UnixNano(),
		Outcome:        mailbox.OutcomeAccepted,
	}
	if err := store.WriteAck(ack); err != nil {
		return nil, fmt.Errorf("writing ack: %w", err)
	}
	return ack, nil
}

// SoldierIsAcked checks whether a specific message has been acknowledged.
func SoldierIsAcked(senderHome, senderIdentity, messageID string) bool {
	return mailbox.NewStore(senderHome).IsAcked(senderIdentity, messageID)
}

// ReadyEvent carries the durable ready signal from a soldier.
type ReadyEvent struct {
	EventID           string `json:"event_id"`
	TaskID            string `json:"task_id"`
	Key               string `json:"key"`
	EndpointGeneration int64 `json:"endpoint_generation"`
	Timestamp         int64  `json:"timestamp"`
}

// ValidateReadyEvent verifies that a ready event is not stale, matches the
// expected task/key, and has a valid endpoint generation against the current
// window metadata. Returns an error on any mismatch (fail closed).
func ValidateReadyEvent(event *ReadyEvent, curTaskID, curKey, metaGeneration string) error {
	if event == nil {
		return fmt.Errorf("ready event: nil")
	}
	if event.EventID == "" {
		return fmt.Errorf("ready event: empty event ID")
	}
	if event.TaskID != curTaskID {
		return fmt.Errorf("ready event: task ID mismatch %q != %q", event.TaskID, curTaskID)
	}
	if event.Key != curKey {
		return fmt.Errorf("ready event: key mismatch %q != %q", event.Key, curKey)
	}

	// Validate endpoint generation (fail closed on mismatch).
	if metaGeneration != "" {
		var gen int64
		if _, err := fmt.Sscanf(metaGeneration, "%d", &gen); err == nil && gen != 0 {
			if event.EndpointGeneration != gen {
				return fmt.Errorf("ready event: generation mismatch %d != %d (stale event)", event.EndpointGeneration, gen)
			}
		}
	}

	// Reject stale events (more than 5 minutes old).
	now := time.Now().UnixNano()
	if event.Timestamp > 0 && now-event.Timestamp > int64(5*time.Minute) {
		return fmt.Errorf("ready event: stale (age=%dns)", now-event.Timestamp)
	}

	return nil
}

// Encode serializes ReadyEvent to canonical JSON.
func (e *ReadyEvent) Encode() string {
	data, _ := json.Marshal(e)
	return string(data)
}

// ParseReadyEvent deserializes a ReadyEvent from JSON.
func ParseReadyEvent(s string) (*ReadyEvent, error) {
	var e ReadyEvent
	if err := json.Unmarshal([]byte(s), &e); err != nil {
		return nil, fmt.Errorf("parse ready event: %w", err)
	}
	if e.EventID == "" {
		return nil, fmt.Errorf("ready event: empty event ID after parse")
	}
	return &e, nil
}

// ConsumeReadyEvent validates and processes a ready event.
// Returns true if a pending command was flushed, false if no pending existed.
// Returns error if the event is invalid or the flush failed.
func ConsumeReadyEvent(senderHome, soldierTaskID, senderIdentity string, event *ReadyEvent, metaGeneration string) (bool, error) {
	meta, err := task.ReadMeta(senderHome, soldierTaskID)
	if err != nil {
		return false, fmt.Errorf("reading soldier meta: %w", err)
	}

	if err := ValidateReadyEvent(event, soldierTaskID, meta["key"], metaGeneration); err != nil {
		return false, fmt.Errorf("ready event validation: %w", err)
	}

	result := FlushPendingSoldierCommands(senderHome, soldierTaskID, senderIdentity)
	if result.Err != nil {
		return false, fmt.Errorf("flush after ready event: %w", result.Err)
	}

	return result.Sent, nil
}
