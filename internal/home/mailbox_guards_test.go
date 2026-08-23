package home

import (
	"strings"
	"testing"
)

// The refusal branches of the mailbox validators.
//
// Each case takes a record the validator ACCEPTS and breaks exactly one field.
// The accepted fixture is asserted first, once per validator, so a case that
// refuses can only be refusing because of the field it broke: if an earlier
// guard were what fired, the untouched fixture would already be rejected. That
// control is what BEO-87 found missing — two tests asserted a refusal message
// while an earlier guard was what produced it.
//
// Messages are asserted individually rather than `err != nil`, because every
// validator here refuses many different ways with the same error type.

// guardCase breaks one field of an otherwise-accepted record.
type guardCase[T any] struct {
	name    string
	corrupt func(*T)
	wantSub string
}

// runGuardCases asserts the fixture is accepted, then that each single-field
// corruption is refused with its own message.
func runGuardCases[T any](t *testing.T, valid func() T, validate func(T) error, cases []guardCase[T]) {
	t.Helper()
	if err := validate(valid()); err != nil {
		t.Fatalf("fixture is not accepted, so no case below can attribute its refusal: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := valid()
			tc.corrupt(&rec)
			err := validate(rec)
			if err == nil {
				t.Fatalf("validator accepted a record with %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %v, want the %q refusal", err, tc.wantSub)
			}
		})
	}
}

// validGuardEnvelope is a general->captain envelope ValidateEnvelope accepts.
func validGuardEnvelope() Envelope {
	payload := "spawn task t1"
	return Envelope{
		SchemaVersion:  SchemaVersion,
		MessageID:      "msg1",
		SenderRank:     RankGeneral,
		SenderIdentity: "general-home",
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Kind:           "command",
		TaskID:         "t1",
		Key:            "spawn",
		Payload:        payload,
		PayloadHash:    PayloadHashHex(payload),
		CreatedAt:      1700000000,
	}
}

// validGuardAck is the ack ValidateAck accepts against validGuardEnvelope.
func validGuardAck() ProcessingAck {
	env := validGuardEnvelope()
	return ProcessingAck{
		SchemaVersion:  AckSchemaVersion,
		MessageID:      env.MessageID,
		SenderRank:     env.SenderRank,
		SenderIdentity: env.SenderIdentity,
		ReceiverRank:   env.ReceiverRank,
		ReceiverID:     env.ReceiverID,
		TaskID:         env.TaskID,
		Key:            env.Key,
		PayloadHash:    env.PayloadHash,
		ProcessedAt:    1700000001,
		Outcome:        OutcomeAccepted,
	}
}

// An envelope with no routing identity, no payload, or a payload that does not
// hash to its recorded digest cannot be delivered or attributed, so it is
// refused at validation rather than written into an inbox.
func TestValidateEnvelopeRefusesIncompleteOrTamperedEnvelopes(t *testing.T) {
	runGuardCases(t, validGuardEnvelope,
		func(env Envelope) error { return ValidateEnvelope(&env) },
		[]guardCase[Envelope]{
			{"no message id", func(e *Envelope) { e.MessageID = "" }, "empty message ID"},
			{"no sender identity", func(e *Envelope) { e.SenderIdentity = "" }, "empty sender identity"},
			{"no receiver identity", func(e *Envelope) { e.ReceiverID = "" }, "empty receiver identity"},
			{"no payload", func(e *Envelope) { e.Payload = "" }, "empty payload"},
			// The payload is rewritten, not the digest: that is the shape a
			// tampered inbox file has on disk.
			{"payload that does not match its digest", func(e *Envelope) { e.Payload = "rm -rf /" }, "payload hash mismatch"},
		})
}

// The ack is what lets a sender drop its pending record, so every field it
// carries must match the envelope exactly. A mismatch on any one of them means
// the ack is for some other message and must not retire this one.
func TestValidateAckRefusesAckThatDoesNotMatchTheEnvelope(t *testing.T) {
	env := validGuardEnvelope()
	runGuardCases(t, validGuardAck,
		func(ack ProcessingAck) error { return ValidateAck(&env, &ack) },
		[]guardCase[ProcessingAck]{
			{"a different message id", func(a *ProcessingAck) { a.MessageID = "msg2" }, "message ID mismatch"},
			{"a different sender identity", func(a *ProcessingAck) { a.SenderIdentity = "other-general" }, "sender identity mismatch"},
			{"a different receiver id", func(a *ProcessingAck) { a.ReceiverID = "captain-2" }, "receiver ID mismatch"},
			{"a different task id", func(a *ProcessingAck) { a.TaskID = "t2" }, "task ID mismatch"},
			{"a different key", func(a *ProcessingAck) { a.Key = "retire" }, "key mismatch"},
			{"a different payload hash", func(a *ProcessingAck) { a.PayloadHash = PayloadHashHex("something else") }, "payload hash mismatch"},
		})
}

// The ack's own fields carry when it was processed and what the outcome was.
// An unknown outcome or an absent timestamp makes the record unusable as
// evidence, independently of whether it matches an envelope.
func TestValidateProcessingAckRefusesUnusableAckRecords(t *testing.T) {
	runGuardCases(t, validGuardAck,
		func(ack ProcessingAck) error { return ValidateProcessingAck(&ack) },
		[]guardCase[ProcessingAck]{
			{"no processed timestamp", func(a *ProcessingAck) { a.ProcessedAt = 0 }, "processed_at must be > 0"},
			{"a negative processed timestamp", func(a *ProcessingAck) { a.ProcessedAt = -1 }, "processed_at must be > 0"},
			{"an outcome outside the known set", func(a *ProcessingAck) { a.Outcome = "finished" }, "invalid outcome"},
		})
}

// Identities and message IDs become path segments in the inbox and outbox, so
// anything that could traverse out of the mailbox tree is refused before a
// path is ever built from it.
func TestValidatePathComponentRefusesTraversalCapableComponents(t *testing.T) {
	runGuardCases(t,
		func() string { return "captain-1" },
		func(component string) error { return ValidatePathComponent(component, "sender identity") },
		[]guardCase[string]{
			{"an empty component", func(s *string) { *s = "" }, "empty"},
			{"a slash", func(s *string) { *s = "captain-1/state" }, "contains slash"},
			{"a backslash", func(s *string) { *s = `captain-1\state` }, "contains backslash"},
			// Without a slash: the slash guard runs first, so "../x" would be
			// refused before this branch is reached.
			{"a relative path", func(s *string) { *s = "captain..1" }, "contains relative path"},
			{"a colon", func(s *string) { *s = "host:captain-1" }, "contains colon"},
		})
}

// WriteAck revalidates the ack's own fields at the durable boundary rather
// than trusting the caller to have run ValidateProcessingAck first: a record
// with no processed time or an unknown outcome must never reach the inbox.
func TestStoreWriteAckRefusesUnusableAckRecords(t *testing.T) {
	store := NewStore(t.TempDir())
	runGuardCases(t, validGuardAck,
		func(ack ProcessingAck) error { return store.WriteAck(&ack) },
		[]guardCase[ProcessingAck]{
			{"no processed timestamp", func(a *ProcessingAck) { a.ProcessedAt = 0 }, "processed_at must be > 0"},
			{"a negative processed timestamp", func(a *ProcessingAck) { a.ProcessedAt = -1 }, "processed_at must be > 0"},
			{"an outcome outside the known set", func(a *ProcessingAck) { a.Outcome = "finished" }, "invalid outcome"},
		})
}

// A pending record is the sender's only evidence that a message is still
// outstanding. Removing one without an ack in hand would lose that evidence,
// so a nil ack fails closed instead of being read as "nothing to check".
func TestRemovePendingAfterAckRefusesNilAck(t *testing.T) {
	store := NewStore(t.TempDir())
	env := validGuardEnvelope()
	if err := store.WritePending(&env); err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	if err := store.RemovePendingAfterAck(env.SenderIdentity, env.MessageID, nil); err == nil {
		t.Fatal("RemovePendingAfterAck removed a pending record with no ack")
	} else if !strings.Contains(err.Error(), "ack is nil") {
		t.Fatalf("error = %v, want the nil-ack refusal", err)
	}

	// Control: the same pending record IS removed once a matching ack is
	// supplied, so the refusal above came from the nil ack and not from the
	// fixture being unremovable.
	ack := validGuardAck()
	if err := store.RemovePendingAfterAck(env.SenderIdentity, env.MessageID, &ack); err != nil {
		t.Fatalf("RemovePendingAfterAck with a matching ack: %v", err)
	}
	pending, err := store.ReadPending(env.SenderIdentity, env.MessageID)
	if err != nil {
		t.Fatalf("ReadPending: %v", err)
	}
	if pending != nil {
		t.Fatal("pending record survived a validated ack")
	}
}

// The legacy single-home helper reads the ack itself instead of taking one, so
// it has its own copy of the same rule: no ack on disk, no removal.
func TestRemoveSenderPendingRefusesWhenNoAckExists(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	env := validGuardEnvelope()
	if err := store.WritePending(&env); err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	if err := RemoveSenderPending(dir, env.SenderIdentity, env.MessageID); err == nil {
		t.Fatal("RemoveSenderPending removed a pending record with no ack on disk")
	} else if !strings.Contains(err.Error(), "no ack found for message") {
		t.Fatalf("error = %v, want the missing-ack refusal", err)
	}

	// Control: writing the matching ack is the only difference, and it makes
	// the same call succeed.
	ack := validGuardAck()
	if err := store.WriteAck(&ack); err != nil {
		t.Fatalf("WriteAck: %v", err)
	}
	if err := RemoveSenderPending(dir, env.SenderIdentity, env.MessageID); err != nil {
		t.Fatalf("RemoveSenderPending with an ack on disk: %v", err)
	}
}

// Rank is what makes the mailbox one-hop. An unrecognised rank has no place in
// the hierarchy, and same-rank routes remain refused; recognized cross-rank
// routes follow the explicit transition table. These branches were invisible
// to the guards lane until BEO-123 fixed the instrument, which had been
// reading SenderRank and ReceiverRank as error values on their names.
func TestValidateEnvelopeRefusesInvalidRanksAndIllegalTransitions(t *testing.T) {
	runGuardCases(t, validGuardEnvelope,
		func(env Envelope) error { return ValidateEnvelope(&env) },
		[]guardCase[Envelope]{
			{"an unrecognised sender rank", func(e *Envelope) { e.SenderRank = Rank("admiral") }, "invalid sender rank"},
			{"an unrecognised receiver rank", func(e *Envelope) { e.ReceiverRank = Rank("admiral") }, "invalid receiver rank"},
			{"a general addressing another general", func(e *Envelope) {
				e.SenderRank, e.ReceiverRank = RankGeneral, RankGeneral
			}, "general can only send to captain"},
			{"a captain addressing another captain", func(e *Envelope) {
				e.SenderRank, e.ReceiverRank = RankCaptain, RankCaptain
			}, "captain can only send to general or soldier"},
			{"a soldier addressing another soldier", func(e *Envelope) {
				e.SenderRank, e.ReceiverRank = RankSoldier, RankSoldier
			}, "soldier can only send to captain or general"},
		})
}

// The Soldier -> General edge is the one direct-dispatch hop the matrix section
// 1.1 report row depends on: with no Captain in the topology, the soldier's
// parent home is the General itself. Admitting it is what lets the receiving
// General accept its own inbox item (issue #562); it is not a general
// relaxation, which is why the refusals above still stand.
func TestValidateEnvelopeAdmitsSoldierToGeneralDirectDispatch(t *testing.T) {
	env := validGuardEnvelope()
	env.SenderRank, env.ReceiverRank = RankSoldier, RankGeneral
	if err := ValidateEnvelope(&env); err != nil {
		t.Fatalf("soldier -> general under direct dispatch must be legal: %v", err)
	}
}

// The rank halves of the ack. An ack whose ranks disagree with the envelope is
// an ack for a different hop, and retiring the pending record on it would drop
// a message that was never processed.
func TestValidateAckRefusesRanksThatDoNotMatchTheEnvelope(t *testing.T) {
	env := validGuardEnvelope()
	runGuardCases(t, validGuardAck,
		func(ack ProcessingAck) error { return ValidateAck(&env, &ack) },
		[]guardCase[ProcessingAck]{
			{"a different sender rank", func(a *ProcessingAck) { a.SenderRank = RankCaptain }, "sender rank mismatch"},
			{"a different receiver rank", func(a *ProcessingAck) { a.ReceiverRank = RankSoldier }, "receiver rank mismatch"},
		})
}

// The inbox is keyed by message ID, so a second write under an ID that already
// exists is either a replay or a collision. Identical content replays
// harmlessly; different content under the same ID means two different messages
// are claiming one identity, and the store refuses rather than overwriting the
// one already delivered.
func TestStoreWriteEnvelopeRefusesConflictingContentUnderOneMessageID(t *testing.T) {
	store := NewStore(t.TempDir())
	env := validGuardEnvelope()
	if err := store.WriteEnvelope(&env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	// Control: the identical envelope replays without error.
	replay := validGuardEnvelope()
	if err := store.WriteEnvelope(&replay); err != nil {
		t.Fatalf("identical rewrite must be idempotent, got: %v", err)
	}
	conflicting := validGuardEnvelope()
	conflicting.Payload = "spawn task t2"
	conflicting.PayloadHash = PayloadHashHex(conflicting.Payload)
	err := store.WriteEnvelope(&conflicting)
	if err == nil {
		t.Fatal("store accepted a different payload under an existing message ID")
	}
	if !strings.Contains(err.Error(), "already exists with different content") {
		t.Fatalf("error = %v, want the conflict refusal", err)
	}
}
