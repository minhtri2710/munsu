// Package mailbox implements rank-aware durable one-hop mailboxes for
// General↔Captain↔Soldier communication.
//
// Design:
//   - Envelopes are receiver-owned: the receiver's state/.inbox/<sender-id>/ owns
//     the durable envelope until processed and acked.
//   - The sender retains a pending record until exact ack is received.
//   - Delivery is direct via session.SubmitPrompt (SendKeys); the watcher is not
//     involved in normal routing.
//   - Watcher provides recovery-only retry on startup/restart.
//   - Rank validation ensures foreign/misplaced envelopes fail closed.
package mailbox

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is the stable schema identifier for all mailbox envelopes.
const SchemaVersion = "munsu.mailbox-envelope/v1"

// Delivery status constants.
const (
	StatusPending   = "pending"   // envelope created, not yet delivered
	StatusDelivered = "delivered" // prompt submitted, awaiting processing ack
	StatusAcked     = "acked"     // processing ack received from receiver
	StatusFailed    = "failed"    // delivery permanently failed
)

// Rank identifies the role of a mailbox participant.
type Rank string

const (
	RankGeneral Rank = "general"
	RankCaptain Rank = "captain"
	RankSoldier Rank = "soldier"
)

// ValidRank returns true if the rank is a known value.
func ValidRank(r Rank) bool {
	switch r {
	case RankGeneral, RankCaptain, RankSoldier:
		return true
	default:
		return false
	}
}

// InboxDir is the receiver-owned inbox subdirectory under state/.
const InboxDir = ".inbox"

// OutboxDir is the sender pending record subdirectory under state/.
const OutboxDir = ".outbox"

// Envelope is the durable, rank-aware one-hop message.
// Immutable after creation (MessageID never changes); only status transitions.
type Envelope struct {
	SchemaVersion   string `json:"schema_version"`
	MessageID       string `json:"message_id"`
	SenderRank      Rank   `json:"sender_rank"`
	SenderIdentity  string `json:"sender_identity"`
	ReceiverRank    Rank   `json:"receiver_rank"`
	ReceiverID      string `json:"receiver_id"`
	ReceiverHome    string `json:"receiver_home,omitempty"` // resolved home for inbox path
	TaskID          string `json:"task_id"`
	Key             string `json:"key"`
	State           string `json:"state"`       // report state (done, failed, etc.)
	Payload         string `json:"payload"`      // message content
	PayloadHash     string `json:"payload_hash"` // SHA-256 of payload
	CreatedAt       int64  `json:"created_at"`
	DeliveryAttempts int   `json:"delivery_attempts"`
	DeliveryStatus  string `json:"delivery_status"` // pending, delivered, acked, failed
	LastAttemptAt   int64  `json:"last_attempt_at,omitempty"`
	AckedAt         int64  `json:"acked_at,omitempty"`
}

// --- ID generation ---

// NewMessageID generates a random 32-char hex message ID.
func NewMessageID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating message id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// PayloadHashHex returns the hex SHA-256 of payload.
func PayloadHashHex(payload string) string {
	h := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(h[:])
}

// --- Validation ---

// ValidateEnvelope checks that the envelope has valid rank, identity, and payload.
func ValidateEnvelope(env *Envelope) error {
	if env.MessageID == "" {
		return fmt.Errorf("envelope: empty message ID")
	}
	if !ValidRank(env.SenderRank) {
		return fmt.Errorf("envelope: invalid sender rank %q", env.SenderRank)
	}
	if !ValidRank(env.ReceiverRank) {
		return fmt.Errorf("envelope: invalid receiver rank %q", env.ReceiverRank)
	}
	if env.SenderIdentity == "" {
		return fmt.Errorf("envelope: empty sender identity")
	}
	if env.ReceiverID == "" {
		return fmt.Errorf("envelope: empty receiver identity")
	}
	if env.Payload == "" {
		return fmt.Errorf("envelope: empty payload")
	}
	if env.PayloadHash != PayloadHashHex(env.Payload) {
		return fmt.Errorf("envelope: payload hash mismatch")
	}
	// Validate rank transitions (one-hop semantics).
	switch env.SenderRank {
	case RankGeneral:
		if env.ReceiverRank != RankCaptain {
			return fmt.Errorf("envelope: general can only send to captain, not %q", env.ReceiverRank)
		}
	case RankCaptain:
		if env.ReceiverRank != RankGeneral && env.ReceiverRank != RankSoldier {
			return fmt.Errorf("envelope: captain can only send to general or soldier, not %q", env.ReceiverRank)
		}
	case RankSoldier:
		if env.ReceiverRank != RankCaptain {
			return fmt.Errorf("envelope: soldier can only send to captain, not %q", env.ReceiverRank)
		}
	}
	return nil
}

// OwnershipError returns a structured error for a foreign/misplaced envelope.
func OwnershipError(gotIdentity, expectedIdentity string) error {
	return fmt.Errorf("ownership mismatch: envelope targets %q but accessed from %q", gotIdentity, expectedIdentity)
}

// --- Path helpers ---

// SenderOutboxHome returns the directory for sender-sided pending envelope records.
// Path: <senderHome>/state/.outbox/
func SenderOutboxHome(senderHome string) string {
	return filepath.Join(senderHome, "state", OutboxDir)
}

// senderOutboxPath returns the path for a sender's pending record for a specific message.
func senderOutboxPath(senderHome, messageID string) string {
	return filepath.Join(SenderOutboxHome(senderHome), messageID+".pending")
}

// ReceiverInboxDir returns the receiver-owned inbox directory.
// Path: <receiverHome>/state/.inbox/<senderIdentity>/
func ReceiverInboxDir(receiverHome, senderIdentity string) string {
	return filepath.Join(receiverHome, "state", InboxDir, senderIdentity)
}

// receiverInboxPath returns the path for a received envelope.
func receiverInboxPath(receiverHome, senderIdentity, messageID string) string {
	return filepath.Join(ReceiverInboxDir(receiverHome, senderIdentity), messageID+".json")
}

// receiverInboxAckPath returns the path for an ack of an envelope.
func receiverInboxAckPath(receiverHome, senderIdentity, messageID string) string {
	return filepath.Join(ReceiverInboxDir(receiverHome, senderIdentity), messageID+".ack")
}

// --- Create and send an envelope ---

// NewEnvelope creates a new rank-validated envelope and writes it to the
// receiver's durable inbox. Returns the envelope and the sender's pending
// path. The sender must retain the outbox pending record until the exact
// ack is received.
func NewEnvelope(receiverHome string, env *Envelope) error {
	// Auto-generate message ID if empty.
	if env.MessageID == "" {
		id, err := NewMessageID()
		if err != nil {
			return err
		}
		env.MessageID = id
	}
	env.SchemaVersion = SchemaVersion

	// Always set the payload hash from the current payload.
	env.PayloadHash = PayloadHashHex(env.Payload)

	// Set initial delivery status.
	env.DeliveryStatus = StatusPending
	env.CreatedAt = time.Now().UnixNano()

	// Validate the envelope.
	if err := ValidateEnvelope(env); err != nil {
		return fmt.Errorf("new envelope: %w", err)
	}

	// Write to receiver's inbox directory.
	inboxDir := ReceiverInboxDir(receiverHome, env.SenderIdentity)
	if err := os.MkdirAll(inboxDir, 0755); err != nil {
		return fmt.Errorf("creating receiver inbox dir: %w", err)
	}

	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling envelope: %w", err)
	}

	inboxPath := receiverInboxPath(receiverHome, env.SenderIdentity, env.MessageID)
	if err := os.WriteFile(inboxPath, data, 0644); err != nil {
		return fmt.Errorf("writing envelope to inbox: %w", err)
	}

	return nil
}

// --- Sender pending record ---

// SaveSenderPending writes a record of the sent envelope in the sender's
// outbox. This is the sender's authoritative evidence that a message was
// dispatched and is pending ack. Returns the path to the pending record.
func SaveSenderPending(senderHome string, env *Envelope) (string, error) {
	outboxDir := SenderOutboxHome(senderHome)
	if err := os.MkdirAll(outboxDir, 0755); err != nil {
		return "", fmt.Errorf("creating sender outbox dir: %w", err)
	}

	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling outbox pending: %w", err)
	}

	path := senderOutboxPath(senderHome, env.MessageID)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("writing sender outbox: %w", err)
	}

	return path, nil
}

// RemoveSenderPending removes the sender's pending record for a message.
func RemoveSenderPending(senderHome, messageID string) error {
	err := os.Remove(senderOutboxPath(senderHome, messageID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ListSenderPending returns all pending (un-acked) outbox messages for the sender.
func ListSenderPending(senderHome string) ([]*Envelope, error) {
	dir := SenderOutboxHome(senderHome)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var envelopes []*Envelope
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pending") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			continue
		}
		var env Envelope
		if unmarshalErr := json.Unmarshal(data, &env); unmarshalErr != nil {
			continue
		}
		envelopes = append(envelopes, &env)
	}

	// Sort by creation time (FIFO).
	sort.Slice(envelopes, func(i, j int) bool {
		return envelopes[i].CreatedAt < envelopes[j].CreatedAt
	})

	return envelopes, nil
}

// --- Receiver inbox operations ---

// GetInboxEnvelope reads an envelope from the receiver's inbox.
// Returns nil, nil if not found.
func GetInboxEnvelope(receiverHome, senderIdentity, messageID string) (*Envelope, error) {
	data, err := os.ReadFile(receiverInboxPath(receiverHome, senderIdentity, messageID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("unmarshaling inbox envelope %s: %w", messageID, err)
	}
	return &env, nil
}

// ListPendingInbox returns all pending (delivery_status != acked) envelopes
// from a sender in a receiver's inbox.
func ListPendingInbox(receiverHome, senderIdentity string) ([]*Envelope, error) {
	dir := ReceiverInboxDir(receiverHome, senderIdentity)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var envelopes []*Envelope
	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".json")) || strings.HasSuffix(e.Name(), ".ack") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			continue
		}
		var env Envelope
		if unmarshalErr := json.Unmarshal(data, &env); unmarshalErr != nil {
			continue
		}
		if env.DeliveryStatus == StatusAcked {
			continue
		}
		envelopes = append(envelopes, &env)
	}

	sort.Slice(envelopes, func(i, j int) bool {
		return envelopes[i].CreatedAt < envelopes[j].CreatedAt
	})

	return envelopes, nil
}

// MarkInboxDelivered updates the delivery status to "delivered" in the
// receiver's inbox envelope.
func MarkInboxDelivered(receiverHome, senderIdentity, messageID string) error {
	return updateInboxEnvelope(receiverHome, senderIdentity, messageID, func(env *Envelope) {
		env.DeliveryStatus = StatusDelivered
		env.LastAttemptAt = time.Now().UnixNano()
		env.DeliveryAttempts++
	})
}

// MarkInboxFailed updates the delivery status to "failed".
func MarkInboxFailed(receiverHome, senderIdentity, messageID string) error {
	return updateInboxEnvelope(receiverHome, senderIdentity, messageID, func(env *Envelope) {
		env.DeliveryStatus = StatusFailed
		env.LastAttemptAt = time.Now().UnixNano()
		env.DeliveryAttempts++
	})
}

// WriteAck writes an ack file for a received envelope.
func WriteAck(receiverHome, senderIdentity, messageID string) error {
	ackPath := receiverInboxAckPath(receiverHome, senderIdentity, messageID)
	if err := os.MkdirAll(filepath.Dir(ackPath), 0755); err != nil {
		return fmt.Errorf("creating ack dir: %w", err)
	}
	content := fmt.Sprintf("message_id=%s\nacked_at=%d\n",
		messageID, time.Now().UnixNano())
	return os.WriteFile(ackPath, []byte(content), 0644)
}

// IsAcked returns true if the envelope has been acknowledged by the receiver.
func IsAcked(receiverHome, senderIdentity, messageID string) bool {
	_, err := os.Stat(receiverInboxAckPath(receiverHome, senderIdentity, messageID))
	if err == nil {
		return true
	}
	// Also check the envelope's delivery status.
	env, err := GetInboxEnvelope(receiverHome, senderIdentity, messageID)
	if err != nil || env == nil {
		return false
	}
	return env.DeliveryStatus == StatusAcked
}

// MarkProcessed marks an envelope as fully processed (acked) by the receiver.
func MarkProcessed(receiverHome, senderIdentity, messageID string) error {
	// Write ack file and update envelope status.
	if err := WriteAck(receiverHome, senderIdentity, messageID); err != nil {
		return err
	}
	return updateInboxEnvelope(receiverHome, senderIdentity, messageID, func(env *Envelope) {
		env.DeliveryStatus = StatusAcked
		env.AckedAt = time.Now().UnixNano()
	})
}

// --- Update helpers ---

func updateInboxEnvelope(receiverHome, senderIdentity, messageID string, update func(*Envelope)) error {
	env, err := GetInboxEnvelope(receiverHome, senderIdentity, messageID)
	if err != nil {
		return err
	}
	if env == nil {
		return fmt.Errorf("envelope %s not found in inbox", messageID)
	}
	update(env)
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling updated envelope: %w", err)
	}
	path := receiverInboxPath(receiverHome, senderIdentity, messageID)
	return os.WriteFile(path, data, 0644)
}

// --- Rank resolution helpers ---

// ResolveCaptainIdentity reads the captain identity from the provenance marker.
// Returns the directory basename as fallback.
func ResolveCaptainIdentity(captainHome string) (string, error) {
	markerPath := filepath.Join(captainHome, ".munsu-captain-home")
	data, err := os.ReadFile(markerPath)
	if err != nil {
		return filepath.Base(captainHome), nil
	}
	parts := strings.Fields(string(data))
	if len(parts) >= 2 {
		return parts[1], nil
	}
	return filepath.Base(captainHome), nil
}

// ResolveGeneralIdentity returns the identity of a General home.
// Uses the directory basename of the home.
func ResolveGeneralIdentity(generalHome string) string {
	return filepath.Base(generalHome)
}

// ResolveSoldierIdentity returns the soldier identity from the task ID.
func ResolveSoldierIdentity(taskID string) string {
	return taskID
}
