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
// It proves every reader treats an ack-log entry as a first-class handled
// state, whether or not the payload is still on disk:
//   - Receiver.Ack appends the ack to the receiver's ack log and leaves the
//     payload; the sender's Store.RemoveAckedPayload removes it later.
//   - Receiver.Ack replays idempotently even when the payload is already gone.
//   - Store.MarkSuperseded GCs the payload after the .superseded tombstone.
//   - Receiver.Receive refuses an acked message.
//   - Store.ListInbox skips an acked record.
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

	// ---- Scenario 1: Receiver.Ack keeps the payload; the sender removes it ----
	log("--- Scenario 1: Receiver.Ack logs the ack, the sender removes the payload ---")
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
	if payload, err := store.ReadEnvelope(env.SenderIdentity, env.MessageID); err != nil || payload == nil {
		t.Fatalf("ReadEnvelope after Ack = %v, %v; want the payload kept for the sender", payload, err)
	}
	if listed, err := store.ListInbox(env.SenderIdentity); err != nil || len(listed) != 0 {
		t.Fatalf("ListInbox after Ack = %d records, %v; want the acked record skipped", len(listed), err)
	}
	store.RemoveAckedPayload(&env)
	log("Store.RemoveAckedPayload (the sender's reconcile) ran")
	listInbox("after-reconcile", homeDir, env.SenderIdentity)
	payload, err := store.ReadEnvelope(env.SenderIdentity, env.MessageID)
	if err != nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	if payload != nil {
		t.Fatal("payload still present after the sender's reconcile")
	}
	if !store.IsAcked(&env) {
		t.Fatal("ack log entry was removed by GC")
	}
	log("assert: payload .json gone, ack log entry retained (IsAcked=true)")

	// ---- Scenario 2: Receiver.Receive refuses an acked message ----
	log("")
	log("--- Scenario 2: Receiver.Receive sees a handled message ---")
	if _, err := r.Receive(ref); err == nil || !strings.Contains(err.Error(), "receive envelope already acked") {
		t.Fatalf("Receive error = %v, want already acked", err)
	}
	log("Receiver.Receive returned deterministic error: %q", "receive envelope already acked")

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
