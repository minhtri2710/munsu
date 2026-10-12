package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

// SendToSoldierResult describes the outcome of sending a command to a soldier.
type SendToSoldierResult struct {
	MessageID string
	Queued    bool // true when the soldier was busy and the command was queued
	Sent      bool // true when the soldier was idle and the notification ref was sent
	Err       error
}

type SoldierEndpointCapabilities interface {
	Busy(home string, meta map[string]string) (bool, error)
	Send(home string, meta map[string]string, payload string) home.BoundSendResult
}

// SendToSoldier sends a command to a soldier using the mailbox envelope pattern.
//
// Flow:
//  1. Persist the command as a mailbox Envelope in the shared home inbox
//     (state/.inbox/<sender-identity>/<msg-id>.json)
//  2. Write a pending record in the sender's outbox
//     (state/.outbox/<sender-identity>/<msg-id>.pending)
//  3. Check if the soldier endpoint is busy
//  4. If not busy: send NotificationRef through the endpoint capability (NOT the raw line)
//  5. If busy: return queued (no notification, no ack)
//
// IMPORTANT: No ProcessingAck is written here. The ack is written only by the
// Soldier agent when it accepts the command into context via inbox ack.
// The pending record is retained until reconciled against a matching ack.
//
// senderHome is the General or Captain home that owns the soldier and stores
// the shared state/.inbox and state/.outbox records.
// soldierTaskID is the task identifier (MUNSU_TASK_ID).
// senderIdentity identifies the dispatching home in the envelope and outbox.
// The envelope SenderRank is read from senderHome's durable identity rather
// than inferred from senderIdentity.
// line is the command text to send.
func SendToSoldier(senderHome, soldierTaskID, senderIdentity, line string, endpoint SoldierEndpointCapabilities) *SendToSoldierResult {
	result := &SendToSoldierResult{}

	// 1. Read soldier task meta for window and backend.
	meta, err := home.ReadMeta(senderHome, soldierTaskID)
	if err != nil {
		result.Err = fmt.Errorf("reading soldier meta: %w", err)
		return result
	}
	windowID := meta["window"]
	if windowID == "" {
		result.Err = fmt.Errorf("soldier %s has no window endpoint", soldierTaskID)
		return result
	}

	// 2. Create the mailbox Envelope (sender→soldier).
	// ReceiverID is the sanitized soldier task ID; ReceiverRank is RankSoldier.
	_, senderRank, err := home.ReadHomeIdentity(senderHome)
	if err != nil {
		result.Err = fmt.Errorf("reading sender home identity: %w", err)
		return result
	}
	receiverID := home.ReceiverIDForTask(soldierTaskID)
	env := &home.Envelope{
		SenderRank:     senderRank,
		SenderIdentity: senderIdentity,
		ReceiverRank:   home.RankSoldier,
		ReceiverID:     receiverID, // sanitized task ID
		TaskID:         soldierTaskID,
		Payload:        line,
	}

	// 3. Write envelope to the shared inbox (state/.inbox/<sender>/<msg-id>.json).
	store := home.NewStore(senderHome)
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

	// 5. Check endpoint readiness after durable publication.
	if endpoint == nil {
		result.Err = fmt.Errorf("soldier endpoint capability is required")
		return result
	}
	busy, busyErr := endpoint.Busy(senderHome, meta)
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

	// 6b. Soldier is idle — send NotificationRef through the endpoint capability.
	ref := home.NotificationRef{
		MessageID:      env.MessageID,
		SenderIdentity: senderIdentity,
	}
	refText := ref.Encode()

	promptResult := endpoint.Send(senderHome, meta, refText)
	if !promptResult.Acknowledged {
		// Not acknowledged — pending retained for retry.
		result.Err = fmt.Errorf("send not acknowledged (status=%s)", promptResult.Status)
		return result
	}

	result.Sent = true
	return result
}

// FlushPendingSoldierCommands reads pending envelopes for a specific soldier
// and sends the oldest still-unacked command through the endpoint capability.
//
// Flow:
//  1. ListCaptains pending for sender identity
//  2. Filter to only envelopes targeting this soldier (ReceiverID == soldierTaskID)
//  3. Skip if already acked (remove pending via reconcile)
//  4. Check if soldier is busy — if yes, retain pending
//  5. Send NotificationRef through the endpoint capability
//  6. NO ProcessingAck written here
//
// Only one command is flushed per call (FIFO order).
// Idempotent: calling with no pending or all acked is a no-op.
func FlushPendingSoldierCommands(senderHome, soldierTaskID, senderIdentity string, endpoint SoldierEndpointCapabilities) *SendToSoldierResult {
	result := &SendToSoldierResult{}

	// Read pending envelopes for this sender.
	store := home.NewStore(senderHome)
	pending, err := store.ListPending(senderIdentity)
	if err != nil {
		result.Err = fmt.Errorf("listing pending: %w", err)
		return result
	}

	// Filter to only pending targeting this exact soldier task.
	var targetEnv *home.Envelope
	for _, env := range pending {
		if matchesSoldierTask(env, soldierTaskID) {
			targetEnv = env
			break
		}
	}
	if targetEnv == nil {
		return result // no pending for this soldier
	}

	// Read soldier meta for window/backend.
	meta, err := home.ReadMeta(senderHome, soldierTaskID)
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

	// Check endpoint readiness through the bound capability.
	if endpoint == nil {
		result.Err = fmt.Errorf("soldier endpoint capability is required")
		return result
	}
	busy, busyErr := endpoint.Busy(senderHome, meta)
	if busyErr != nil {
		result.Err = fmt.Errorf("checking soldier status: %w", busyErr)
		return result
	}
	if busy {
		result.Queued = true
		return result // still busy, retain pending
	}

	// Already acked? If so, remove pending and return.
	if ack, ackErr := store.ReadAck(targetEnv); ackErr == nil && ack != nil {
		if store.RemovePendingAfterAck(senderIdentity, targetEnv.MessageID, ack) == nil {
			store.RemoveAckedPayload(targetEnv)
		}
		return result
	}

	// Send NotificationRef through the endpoint capability.
	ref := home.NotificationRef{
		MessageID:      targetEnv.MessageID,
		SenderIdentity: senderIdentity,
	}
	refText := ref.Encode()

	promptResult := endpoint.Send(senderHome, meta, refText)
	if !promptResult.Acknowledged {
		result.Err = fmt.Errorf("flush not acknowledged (status=%s)", promptResult.Status)
		return result
	}

	result.Sent = true
	return result
}

// ReconcileSoldierPending checks for acks on pending envelopes and removes
// matching pending records when a valid ack exists.
func ReconcileSoldierPending(senderHome, senderIdentity string) error {
	store := home.NewStore(senderHome)
	// resolve taskID from env.ReceiverID... but ReceiverID is sanitized.
	// We store the original TaskID in the envelope, so we can read it.

	pending, err := store.ListPending(senderIdentity)
	if err != nil {
		return fmt.Errorf("listing pending: %w", err)
	}
	for _, env := range pending {
		ack, ackErr := store.ReadAck(env)
		if ackErr != nil {
			return fmt.Errorf("reading ack for %s: %w", env.MessageID, ackErr)
		}
		if ack == nil {
			continue // not yet acked
		}
		if err := store.RemovePendingAfterAck(senderIdentity, env.MessageID, ack); err != nil {
			return fmt.Errorf("removing pending for %s: %w", env.MessageID, err)
		}
		store.RemoveAckedPayload(env)
		// Clean up dispatched marker if the pending was acked.
		if env.TaskID != "" {
			_ = cleanDispatched(senderHome, env.TaskID, env.MessageID)
		}
	}
	return nil
}

func matchesSoldierTask(env *home.Envelope, taskID string) bool {
	return env != nil &&
		env.ReceiverRank == home.RankSoldier &&
		env.ReceiverID == home.ReceiverIDForTask(taskID) &&
		env.TaskID == taskID
}

// ConsumeReady flushes one pending command to a soldier whose last status
// line is review-ready: the soldier's own report is its ready signal. It
// returns the number of commands flushed (0 or 1).
//
// This is the captain-side entry point. Idempotent: a soldier that is not
// review-ready, no pending command, or a command already dispatched and
// awaiting its ack is a no-op.
func ConsumeReady(senderHome, soldierTaskID, senderIdentity string, endpoint SoldierEndpointCapabilities) (int, error) {
	lines, err := home.ReadStatus(senderHome, soldierTaskID)
	if err != nil {
		return 0, fmt.Errorf("consume ready: reading status: %w", err)
	}
	if len(lines) == 0 || !strings.HasPrefix(lines[len(lines)-1], string(DeliveryStateReviewReady)+":") {
		return 0, nil
	}

	store := home.NewStore(senderHome)
	pending, err := store.ListPending(senderIdentity)
	if err != nil {
		return 0, fmt.Errorf("consume ready: list pending: %w", err)
	}
	var pendingEnv *home.Envelope
	for _, p := range pending {
		if matchesSoldierTask(p, soldierTaskID) {
			pendingEnv = p
			break
		}
	}
	if pendingEnv == nil {
		return 0, nil
	}
	if isDispatched(senderHome, soldierTaskID, pendingEnv.MessageID) {
		// NotificationRef sent, awaiting ack: never re-send.
		return 0, nil
	}

	flushResult := FlushPendingSoldierCommands(senderHome, soldierTaskID, senderIdentity, endpoint)
	if flushResult.Err != nil {
		return 0, fmt.Errorf("consume ready: flush: %w", flushResult.Err)
	}
	if !flushResult.Sent {
		return 0, nil
	}
	// Mark as dispatched so a repeated consume does not re-send.
	_ = markDispatched(senderHome, soldierTaskID, flushResult.MessageID)
	return 1, nil
}

// dispatchedPath returns the path for a dispatch marker file.
// Dispatch markers track that a NotificationRef was sent for a pending
// message, preventing duplicate sends on duplicate ready events.
func dispatchedPath(senderHome, taskID, messageID string) string {
	safeID := strings.NewReplacer("/", "_", ":", "_", "\\", "_").Replace(taskID)
	safeMsg := strings.NewReplacer("/", "_", ":", "_", "\\", "_", ".", "_").Replace(messageID)
	return filepath.Join(senderHome, "state", ".dispatched", safeID, safeMsg+".dispatched")
}

// markDispatched writes a durable marker that a NotificationRef was sent.
// Uses home.AtomicWrite.
func markDispatched(senderHome, taskID, messageID string) error {
	p := dispatchedPath(senderHome, taskID, messageID)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("mark dispatched: creating dir: %w", err)
	}
	content := fmt.Sprintf(`{"message_id":%q,"dispatched_at":%d}`, messageID, time.Now().UnixNano())
	if err := home.AtomicWrite(p, []byte(content), 0600); err != nil {
		return fmt.Errorf("mark dispatched: %w", err)
	}
	return nil
}

// isDispatched returns true if a dispatch marker exists for the message,
// meaning the NotificationRef was already sent and is awaiting ack.
func isDispatched(senderHome, taskID, messageID string) bool {
	_, err := os.Stat(dispatchedPath(senderHome, taskID, messageID))
	return err == nil
}

// cleanDispatched removes a dispatch marker after the pending is resolved.
// Idempotent: returns nil if marker doesn't exist.
func cleanDispatched(senderHome, taskID, messageID string) error {
	err := os.Remove(dispatchedPath(senderHome, taskID, messageID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
