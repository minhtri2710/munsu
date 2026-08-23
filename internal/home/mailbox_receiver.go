package home

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
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

// Receiver provides receiver-side processing for one receiver identity.
// It loads envelopes from the receiver's own inbox and validates all
// provenance fields before writing acks.
//
// Identity and rank are derived from durable files rather than from
// caller-provided strings. Which durable file depends on whether the
// receiver owns a home: a general or captain owns the home it runs in, so
// NewReceiver reads home provenance. A soldier owns no home -- it is
// launched with MUNSU_HOME set to its dispatcher's home -- so
// NewSoldierReceiver reads the durable per-task record that home holds for
// the soldier instead.
//
// The Receiver exposes two independent operations:
//   - Receive: validate and load the envelope, returning the payload.
//     Writes NO ack. Used by the captain agent to inspect what was sent.
//   - Ack: write the exact "accepted" ack after the agent has taken
//     the command into its context. Pending remains until the sender
//     reconciles via converge. Always writes outcome "accepted".
type Receiver struct {
	identity string
	rank     Rank
	taskID   string
	store    *Store
}

// NewReceiver creates a Receiver for the owner of homeDir: the captain whose
// .munsu-captain-home marker it carries, or the general whose home it is.
// Identity and rank come from durable home provenance instead of caller
// strings. Soldiers are not home owners -- see NewSoldierReceiver.
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

// ReceiverIDForTask returns the mailbox ReceiverID that addresses a task.
// Task IDs carry characters that are rejected as path components (colons in
// "task:foo", separators); ReceiverID never names a file -- inbox paths are
// built from SenderIdentity and MessageID -- so those characters are folded
// to underscores. This is the one sanitizer: the sender that addresses a
// soldier and the soldier that identifies itself must agree exactly.
func ReceiverIDForTask(taskID string) string {
	return strings.NewReplacer(":", "_", "/", "_", "\\", "_", "..", "_").Replace(taskID)
}

// NewSoldierReceiver creates a Receiver for the soldier task that homeDir
// hosts. A soldier has no home of its own: it is launched with MUNSU_HOME
// set to the home of the general or captain that dispatched it, so home
// provenance there names the dispatcher and can never name the soldier.
//
// The durable statement that this home hosts this soldier is the task meta
// file the home keeps for it. Without that record there is no soldier to be,
// and construction fails closed. The trust boundary for this check is the
// home directory, not the task: it establishes that this home durably hosts
// the task, just as ReadHomeIdentity establishes that a home is a captain
// home. Neither check authenticates the caller.
func NewSoldierReceiver(homeDir, taskID string) (*Receiver, error) {
	if _, err := MetaFilePath(homeDir, taskID); err != nil {
		return nil, fmt.Errorf("new soldier receiver: %w", err)
	}
	meta, readErr := ReadMeta(homeDir, taskID)
	if readErr != nil {
		return nil, fmt.Errorf("new soldier receiver: home %s hosts no task %q (unreadable): %w", homeDir, taskID, readErr)
	}
	if meta["kind"] == "captain" {
		return nil, fmt.Errorf("new soldier receiver: task %q is a captain task", taskID)
	}
	return &Receiver{
		identity: ReceiverIDForTask(taskID),
		rank:     RankSoldier,
		taskID:   taskID,
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
			info, statErr := os.Stat(homeDir)
			if statErr != nil {
				return "", "", fmt.Errorf("reading markerless home %s: %w", homeDir, statErr)
			}
			if !info.IsDir() {
				return "", "", fmt.Errorf("reading markerless home %s: not a directory", homeDir)
			}
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
// For general homes, identity is derived from the directory basename. There
// is no soldier marker to write: a soldier is a task inside its dispatcher's
// home, so its provenance is that home's task record, not a home marker.
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

// verifySenderRank verifies the rank claimed by env against the one durable
// provenance record that can prove that claim for this receiver. The claim
// selects the proof obligation, never whether proof is needed, so a task
// identity collision cannot make one provenance record shadow another.
func (r *Receiver) verifySenderRank(env *Envelope) error {
	switch env.SenderRank {
	case RankSoldier:
		if r.rank != RankGeneral && r.rank != RankCaptain {
			return fmt.Errorf("soldier sender cannot address receiver rank %q", r.rank)
		}
		if env.TaskID == "" || env.SenderIdentity != ReceiverIDForTask(env.TaskID) {
			return fmt.Errorf("soldier sender identity does not match task %q", env.TaskID)
		}
		meta, err := ReadMeta(r.store.homeDir, env.TaskID)
		if err != nil {
			return fmt.Errorf("reading soldier sender provenance: %w", err)
		}
		if meta["kind"] == "captain" {
			return fmt.Errorf("soldier sender provenance names a captain task")
		}
		return nil
	case RankCaptain:
		switch r.rank {
		case RankGeneral:
			meta, err := ReadMeta(r.store.homeDir, "captain:"+env.SenderIdentity)
			if err != nil {
				return fmt.Errorf("reading captain sender provenance: %w", err)
			}
			if meta["kind"] != "captain" {
				return fmt.Errorf("captain sender provenance has kind %q", meta["kind"])
			}
			captainHome := meta["home"]
			if captainHome == "" {
				return fmt.Errorf("captain sender provenance has no home")
			}
			identity, rank, err := ReadHomeIdentity(captainHome)
			if err != nil {
				return fmt.Errorf("reading captain sender home provenance: %w", err)
			}
			if identity != env.SenderIdentity || rank != RankCaptain {
				return fmt.Errorf("captain home provenance is (%q, %q), want (%q, %q)", identity, rank, env.SenderIdentity, RankCaptain)
			}
			return nil
		case RankSoldier:
			identity, rank, err := ReadHomeIdentity(r.store.homeDir)
			if err != nil {
				return fmt.Errorf("reading soldier hosting home provenance: %w", err)
			}
			if identity != env.SenderIdentity || rank != RankCaptain {
				return fmt.Errorf("hosting home provenance is (%q, %q), want (%q, %q)", identity, rank, env.SenderIdentity, RankCaptain)
			}
			return nil
		default:
			return fmt.Errorf("captain sender cannot address receiver rank %q", r.rank)
		}
	case RankGeneral:
		switch r.rank {
		case RankCaptain:
			parentHome, err := config.Get(r.store.homeDir, "parent-home")
			if err != nil {
				return fmt.Errorf("reading captain parent home: %w", err)
			}
			identity, rank, err := ReadHomeIdentity(parentHome)
			if err != nil {
				return fmt.Errorf("reading parent home provenance: %w", err)
			}
			if identity != env.SenderIdentity || rank != RankGeneral {
				return fmt.Errorf("parent home provenance is (%q, %q), want (%q, %q)", identity, rank, env.SenderIdentity, RankGeneral)
			}
			return nil
		case RankSoldier:
			identity, rank, err := ReadHomeIdentity(r.store.homeDir)
			if err != nil {
				return fmt.Errorf("reading soldier hosting home provenance: %w", err)
			}
			if identity != env.SenderIdentity || rank != RankGeneral {
				return fmt.Errorf("hosting home provenance is (%q, %q), want (%q, %q)", identity, rank, env.SenderIdentity, RankGeneral)
			}
			return nil
		default:
			return fmt.Errorf("general sender cannot address receiver rank %q", r.rank)
		}
	default:
		return fmt.Errorf("unsupported sender rank %q", env.SenderRank)
	}
}

// Receive validates and loads an envelope from the receiver's inbox for the
// given NotificationRef. It validates all provenance fields (receiver identity,
// receiver rank, sender identity, payload hash) and returns the envelope.
//
// Writes NO ack — this is purely a read/validate operation. The captain agent
// calls Receive to inspect the incoming envelope payload, then independently
// calls Ack once the command has been accepted into agent context.
//
// Validation order:
//  1. ref.MessageID and ref.SenderIdentity are non-empty
//  2. Envelope exists in the receiver's inbox for (ref.SenderIdentity, ref.MessageID)
//  3. Full ValidateEnvelope (rank transition, task/key, hash completeness)
//  4. Envelope ReceiverID matches receiver identity (home provenance)
//  5. Envelope ReceiverRank matches receiver rank (home provenance)
//  6. Envelope SenderIdentity matches ref.SenderIdentity
//  7. Envelope SenderRank is proven by the claim-directed durable provenance
//     check (see verifySenderRank)
//  8. Payload hash matches envelope payload (redundant after ValidateEnvelope)
//  9. No ack is written — that is a separate Ack() call
func (r *Receiver) Receive(ref NotificationRef) (*Envelope, error) {
	// 1. Validate ref.
	if err := ref.Validate(); err != nil {
		return nil, fmt.Errorf("receive: %w", err)
	}
	if r.store.IsSuperseded(ref.SenderIdentity, ref.MessageID) {
		return nil, fmt.Errorf("receive envelope superseded: sender=%s msg=%s", ref.SenderIdentity, ref.MessageID)
	}

	// 2. Load envelope from own inbox.
	env, err := r.store.ReadEnvelope(ref.SenderIdentity, ref.MessageID)
	if err != nil {
		return nil, fmt.Errorf("receive read envelope: %w", err)
	}
	if env == nil {
		return nil, fmt.Errorf("receive envelope not found: sender=%s msg=%s",
			ref.SenderIdentity, ref.MessageID)
	}

	// 3. Call full ValidateEnvelope for rank transition, task/key, hash completeness.
	if err := ValidateEnvelope(env); err != nil {
		return nil, fmt.Errorf("receive validate envelope: %w", err)
	}

	// 4. Validate receiver identity (home provenance).
	if env.ReceiverID != r.identity {
		return nil, fmt.Errorf("receive receiver identity mismatch: envelope has %q, receiver is %q",
			env.ReceiverID, r.identity)
	}

	// 5. Validate receiver rank.
	if env.ReceiverRank != r.rank {
		return nil, fmt.Errorf("receive receiver rank mismatch: envelope has %q, receiver is %q",
			env.ReceiverRank, r.rank)
	}

	if r.taskID != "" && env.TaskID != r.taskID {
		return nil, fmt.Errorf("receive task ID mismatch: envelope has %q, receiver is %q",
			env.TaskID, r.taskID)
	}

	// 6. Validate sender identity matches ref.
	if env.SenderIdentity != ref.SenderIdentity {
		return nil, fmt.Errorf("receive sender identity mismatch: envelope has %q, ref has %q",
			env.SenderIdentity, ref.SenderIdentity)
	}

	// 7. Validate sender rank against provenance the receiving home can
	// establish, so the one-hop transition table is enforced against a
	// derived value rather than a self-reported one.
	if err := r.verifySenderRank(env); err != nil {
		return nil, fmt.Errorf("receive sender rank underivable: %w", err)
	}

	// 8. Validate payload hash (detect tampering).
	// This is redundant after ValidateEnvelope but kept as defense-in-depth.
	if env.PayloadHash != PayloadHashHex(env.Payload) {
		return nil, fmt.Errorf("receive tampered payload: hash mismatch")
	}

	// 9. No ack is written — call Ack() separately after accepting into context.
	return env, nil
}

// Ack writes a fixed "accepted" ProcessingAck for the given NotificationRef,
// independently performing the same full validation as Receive.
//
// The ack means the captain agent has taken the command into its agent
// context — NOT that the command completed. Completion is tracked through
// separate report/relay flows (munsu report).
//
// Calling Ack without calling Receive first is valid — Ack performs its own
// validation independent of Receive. Each call is self-contained.
//
// Idempotent: calling with the same "accepted" outcome for the same message
// is OK and returns the existing ack (with original timestamp preserved).
// Any conflicting outcome on disk fails closed.
//
// Validation order:
//  1. ref.MessageID and ref.SenderIdentity are non-empty
//  2. Envelope exists in the receiver's inbox
//  3. Full ValidateEnvelope
//  4. Envelope ReceiverID matches receiver identity
//  5. Envelope ReceiverRank matches receiver rank
//  6. Envelope SenderIdentity matches ref.SenderIdentity
//  7. Payload hash verification
//  8. Existing ack: same outcome "accepted" = idempotent, different = conflict
//     (fail closed); replaying a persisted decision does not require current
//     sender provenance
//  9. Envelope SenderRank is proven by the claim-directed durable provenance
//     check (see verifySenderRank); current provenance is required before
//     writing a new ack
//  10. Write "accepted" ack
func (r *Receiver) Ack(ref NotificationRef) (*ProcessingAck, error) {
	// 1. Validate ref.
	if err := ref.Validate(); err != nil {
		return nil, fmt.Errorf("ack: %w", err)
	}
	if r.store.IsSuperseded(ref.SenderIdentity, ref.MessageID) {
		return nil, fmt.Errorf("ack envelope superseded: sender=%s msg=%s", ref.SenderIdentity, ref.MessageID)
	}

	// 2. Load envelope from own inbox.
	env, err := r.store.ReadEnvelope(ref.SenderIdentity, ref.MessageID)
	if err != nil {
		return nil, fmt.Errorf("ack read envelope: %w", err)
	}
	if env == nil {
		return nil, fmt.Errorf("ack envelope not found: sender=%s msg=%s",
			ref.SenderIdentity, ref.MessageID)
	}

	// 3. Call full ValidateEnvelope for rank transition, task/key, hash completeness.
	if err := ValidateEnvelope(env); err != nil {
		return nil, fmt.Errorf("ack validate envelope: %w", err)
	}

	// 4. Validate receiver identity (home provenance).
	if env.ReceiverID != r.identity {
		return nil, fmt.Errorf("ack receiver identity mismatch: envelope has %q, receiver is %q",
			env.ReceiverID, r.identity)
	}

	// 5. Validate receiver rank.
	if env.ReceiverRank != r.rank {
		return nil, fmt.Errorf("ack receiver rank mismatch: envelope has %q, receiver is %q",
			env.ReceiverRank, r.rank)
	}

	if r.taskID != "" && env.TaskID != r.taskID {
		return nil, fmt.Errorf("ack task ID mismatch: envelope has %q, receiver is %q",
			env.TaskID, r.taskID)
	}

	// 6. Validate sender identity matches ref.
	if env.SenderIdentity != ref.SenderIdentity {
		return nil, fmt.Errorf("ack sender identity mismatch: envelope has %q, ref has %q",
			env.SenderIdentity, ref.SenderIdentity)
	}

	// 7. Validate payload hash (detect tampering).
	if env.PayloadHash != PayloadHashHex(env.Payload) {
		return nil, fmt.Errorf("ack tampered payload: hash mismatch")
	}

	// 8. Check for existing ack before requiring current sender provenance.
	existing, err := r.store.ReadAck(ref.SenderIdentity, ref.MessageID)
	if err != nil {
		return nil, fmt.Errorf("ack read existing ack: %w", err)
	}
	if existing != nil {
		if existing.Outcome == OutcomeAccepted {
			// Idempotent: same outcome, return existing ack preserving
			// the original ProcessedAt timestamp.
			return existing, nil
		}
		// Conflicting outcome: fail closed.
		return nil, fmt.Errorf("ack conflicting: existing outcome %q != %q",
			existing.Outcome, OutcomeAccepted)
	}

	// 9. Validate sender rank against provenance the receiving home can
	// establish, so the one-hop transition table is enforced against a
	// derived value rather than a self-reported one.
	if err := r.verifySenderRank(env); err != nil {
		return nil, fmt.Errorf("ack sender rank underivable: %w", err)
	}

	// 10. Build and write "accepted" ack.
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
		Outcome:        OutcomeAccepted,
	}
	if err := r.store.WriteAck(ack); err != nil {
		return nil, fmt.Errorf("ack write ack: %w", err)
	}
	return ack, nil
}

// NotifyResult carries the outcome of a notification delivery attempt.
type NotifyResult struct {
	Ref          NotificationRef
	Acknowledged bool
	Status       string
	Detail       string
	Err          error
}

// NotifyReceiverWithSender sends the canonical NotificationRef text through
// the supplied task-bound sender. The receiver is expected to read their
// own inbox using the ref to locate the envelope.
//
// Submission acknowledgment (Acknowledged=true) never removes the sender's
// pending record. Pending records are managed separately through the ack
// flow via the sender's RemovePendingAfterAck.
func NotifyReceiverWithSender(sender BoundSender, receiverHome string, ref NotificationRef, meta map[string]string) *NotifyResult {
	nr := &NotifyResult{Ref: ref}

	if _, err := sender.Alive(receiverHome, meta); err != nil {
		nr.Err = fmt.Errorf("resolve bound sender: %w", err)
		return nr
	}

	// Build notification text from the ref using canonical Encode — no
	// payload included. Raw payload is not routing authority.
	text := ref.Encode()

	sent := sender.Send(receiverHome, meta, text)
	nr.Acknowledged, nr.Status, nr.Detail, nr.Err = sent.Acknowledged, sent.Status, sent.Detail, sent.Err
	return nr
}
