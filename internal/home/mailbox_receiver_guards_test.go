package home

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The refusal branches on the receiver side of the mailbox.
//
// Every case here builds a receiver whose inbox holds an envelope the receiver
// ACCEPTS, then breaks exactly one thing about that state. Each test asserts
// the accepted state first or repairs it afterwards, so the refusal is
// attributable to the single change and not to an earlier guard.

const (
	guardReceiverID = "captain-1"
	guardSenderID   = "general-home"
)

// newGuardReceiver returns a captain-home receiver plus the store over its own
// inbox. Identity is derived from the durable marker, exactly as production
// derives it — not passed in.
func newGuardReceiver(t *testing.T) (*Receiver, *Store, string) {
	t.Helper()
	dir := t.TempDir()
	if err := WriteHomeIdentity(dir, guardReceiverID, RankCaptain); err != nil {
		t.Fatalf("WriteHomeIdentity: %v", err)
	}
	r, err := NewReceiver(dir)
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	return r, NewStore(dir), dir
}

// deliverGuardEnvelope writes an envelope the receiver accepts and returns the
// ref that locates it.
func deliverGuardEnvelope(t *testing.T, store *Store) (Envelope, NotificationRef) {
	t.Helper()
	env := validGuardEnvelope()
	env.SenderIdentity = guardSenderID
	env.ReceiverID = guardReceiverID
	if err := store.WriteEnvelope(&env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	return env, NotificationRef{MessageID: env.MessageID, SenderIdentity: env.SenderIdentity}
}

// writeEnvelopeUnder writes an envelope into the inbox directory of senderDir
// regardless of what the envelope itself claims its sender is. WriteEnvelope
// derives the directory from the envelope, so this is the only way to build the
// on-disk state where the two disagree.
func writeEnvelopeUnder(t *testing.T, store *Store, senderDir string, env Envelope) {
	t.Helper()
	if err := os.MkdirAll(store.inboxDir(senderDir), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if err := os.WriteFile(store.inboxPath(senderDir, env.MessageID), data, 0644); err != nil {
		t.Fatalf("write envelope: %v", err)
	}
}

// A ref that names no message, or no sender, points at nothing: there is no
// inbox path to build from it, so both Receive and Ack refuse before any I/O.
func TestNotificationRefRefusesIncompleteRefs(t *testing.T) {
	runGuardCases(t,
		func() NotificationRef {
			return NotificationRef{MessageID: "msg1", SenderIdentity: guardSenderID}
		},
		NotificationRef.Validate,
		[]guardCase[NotificationRef]{
			{"no message id", func(r *NotificationRef) { r.MessageID = "" }, "empty message ID"},
			{"no sender identity", func(r *NotificationRef) { r.SenderIdentity = "" }, "empty sender identity"},
		})
}

// Receive and Ack each re-derive the same provenance checks independently —
// calling Ack without Receive is a supported flow — so both are driven through
// the same corruptions here rather than trusting one to stand in for the other.
func TestReceiveAndAckRefuseEnvelopesThatFailProvenance(t *testing.T) {
	cases := []struct {
		name    string
		break_  func(t *testing.T, store *Store, env Envelope, ref NotificationRef) NotificationRef
		wantSub string
	}{
		{
			// The ref names a message that was never delivered.
			name: "no envelope in the inbox",
			break_: func(t *testing.T, store *Store, env Envelope, ref NotificationRef) NotificationRef {
				return NotificationRef{MessageID: "msg-never-sent", SenderIdentity: ref.SenderIdentity}
			},
			wantSub: "envelope not found",
		},
		{
			name: "an envelope addressed to a different receiver",
			break_: func(t *testing.T, store *Store, env Envelope, ref NotificationRef) NotificationRef {
				env.ReceiverID = "captain-2"
				writeEnvelopeUnder(t, store, ref.SenderIdentity, env)
				return ref
			},
			wantSub: "receiver identity mismatch",
		},
		{
			// The envelope sits in general-home's inbox directory but claims a
			// different sender: the directory is what the ref resolves, the
			// field is what the envelope asserts, and they must agree.
			name: "an envelope whose sender does not match the ref",
			break_: func(t *testing.T, store *Store, env Envelope, ref NotificationRef) NotificationRef {
				env.SenderIdentity = "other-general"
				writeEnvelopeUnder(t, store, ref.SenderIdentity, env)
				return ref
			},
			wantSub: "sender identity mismatch",
		},
		{
			name: "an envelope that was superseded",
			break_: func(t *testing.T, store *Store, env Envelope, ref NotificationRef) NotificationRef {
				if err := store.MarkSuperseded(ref.SenderIdentity, ref.MessageID); err != nil {
					t.Fatalf("MarkSuperseded: %v", err)
				}
				return ref
			},
			wantSub: "superseded",
		},
		{
			// The envelope is addressed to this receiver's identity but to a
			// different rank. Identity and rank are separate assertions, and a
			// receiver must refuse work addressed to a rank it does not hold.
			name: "an envelope addressed to a different rank",
			break_: func(t *testing.T, store *Store, env Envelope, ref NotificationRef) NotificationRef {
				env.SenderRank, env.ReceiverRank = RankCaptain, RankSoldier
				writeEnvelopeUnder(t, store, ref.SenderIdentity, env)
				return ref
			},
			wantSub: "receiver rank mismatch",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, op := range []struct {
				name string
				call func(r *Receiver, ref NotificationRef) error
			}{
				{"Receive", func(r *Receiver, ref NotificationRef) error { _, err := r.Receive(ref); return err }},
				{"Ack", func(r *Receiver, ref NotificationRef) error { _, err := r.Ack(ref); return err }},
			} {
				t.Run(op.name, func(t *testing.T) {
					r, store, _ := newGuardReceiver(t)
					env, ref := deliverGuardEnvelope(t, store)

					// Control: the delivered envelope is accepted as-is, so a
					// refusal below cannot be blamed on the fixture.
					if err := op.call(r, ref); err != nil {
						t.Fatalf("%s refused the untouched fixture: %v", op.name, err)
					}

					broken := tc.break_(t, store, env, ref)
					err := op.call(r, broken)
					if err == nil {
						t.Fatalf("%s accepted %s", op.name, tc.name)
					}
					if !strings.Contains(err.Error(), tc.wantSub) {
						t.Fatalf("error = %v, want the %q refusal", err, tc.wantSub)
					}
				})
			}
		})
	}
}

// An ack already on disk is the receiver's own prior decision. A second Ack
// with the same outcome replays it; a different outcome on disk means somebody
// else recorded a conflicting decision for this message, and that fails closed
// rather than being overwritten.
func TestAckRefusesToOverwriteAConflictingOutcome(t *testing.T) {
	r, store, _ := newGuardReceiver(t)
	env, ref := deliverGuardEnvelope(t, store)

	conflicting := validGuardAck()
	conflicting.MessageID = env.MessageID
	conflicting.SenderIdentity = env.SenderIdentity
	conflicting.ReceiverID = env.ReceiverID
	conflicting.PayloadHash = env.PayloadHash
	conflicting.Outcome = OutcomeFailed
	if err := store.WriteAck(&conflicting); err != nil {
		t.Fatalf("WriteAck: %v", err)
	}

	_, err := r.Ack(ref)
	if err == nil {
		t.Fatal("Ack overwrote an existing conflicting outcome")
	}
	if !strings.Contains(err.Error(), "ack conflicting") {
		t.Fatalf("error = %v, want the conflicting-outcome refusal", err)
	}

	// Control: with an "accepted" ack on disk instead, the same branch is
	// entered and replays it — so the refusal above is the outcome comparison,
	// not the mere presence of an ack file.
	r2, store2, _ := newGuardReceiver(t)
	env2, ref2 := deliverGuardEnvelope(t, store2)
	first, err := r2.Ack(ref2)
	if err != nil {
		t.Fatalf("first Ack: %v", err)
	}
	second, err := r2.Ack(ref2)
	if err != nil {
		t.Fatalf("second Ack on the same message: %v", err)
	}
	if second.ProcessedAt != first.ProcessedAt || second.MessageID != env2.MessageID {
		t.Fatalf("replayed ack = %+v, want the original %+v", second, first)
	}
}

// Receiver identity is read from durable home provenance, never from a caller
// string, so a marker that cannot be parsed to an identity must not silently
// resolve to some default receiver.
func TestReadHomeIdentityRefusesUnusableMarkers(t *testing.T) {
	markerFor := func(t *testing.T, content string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, captainMarkerName), []byte(content), 0644); err != nil {
			t.Fatalf("write marker: %v", err)
		}
		return dir
	}

	// Control: a well-formed marker resolves to its captain identity.
	dir := markerFor(t, "munsu-v2\ncaptain-1\n/some/home\n")
	id, rank, err := ReadHomeIdentity(dir)
	if err != nil || id != "captain-1" || rank != RankCaptain {
		t.Fatalf("ReadHomeIdentity = (%q, %q, %v), want (captain-1, captain, nil)", id, rank, err)
	}

	for _, tc := range []struct {
		name    string
		content string
		wantSub string
	}{
		{"a marker with only a version line", "munsu-v2\n", "malformed home identity marker"},
		{"a marker from an unsupported version", "munsu-v1\ncaptain-1\n/some/home\n", "unsupported home identity version"},
		{"a marker with a blank identity line", "munsu-v2\n\n/some/home\n", "empty captain identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := ReadHomeIdentity(markerFor(t, tc.content)); err == nil {
				t.Fatalf("ReadHomeIdentity accepted %s", tc.name)
			} else if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %v, want the %q refusal", err, tc.wantSub)
			}
		})
	}

	// A home with no marker falls back to the directory basename. A path whose
	// basename is not a name — "." — yields no identity, so it refuses instead
	// of taking "." as one. t.Chdir keeps the relative path inside a fresh temp
	// dir that has no marker.
	t.Run("a markerless home whose basename is not an identity", func(t *testing.T) {
		t.Chdir(t.TempDir())
		if _, _, err := ReadHomeIdentity("."); err == nil {
			t.Fatal("ReadHomeIdentity derived an identity from a dot path")
		} else if !strings.Contains(err.Error(), "no marker and empty basename") {
			t.Fatalf("error = %v, want the underivable-identity refusal", err)
		}
	})
}

// WriteHomeIdentity is what provisioning uses to make a home addressable. A
// marker with no identity or an unknown rank would make every later receive
// unattributable, so it is refused at write time.
func TestWriteHomeIdentityRefusesUnusableIdentities(t *testing.T) {
	dir := t.TempDir()

	// Control: the same call with a valid identity and rank writes the marker.
	if err := WriteHomeIdentity(dir, guardReceiverID, RankCaptain); err != nil {
		t.Fatalf("WriteHomeIdentity: %v", err)
	}

	if err := WriteHomeIdentity(dir, "", RankCaptain); err == nil {
		t.Fatal("WriteHomeIdentity accepted an empty identity")
	} else if !strings.Contains(err.Error(), "empty identity") {
		t.Fatalf("error = %v, want the empty-identity refusal", err)
	}

	if err := WriteHomeIdentity(dir, guardReceiverID, Rank("admiral")); err == nil {
		t.Fatal("WriteHomeIdentity accepted an unknown rank")
	} else if !strings.Contains(err.Error(), "invalid rank") {
		t.Fatalf("error = %v, want the invalid-rank refusal", err)
	}
}
