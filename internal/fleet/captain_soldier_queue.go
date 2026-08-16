package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

// SendToSoldierResult describes the outcome of sending a command to a soldier.
type SendToSoldierResult struct {
	MessageID string
	Queued    bool // true when the soldier was busy and the command was queued
	Sent      bool // true when the soldier was idle and the notification ref was sent
	Err       error
}

type SoldierEndpointCapabilities interface {
	home.BoundSender
	Busy(home string, meta map[string]string) (bool, error)
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
//  4. If not busy: send NotificationRef through the endpoint capability (NOT the raw line)
//  5. If busy: return queued (no notification, no ack)
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
func SendToSoldier(senderHome, soldierTaskID, senderIdentity, line string, endpoint SoldierEndpointCapabilities) *SendToSoldierResult {
	result := &SendToSoldierResult{}

	// 1. Read soldier task meta for window and backend.
	meta, err := mhome.ReadMeta(senderHome, soldierTaskID)
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
	env := &home.Envelope{
		SenderRank:     home.RankCaptain,
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

	// Filter to only pending targeting this soldier (ReceiverID matches).
	receiverID := cleanReceiverID(soldierTaskID)
	var targetEnv *home.Envelope
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
	meta, err := mhome.ReadMeta(senderHome, soldierTaskID)
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
	if store.IsAcked(senderIdentity, targetEnv.MessageID) {
		ack, ackErr := store.ReadAck(senderIdentity, targetEnv.MessageID)
		if ackErr == nil && ack != nil {
			_ = store.RemovePendingAfterAck(senderIdentity, targetEnv.MessageID, ack)
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
		// Clean up dispatched marker if the pending was acked.
		if env.TaskID != "" {
			_ = cleanDispatched(senderHome, env.TaskID, env.MessageID)
		}
	}
	return nil
}

// EmitReadyEvent writes a durable ready event marker for a soldier task.
//
// The ready marker is stored at state/.ready/<taskID>/<event-id>.ready
// and contains the JSON-serialized ReadyEvent with a monotonic event ID,
// task ID, endpoint generation, and timestamp.
//
// Returns the ReadyEvent that was persisted. Idempotent: calling with the
// same event ID is a no-op. The caller (typically the soldier agent at a
// review-ready/idle turn boundary) must ensure unique event IDs — use
// time.Now().UnixNano() for the simplest unique key.
//
// After writing the marker, the caller should inject a lightweight
// notification to the captain's pane so the captain knows to scan for
// ready events. The notification should be a simple text line:
// "ready-event: <taskID> key=<eventKey>"
func EmitReadyEvent(homeDir, taskID, eventKey, metaGeneration string) (*ReadyEvent, error) {
	if homeDir == "" {
		return nil, fmt.Errorf("emit ready: empty home")
	}
	if taskID == "" {
		return nil, fmt.Errorf("emit ready: empty task ID")
	}
	if eventKey == "" {
		eventKey = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	// Parse meta generation for endpoint generation validation.
	var endpointGen int64
	if metaGeneration != "" {
		if _, err := fmt.Sscanf(metaGeneration, "%d", &endpointGen); err != nil {
			endpointGen = 0
		}
	}

	event := &ReadyEvent{
		EventID:            eventKey,
		TaskID:             taskID,
		Key:                eventKey,
		EndpointGeneration: endpointGen,
		Timestamp:          time.Now().UnixNano(),
	}

	// Write marker file atomically (temp-file + rename).
	p := readyEventPath(homeDir, taskID, eventKey)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return nil, fmt.Errorf("emit ready: creating dir: %w", err)
	}

	data, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("emit ready: marshal: %w", err)
	}

	// Check for existing marker — idempotent.
	if existing, err := os.ReadFile(p); err == nil && len(existing) > 0 {
		// Parse the existing event to preserve original timestamp.
		var existingEvent ReadyEvent
		if jsonErr := json.Unmarshal(existing, &existingEvent); jsonErr == nil {
			// Same eventKey means the same turn boundary — idempotent.
			return &existingEvent, nil
		}
		// Corrupt file: overwrite.
	}

	// Atomic write: temp file + rename.
	tmp, tmpErr := os.CreateTemp(filepath.Dir(p), ".tmp-")
	if tmpErr != nil {
		return nil, fmt.Errorf("emit ready: create temp: %w", tmpErr)
	}
	tmpName := tmp.Name()
	if _, writeErr := tmp.Write(data); writeErr != nil {
		tmp.Close()
		os.Remove(tmpName)
		return nil, fmt.Errorf("emit ready: write temp: %w", writeErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("emit ready: close temp: %w", closeErr)
	}
	if renameErr := os.Rename(tmpName, p); renameErr != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("emit ready: rename: %w", renameErr)
	}

	return event, nil
}

// readyEventPath returns the path for a ready event marker file.
func readyEventPath(homeDir, taskID, eventKey string) string {
	safeID := strings.NewReplacer("/", "_", ":", "_", "\\", "_").Replace(taskID)
	safeKey := strings.NewReplacer("/", "_", ":", "_", "\\", "_", ".", "_").Replace(eventKey)
	return filepath.Join(homeDir, "state", ".ready", safeID, safeKey+".ready")
}

// readyEventDir returns the directory for ready events for a specific task.
func readyEventDir(homeDir, taskID string) string {
	safeID := strings.NewReplacer("/", "_", ":", "_", "\\", "_").Replace(taskID)
	return filepath.Join(homeDir, "state", ".ready", safeID)
}

// ScanReadyEvents reads all ready event markers for a given task and returns
// them ordered by timestamp (oldest first). Returns nil, nil if no ready
// events exist. After reading, markers are NOT removed — they are left for
// the consumer to clean up after processing.
func ScanReadyEvents(homeDir, taskID string) ([]*ReadyEvent, error) {
	dir := readyEventDir(homeDir, taskID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan ready: reading dir: %w", err)
	}

	var events []*ReadyEvent
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ready") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			continue // skip unreadable
		}
		event, parseErr := ParseReadyEvent(string(data))
		if parseErr != nil {
			continue // skip malformed
		}
		events = append(events, event)
	}

	// Sort by timestamp (oldest first).
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp < events[j].Timestamp
	})

	return events, nil
}

// CleanReadyEvent removes a single ready event marker after processing.
// Idempotent: returns nil if marker doesn't exist.
func CleanReadyEvent(homeDir, taskID, eventKey string) error {
	err := os.Remove(readyEventPath(homeDir, taskID, eventKey))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// CleanAllReadyEvents removes all ready event markers for a given task.
// Idempotent: returns nil if directory doesn't exist.
func CleanAllReadyEvents(homeDir, taskID string) error {
	return os.RemoveAll(readyEventDir(homeDir, taskID))
}

// ConsumeAllReadyEvents scans for ready events for a task and flushes pending
// commands for each valid ready event. After flush, the ready event marker is
// cleaned up. Returns the number of commands flushed and any errors.
//
// This is the captain-side entry point for consuming ready events.
// Idempotent: calling with no ready events or no pending is a no-op.
// Stale events (by timestamp) are rejected and cleaned up.
//
// Key validation: the ready event's Key field is validated against the
// durable task key from meta, not against itself. Callers must provide the
// correct key context.
func ConsumeAllReadyEvents(senderHome, soldierTaskID, senderIdentity, metaGeneration string, endpoint SoldierEndpointCapabilities) (int, error) {
	events, err := ScanReadyEvents(senderHome, soldierTaskID)
	if err != nil {
		return 0, fmt.Errorf("consume ready: scan: %w", err)
	}
	if len(events) == 0 {
		return 0, nil
	}

	// Read task meta for durable key validation.
	meta, metaErr := mhome.ReadMeta(senderHome, soldierTaskID)
	if metaErr != nil {
		// If meta doesn't exist (task never spawned), there's nothing to flush.
		// Clean up any stale ready events and return.
		_ = CleanAllReadyEvents(senderHome, soldierTaskID)
		return 0, nil
	}
	metaKey := meta["key"]

	var flushed int
	for _, ev := range events {
		// Validate the ready event against durable task ID, meta key (if set),
		// and generation. The meta key is the authoritative key for dispatch
		// lifecycle. If meta key is empty, only task ID and generation are checked.
		if err := ValidateReadyEvent(ev, soldierTaskID, metaKey, metaGeneration); err != nil {
			// Stale or invalid: clean up and continue.
			_ = CleanReadyEvent(senderHome, soldierTaskID, ev.EventID)
			continue
		}

		// Check if the pending command has already been dispatched (marked
		// by a .dispatched marker). This prevents re-sending the same
		// NotificationRef on duplicate ready events.
		store := home.NewStore(senderHome)
		pending, listErr := store.ListPending(senderIdentity)
		if listErr != nil {
			return flushed, fmt.Errorf("consume ready: list pending: %w", listErr)
		}

		// Filter to pending targeting this soldier.
		receiverID := cleanReceiverID(soldierTaskID)
		var pendingEnv *home.Envelope
		for _, p := range pending {
			if p.ReceiverID == receiverID {
				pendingEnv = p
				break
			}
		}

		if pendingEnv != nil && store.IsAcked(senderIdentity, pendingEnv.MessageID) {
			// Already acked — remove pending and clean up ready event.
			ack, ackErr := store.ReadAck(senderIdentity, pendingEnv.MessageID)
			if ackErr == nil && ack != nil {
				_ = store.RemovePendingAfterAck(senderIdentity, pendingEnv.MessageID, ack)
			}
			_ = CleanReadyEvent(senderHome, soldierTaskID, ev.EventID)
			continue
		}

		if pendingEnv != nil && isDispatched(senderHome, soldierTaskID, pendingEnv.MessageID) {
			// Already dispatched (NotificationRef sent, awaiting ack).
			// This duplicate ready event is harmless — clean up and skip.
			// The pending remains until exact ack reconciles.
			_ = CleanReadyEvent(senderHome, soldierTaskID, ev.EventID)
			continue
		}

		// Flush one pending command.
		flushResult := FlushPendingSoldierCommands(senderHome, soldierTaskID, senderIdentity, endpoint)
		if flushResult.Err != nil {
			// If flush failed, leave the ready event for retry.
			return flushed, fmt.Errorf("consume ready: flush: %w", flushResult.Err)
		}

		if flushResult.Sent {
			// Mark as dispatched so duplicate ready events don't re-send.
			_ = markDispatched(senderHome, soldierTaskID, flushResult.MessageID)
			flushed++
		}

		// Clean up the ready event marker.
		_ = CleanReadyEvent(senderHome, soldierTaskID, ev.EventID)

		// Only flush one command per scan — the first ready event triggered one flush.
		// If more pending exist, the next ready event will flush them.
		break
	}

	return flushed, nil
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
// Uses atomic write (temp-file + rename).
func markDispatched(senderHome, taskID, messageID string) error {
	p := dispatchedPath(senderHome, taskID, messageID)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("mark dispatched: creating dir: %w", err)
	}
	content := fmt.Sprintf(`{"message_id":%q,"dispatched_at":%d}`, messageID, time.Now().UnixNano())
	tmp, tmpErr := os.CreateTemp(filepath.Dir(p), ".tmp-")
	if tmpErr != nil {
		return fmt.Errorf("mark dispatched: create temp: %w", tmpErr)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write([]byte(content)); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("mark dispatched: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("mark dispatched: close: %w", err)
	}
	if err := os.Rename(tmpName, p); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("mark dispatched: rename: %w", err)
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

// ReadyEvent carries the durable ready signal from a soldier.
type ReadyEvent struct {
	EventID            string `json:"event_id"`
	TaskID             string `json:"task_id"`
	Key                string `json:"key"`
	EndpointGeneration int64  `json:"endpoint_generation"`
	Timestamp          int64  `json:"timestamp"`
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
	// Only validate key when the durable key is set. When empty, the key
	// check is a no-op (e.g., tasks without an explicit lifecycle key).
	if curKey != "" && event.Key != curKey {
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
