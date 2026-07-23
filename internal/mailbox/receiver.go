package mailbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/session"
)

// captainMarkerName is the provenance marker file used by captain homes.
// The file contains three lines: version, captain identity, canonical path.
const captainMarkerName = ".munsu-captain-home"

// NotificationRef is a compact structured reference that points the receiver
// at their own inbox data. It contains only the message ID and sender
// identity needed to locate the envelope; the raw payload is not included
// as routing authority. The receiver loads the envelope from their own
// inbox, which is the single source of truth for payload content.
type NotificationRef struct {
	MessageID      string `json:"message_id"`
	SenderIdentity string `json:"sender_identity"`
}

// Encode returns a canonical JSON string for the NotificationRef.
// This is the stable serialization format used in notification text, not
// ad-hoc fmt.Sprintf formatting.
func (ref NotificationRef) Encode() string {
	data, _ := json.Marshal(ref)
	return string(data)
}

// ParseNotificationRef parses a canonical JSON-encoded NotificationRef.
// Returns an error if the input is not valid JSON or lacks required fields.
func ParseNotificationRef(s string) (NotificationRef, error) {
	var ref NotificationRef
	if err := json.Unmarshal([]byte(s), &ref); err != nil {
		return ref, fmt.Errorf("parse notification ref: %w", err)
	}
	if err := ref.Validate(); err != nil {
		return ref, fmt.Errorf("parse notification ref: %w", err)
	}
	return ref, nil
}

// Validate checks that the ref has the minimum required fields.
func (ref NotificationRef) Validate() error {
	if ref.MessageID == "" {
		return fmt.Errorf("notification: empty message ID")
	}
	if ref.SenderIdentity == "" {
		return fmt.Errorf("notification: empty sender identity")
	}
	return nil
}

// Resolution is the outcome of receiver processing one notification.
type Resolution struct {
	Ref      NotificationRef
	Envelope *Envelope
	Outcome  string
	Ack      *ProcessingAck
	Err      error
}

// Ok returns true when processing succeeded (ack was written or was already
// present with the same outcome).
func (r *Resolution) Ok() bool {
	return r.Err == nil && r.Ack != nil
}

// Receiver provides receiver-side processing for one receiver identity.
// It loads envelopes from the receiver's own inbox and validates all
// provenance fields before writing acks.
//
// Identity and rank are derived from durable files in the home directory
// rather than trusting caller-provided strings. Captain homes carry a
// .munsu-captain-home provenance marker; other homes derive identity
// from the directory basename with general rank.
type Receiver struct {
	identity string
	rank     Rank
	store    *Store
}

// NewReceiver creates a Receiver backed by the store at the receiver's
// home directory. The receiver identity and rank are derived from durable
// home provenance (e.g., .munsu-captain-home marker) instead of trusting
// caller strings.
func NewReceiver(homeDir string) (*Receiver, error) {
	ident, rnk, err := ReadHomeIdentity(homeDir)
	if err != nil {
		return nil, fmt.Errorf("new receiver: %w", err)
	}
	return &Receiver{
		identity: ident,
		rank:     rnk,
		store:    NewStore(homeDir),
	}, nil
}

// ReadHomeIdentity reads the identity and rank from durable files in the
// given home directory. For captain homes with a .munsu-captain-home marker,
// the identity comes from the marker and rank is RankCaptain. For homes
// without a marker, the identity is the directory basename and rank is
// RankGeneral (parent/orchestrator home).
func ReadHomeIdentity(homeDir string) (identity string, rank Rank, err error) {
	markerPath := filepath.Join(homeDir, captainMarkerName)
	data, readErr := os.ReadFile(markerPath)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			// No captain marker — treat as general/parent home.
			base := filepath.Base(homeDir)
			if base == "" || base == "." || base == "/" {
				return "", "", fmt.Errorf("cannot derive identity from home %s: no marker and empty basename", homeDir)
			}
			return base, RankGeneral, nil
		}
		return "", "", fmt.Errorf("reading home identity marker: %w", readErr)
	}

	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 4)
	if len(lines) < 2 {
		return "", "", fmt.Errorf("malformed home identity marker at %s", markerPath)
	}
	version := strings.TrimSpace(lines[0])
	if version != "munsu-v2" {
		return "", "", fmt.Errorf("unsupported home identity version %q at %s", version, markerPath)
	}
	id := strings.TrimSpace(lines[1])
	if id == "" {
		return "", "", fmt.Errorf("empty captain identity in marker %s", markerPath)
	}
	return id, RankCaptain, nil
}

// WriteHomeIdentity writes a durable identity marker into the home directory
// so that NewReceiver/ReadHomeIdentity can derive identity and rank from it.
// Used in tests and provisioning.
//
// For captain homes, a .munsu-captain-home provenance marker is written.
// For non-captain homes, identity is derived from the directory basename
// with general rank (soldier/general distinction is not stored durably in
// the current model — tests use named subdirectories).
func WriteHomeIdentity(homeDir, identity string, rank Rank) error {
	if identity == "" {
		return fmt.Errorf("write home identity: empty identity")
	}
	if !ValidRank(rank) {
		return fmt.Errorf("write home identity: invalid rank %q", rank)
	}
	if rank == RankCaptain {
		canon, err := filepath.Abs(homeDir)
		if err != nil {
			return fmt.Errorf("write home identity: resolving home: %w", err)
		}
		content := fmt.Sprintf("munsu-v2\n%s\n%s\n", identity, canon)
		path := filepath.Join(homeDir, captainMarkerName)
		return os.WriteFile(path, []byte(content), 0644)
	}
	// For non-captain ranks, no marker is written — identity is derived from
	// the directory basename on read with general rank. The caller must use
	// a named directory (e.g., filepath.Join(t.TempDir(), identity)) so that
	// the basename matches the desired identity.
	return nil
}

// Process loads the envelope from the receiver's own inbox, validates all
// provenance fields (receiver identity, receiver rank, sender identity,
// payload hash), and writes an ack with the given outcome.
//
// Idempotent: calling with the same outcome for the same message is OK
// and returns the existing ack (with original timestamp preserved).
// Conflicting outcome fails closed.
//
// Validation order:
//  1. ref.MessageID and ref.SenderIdentity are non-empty
//  2. Envelope exists in the receiver's inbox for (ref.SenderIdentity, ref.MessageID)
//  3. Full ValidateEnvelope (rank transition, task/key, payload hash completeness)
//  4. Envelope ReceiverID matches receiver identity (home provenance)
//  5. Envelope ReceiverRank matches receiver rank (home provenance)
//  6. Envelope SenderIdentity matches ref.SenderIdentity
//  7. Payload hash matches envelope payload (redundant after ValidateEnvelope)
//  8. Existing ack: same outcome = idempotent (original timestamp preserved),
//     different outcome = conflict (fail closed)
func (r *Receiver) Process(ref NotificationRef, outcome string) *Resolution {
	res := &Resolution{
		Ref:     ref,
		Outcome: outcome,
	}

	// 1. Validate ref.
	if err := ref.Validate(); err != nil {
		res.Err = err
		return res
	}

	// 2. Load envelope from own inbox.
	env, err := r.store.ReadEnvelope(ref.SenderIdentity, ref.MessageID)
	if err != nil {
		res.Err = fmt.Errorf("read envelope: %w", err)
		return res
	}
	if env == nil {
		res.Err = fmt.Errorf("envelope not found: sender=%s msg=%s",
			ref.SenderIdentity, ref.MessageID)
		return res
	}
	res.Envelope = env

	// 3. Call full ValidateEnvelope for rank transition, task/key, hash completeness.
	if err := ValidateEnvelope(env); err != nil {
		res.Err = fmt.Errorf("validate envelope: %w", err)
		return res
	}

	// 4. Validate receiver identity (home provenance).
	if env.ReceiverID != r.identity {
		res.Err = fmt.Errorf("receiver identity mismatch: envelope has %q, receiver is %q",
			env.ReceiverID, r.identity)
		return res
	}

	// 5. Validate receiver rank.
	if env.ReceiverRank != r.rank {
		res.Err = fmt.Errorf("receiver rank mismatch: envelope has %q, receiver is %q",
			env.ReceiverRank, r.rank)
		return res
	}

	// 6. Validate sender identity matches ref.
	if env.SenderIdentity != ref.SenderIdentity {
		res.Err = fmt.Errorf("sender identity mismatch: envelope has %q, ref has %q",
			env.SenderIdentity, ref.SenderIdentity)
		return res
	}

	// 7. Validate payload hash (detect tampering).
	// This is redundant after ValidateEnvelope but kept as defense-in-depth.
	if env.PayloadHash != PayloadHashHex(env.Payload) {
		res.Err = fmt.Errorf("tampered payload: hash mismatch")
		return res
	}

	// 8. Check for existing ack.
	existing, err := r.store.ReadAck(ref.SenderIdentity, ref.MessageID)
	if err != nil {
		res.Err = fmt.Errorf("read existing ack: %w", err)
		return res
	}
	if existing != nil {
		if existing.Outcome == outcome {
			// Idempotent: same outcome, return existing ack preserving
			// the original ProcessedAt timestamp.
			res.Ack = existing
			return res
		}
		// Conflicting outcome: fail closed.
		res.Err = fmt.Errorf("conflicting ack: existing outcome %q != new outcome %q",
			existing.Outcome, outcome)
		return res
	}

	// 9. Build and write ack.
	ack := &ProcessingAck{
		MessageID:      env.MessageID,
		SenderRank:     env.SenderRank,
		SenderIdentity: env.SenderIdentity,
		ReceiverRank:   env.ReceiverRank,
		ReceiverID:     env.ReceiverID,
		TaskID:         env.TaskID,
		Key:            env.Key,
		PayloadHash:    env.PayloadHash,
		ProcessedAt:    time.Now().UnixNano(),
		Outcome:        outcome,
	}
	if err := r.store.WriteAck(ack); err != nil {
		res.Err = fmt.Errorf("write ack: %w", err)
		return res
	}
	res.Ack = ack
	return res
}

// NotifyResult carries the outcome of a notification delivery attempt.
type NotifyResult struct {
	Ref          NotificationRef
	Acknowledged bool
	Status       string
	Detail       string
	Err          error
}

// NotifyReceiver sends the canonical NotificationRef text to the receiver's
// session via session.SubmitPrompt. The receiver is expected to read their
// own inbox using the ref to locate the envelope.
//
// Submission acknowledgment (Acknowledged=true) never removes the sender's
// pending record. Pending records are managed separately through the ack
// flow via the sender's RemovePendingAfterAck.
func NotifyReceiver(receiverHome string, ref NotificationRef, meta map[string]string) *NotifyResult {
	nr := &NotifyResult{Ref: ref}

	bk, _, err := session.BackendForTask(receiverHome, meta)
	if err != nil {
		nr.Err = fmt.Errorf("resolve backend: %w", err)
		return nr
	}

	windowID := meta["window"]
	if windowID == "" {
		nr.Err = fmt.Errorf("no window in meta")
		return nr
	}

	// Build notification text from the ref using canonical Encode — no
	// payload included. Raw payload is not routing authority.
	text := ref.Encode()

	result := session.SubmitPrompt(bk, windowID, text)
	nr.Acknowledged = result.Acknowledged()
	nr.Status = string(result.Status)
	nr.Detail = result.Detail
	if result.Err != nil {
		nr.Err = result.Err
	}
	return nr
}
