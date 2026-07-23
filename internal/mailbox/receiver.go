package mailbox

import (
	"fmt"
	"time"

	"github.com/minhtri2710/munsu/internal/session"
)

// NotificationRef is a compact structured reference that points the receiver
// at their own inbox data. It contains only the message ID and sender
// identity needed to locate the envelope; the raw payload is not included
// as routing authority. The receiver loads the envelope from their own
// inbox, which is the single source of truth for payload content.
type NotificationRef struct {
	MessageID      string `json:"message_id"`
	SenderIdentity string `json:"sender_identity"`
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
type Receiver struct {
	identity string
	rank     Rank
	store    *Store
}

// NewReceiver creates a Receiver scoped to the given identity and rank,
// backed by the store at the receiver's home directory.
func NewReceiver(homeDir, identity string, rank Rank) *Receiver {
	return &Receiver{
		identity: identity,
		rank:     rank,
		store:    NewStore(homeDir),
	}
}

// Process loads the envelope from the receiver's own inbox, validates all
// provenance fields (receiver identity, receiver rank, sender identity,
// payload hash), and writes an ack with the given outcome.
//
// Idempotent: calling with the same outcome for the same message is OK
// and returns the existing ack. Conflicting outcome fails closed.
//
// Validation order:
//  1. ref.MessageID and ref.SenderIdentity are non-empty
//  2. Envelope exists in the receiver's inbox for (ref.SenderIdentity, ref.MessageID)
//  3. Envelope ReceiverID matches receiver identity
//  4. Envelope ReceiverRank matches receiver rank
//  5. Envelope SenderIdentity matches ref.SenderIdentity
//  6. Payload hash matches envelope payload
//  7. Existing ack: same outcome = idempotent, different outcome = conflict
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

	// 3. Validate receiver identity (home provenance).
	if env.ReceiverID != r.identity {
		res.Err = fmt.Errorf("receiver identity mismatch: envelope has %q, receiver is %q",
			env.ReceiverID, r.identity)
		return res
	}

	// 4. Validate receiver rank.
	if env.ReceiverRank != r.rank {
		res.Err = fmt.Errorf("receiver rank mismatch: envelope has %q, receiver is %q",
			env.ReceiverRank, r.rank)
		return res
	}

	// 5. Validate sender identity matches ref.
	if env.SenderIdentity != ref.SenderIdentity {
		res.Err = fmt.Errorf("sender identity mismatch: envelope has %q, ref has %q",
			env.SenderIdentity, ref.SenderIdentity)
		return res
	}

	// 6. Validate payload hash (detect tampering).
	if env.PayloadHash != PayloadHashHex(env.Payload) {
		res.Err = fmt.Errorf("tampered payload: hash mismatch")
		return res
	}

	// 7. Check for existing ack.
	existing, err := r.store.ReadAck(ref.SenderIdentity, ref.MessageID)
	if err != nil {
		res.Err = fmt.Errorf("read existing ack: %w", err)
		return res
	}
	if existing != nil {
		if existing.Outcome == outcome {
			// Idempotent: same outcome, return equivalent ack.
			res.Ack = existing
			return res
		}
		// Conflicting outcome: fail closed.
		res.Err = fmt.Errorf("conflicting ack: existing outcome %q != new outcome %q",
			existing.Outcome, outcome)
		return res
	}

	// 8. Build and write ack.
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

// NotifyReceiver sends the notification reference text to the receiver's
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

	// Build notification text from the ref only — no payload included.
	text := fmt.Sprintf("notification: message=%s sender=%s",
		ref.MessageID, ref.SenderIdentity)

	result := session.SubmitPrompt(bk, windowID, text)
	nr.Acknowledged = result.Acknowledged()
	nr.Status = string(result.Status)
	nr.Detail = result.Detail
	if result.Err != nil {
		nr.Err = result.Err
	}
	return nr
}
