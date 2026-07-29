// Package mailbox implements rank-aware durable one-hop mailboxes for
// General-Captain-Soldier communication.
//
// Design:
//   - Envelopes are receiver-owned: the receiver's state/.inbox/<sender-id>/
//     owns the durable envelope until processed and acked.
//   - The sender retains a pending record until exact ack is received.
//   - Ack is a typed ProcessingAck written alongside the envelope in the
//     receiver's inbox. Pending records may only be removed after a valid
//     matching ack.
//   - All writes use atomic temp-file + rename to prevent partial writes.
package home

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// SchemaVersion is the stable schema identifier for all mailbox envelopes.
const SchemaVersion = "munsu.mailbox-envelope/v1"

// AckSchemaVersion is the schema identifier for mailbox ack records.
const AckSchemaVersion = "munsu.mailbox-ack/v1"

// Allowed outcomes for ProcessingAck.
const (
	OutcomeAccepted     = "accepted"
	OutcomeDone         = "done"
	OutcomeFailed       = "failed"
	OutcomeNeedsDecisio = "needs-decision"
	OutcomeBlocked      = "blocked"
	OutcomePaused       = "paused"
)

// ValidOutcome returns true if the outcome is a known value.
func ValidOutcome(o string) bool {
	switch o {
	case OutcomeAccepted, OutcomeDone, OutcomeFailed, OutcomeNeedsDecisio, OutcomeBlocked, OutcomePaused:
		return true
	default:
		return false
	}
}

// InboxDir is the receiver-owned inbox subdirectory under state/.
const InboxDir = ".inbox"

// OutboxDir is the sender pending record subdirectory under state/.
const OutboxDir = ".outbox"

// Envelope is the immutable rank-aware one-hop message.
// It contains only creation-time fields; delivery tracking is handled
// by separate ProcessingAck records.
type Envelope struct {
	SchemaVersion  string `json:"schema_version"`
	MessageID      string `json:"message_id"`
	SenderRank     Rank   `json:"sender_rank"`
	SenderIdentity string `json:"sender_identity"`
	ReceiverRank   Rank   `json:"receiver_rank"`
	ReceiverID     string `json:"receiver_id"`
	Kind           string `json:"kind,omitempty"`
	TaskID         string `json:"task_id,omitempty"`
	Key            string `json:"key,omitempty"`
	Payload        string `json:"payload"`
	PayloadHash    string `json:"payload_hash"`
	CreatedAt      int64  `json:"created_at"`
}

// ProcessingAck is the typed acknowledgment from receiver to sender.
// It binds the original envelope fields to confirm exact match before
// the sender may remove its pending record.
type ProcessingAck struct {
	SchemaVersion  string `json:"schema_version"`
	MessageID      string `json:"message_id"`
	SenderRank     Rank   `json:"sender_rank"`
	SenderIdentity string `json:"sender_identity"`
	ReceiverRank   Rank   `json:"receiver_rank"`
	ReceiverID     string `json:"receiver_id"`
	TaskID         string `json:"task_id,omitempty"`
	Key            string `json:"key,omitempty"`
	PayloadHash    string `json:"payload_hash"`
	ProcessedAt    int64  `json:"processed_at"`
	Outcome        string `json:"outcome"`
}

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

// ValidatePathComponent rejects path components that could be used for
// directory traversal. Valid components contain only alphanumerics, hyphens,
// underscores, and dots (no slashes, colons, or path separators).
func ValidatePathComponent(component, label string) error {
	if component == "" {
		return fmt.Errorf("%s: empty", label)
	}
	if strings.Contains(component, "/") {
		return fmt.Errorf("%s: contains slash: %q", label, component)
	}
	if strings.Contains(component, "\\") {
		return fmt.Errorf("%s: contains backslash: %q", label, component)
	}
	if strings.Contains(component, "..") {
		return fmt.Errorf("%s: contains relative path: %q", label, component)
	}
	if strings.Contains(component, ":") {
		return fmt.Errorf("%s: contains colon: %q", label, component)
	}
	return nil
}

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

// ValidateAck checks that ack matches the envelope fields that the sender
// expects to verify before removing the pending record. All identity,
// routing, and content fields must match exactly.
func ValidateAck(env *Envelope, ack *ProcessingAck) error {
	if ack.MessageID != env.MessageID {
		return fmt.Errorf("ack: message ID mismatch: %q != %q", ack.MessageID, env.MessageID)
	}
	if ack.SenderRank != env.SenderRank {
		return fmt.Errorf("ack: sender rank mismatch: %q != %q", ack.SenderRank, env.SenderRank)
	}
	if ack.SenderIdentity != env.SenderIdentity {
		return fmt.Errorf("ack: sender identity mismatch: %q != %q", ack.SenderIdentity, env.SenderIdentity)
	}
	if ack.ReceiverRank != env.ReceiverRank {
		return fmt.Errorf("ack: receiver rank mismatch: %q != %q", ack.ReceiverRank, env.ReceiverRank)
	}
	if ack.ReceiverID != env.ReceiverID {
		return fmt.Errorf("ack: receiver ID mismatch: %q != %q", ack.ReceiverID, env.ReceiverID)
	}
	if ack.TaskID != env.TaskID {
		return fmt.Errorf("ack: task ID mismatch: %q != %q", ack.TaskID, env.TaskID)
	}
	if ack.Key != env.Key {
		return fmt.Errorf("ack: key mismatch: %q != %q", ack.Key, env.Key)
	}
	if ack.PayloadHash != env.PayloadHash {
		return fmt.Errorf("ack: payload hash mismatch: %q != %q", ack.PayloadHash, env.PayloadHash)
	}
	return nil
}

// ValidateProcessingAck validates the ack's own fields (processed_at, outcome).
func ValidateProcessingAck(ack *ProcessingAck) error {
	if ack.ProcessedAt <= 0 {
		return fmt.Errorf("ack: processed_at must be > 0, got %d", ack.ProcessedAt)
	}
	if !ValidOutcome(ack.Outcome) {
		return fmt.Errorf("ack: invalid outcome %q", ack.Outcome)
	}
	return nil
}
