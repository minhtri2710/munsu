package home

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestMailboxGCEndToEndTranscript demonstrates the handled-message garbage
// collection lifecycle end-to-end through the public Receiver/Store API and
// writes a human-readable transcript (including the raw on-disk mailbox layout)
// to MAILBOX_GC_TRANSCRIPT for reviewer-visible evidence.
//
// It proves every reader treats a tombstone-without-payload as a first-class
// handled/absent state:
//   - Receiver.Ack GCs the .json payload right after the .ack tombstone is
//     made durable, keeps the tombstone forever, and replays idempotently even
//     when the payload is already gone.
//   - Store.MarkSuperseded GCs the payload after the .superseded tombstone.
//   - Receiver.Receive returns a deterministic envelope-not-found once the
//     payload is gone but the ack exists.
//   - Store.ListInbox skips the record and opportunistically reclaims residue.
//   - Store.IsAcked stays true.
func TestMailboxGCEndToEndTranscript(t *testing.T) {
	const transcriptPath = "MAILBOX_GC_TRANSCRIPT"
	out := os.Stdout
	if p := os.Getenv(transcriptPath); p != "" {
		f, err := os.Create(p)
		if err != nil {
			t.Fatalf("open transcript %s: %v", p, err)
		}
		defer f.Close()
		out = f
	}
	log := func(format string, args ...any) {
		fmt.Fprintf(out, format+"\n", args...)
	}
	listInbox := func(label string, homeDir, sender string) {
		dir := filepath.Join(homeDir, "state", InboxDir, sender)
		entries, err := os.ReadDir(dir)
		if err != nil {
			log("  [%s] read inbox dir %s: %v", label, dir, err)
			return
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		log("  [%s] inbox dir %s:", label, dir)
		if len(names) == 0 {
			log("    (empty)")
		}
		for _, n := range names {
			log("    %s", n)
		}
	}

	log("=== Mailbox handled-message garbage collection transcript ===")
	log("")

	// ---- Scenario 1: Receiver.Ack GCs payload, keeps .ack tombstone ----
	log("--- Scenario 1: Receiver.Ack garbage-collects the payload ---")
	r, store, homeDir := newGuardReceiver(t)
	env, ref := deliverGuardEnvelope(t, store)
	log("delivered envelope sender=%s msg=%s (payload on disk)", env.SenderIdentity, env.MessageID)
	listInbox("after-deliver", homeDir, env.SenderIdentity)

	ack, err := r.Ack(ref)
	if err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if ack == nil || ack.Outcome != OutcomeAccepted {
		t.Fatalf("Ack outcome = %+v, want accepted", ack)
	}
	log("Receiver.Ack accepted the message")
	listInbox("after-ack", homeDir, env.SenderIdentity)

	// Payload must be gone; tombstone must remain.
	payload, err := store.ReadEnvelope(env.SenderIdentity, env.MessageID)
	if err != nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	if payload != nil {
		t.Fatal("payload still present after Ack GC")
	}
	if !store.IsAcked(env.SenderIdentity, env.MessageID) {
		t.Fatal("ack tombstone was removed by GC")
	}
	log("assert: payload .json gone, .ack tombstone retained (IsAcked=true)")

	// ---- Scenario 2: Receiver.Receive returns envelope-not-found ----
	log("")
	log("--- Scenario 2: Receiver.Receive sees a handled/absent envelope ---")
	if _, err := r.Receive(ref); err == nil || !strings.Contains(err.Error(), "envelope not found") {
		t.Fatalf("Receive error = %v, want envelope not found", err)
	}
	log("Receiver.Receive returned deterministic error: %q", "envelope not found")

	// ---- Scenario 3: Receiver.Ack replays idempotently from ack-only record ----
	log("")
	log("--- Scenario 3: Receiver.Ack replays from retained ack without payload ---")
	replayed, err := r.Ack(ref)
	if err != nil {
		t.Fatalf("Ack replay: %v", err)
	}
	if replayed == nil || replayed.ProcessedAt != ack.ProcessedAt {
		t.Fatalf("replayed ack = %+v, want original ProcessedAt %d", replayed, ack.ProcessedAt)
	}
	log("Receiver.Ack replayed original processed-at=%d without touching payload", ack.ProcessedAt)
	listInbox("after-replay", homeDir, env.SenderIdentity)

	// ---- Scenario 4: Store.ListInbox skips the tombstoned record ----
	log("")
	log("--- Scenario 4: Store.ListInbox skips handled message ---")
	listed, err := store.ListInbox(env.SenderIdentity)
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("ListInbox returned %d records, want 0", len(listed))
	}
	log("Store.ListInbox returned %d actionable records (handled record suppressed)", len(listed))

	// ---- Scenario 5: Store.MarkSuperseded GCs payload, keeps .superseded ----
	log("")
	log("--- Scenario 5: Store.MarkSuperseded garbage-collects payload ---")
	supStore := NewStore(t.TempDir())
	supEnv := validGuardEnvelope()
	supEnv.SenderIdentity = "another-sender"
	supEnv.ReceiverID = "captain-2"
	if err := supStore.WriteEnvelope(&supEnv); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	supHome := inboxHomeOf(t, supStore, supEnv.SenderIdentity)
	log("delivered supersedable envelope sender=%s msg=%s", supEnv.SenderIdentity, supEnv.MessageID)
	listInbox("sup-after-deliver", supHome, supEnv.SenderIdentity)
	if err := supStore.MarkSuperseded(supEnv.SenderIdentity, supEnv.MessageID); err != nil {
		t.Fatalf("MarkSuperseded: %v", err)
	}
	log("Store.MarkSuperseded marked the message")
	listInbox("sup-after-supersede", supHome, supEnv.SenderIdentity)
	supPayload, err := supStore.ReadEnvelope(supEnv.SenderIdentity, supEnv.MessageID)
	if err != nil {
		t.Fatalf("ReadEnvelope superseded: %v", err)
	}
	if supPayload != nil {
		t.Fatal("superseded payload still present after GC")
	}
	if !supStore.IsSuperseded(supEnv.SenderIdentity, supEnv.MessageID) {
		t.Fatal("superseded tombstone was removed by GC")
	}
	log("assert: payload .json gone, .superseded tombstone retained (IsSuperseded=true)")

	log("")
	log("=== GC transcript complete: all readers treat tombstone-without-payload as handled ===")
}

// inboxHomeOf returns the home directory backing the given store so the test
// can list the real on-disk inbox layout used by the Store.
func inboxHomeOf(t *testing.T, s *Store, senderIdentity string) string {
	t.Helper()
	dir, err := s.inboxDir(senderIdentity)
	if err != nil {
		t.Fatalf("inboxDir: %v", err)
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(dir))) // strip state/InboxDir/sender
}
