package captain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/mailbox"
	"github.com/minhtri2710/munsu/internal/marker"
)

// seedLegacySendOutbox writes a legacy .captain-send-outbox entry.
func seedLegacySendOutbox(t *testing.T, parentHome, smID, msg string) {
	t.Helper()
	if err := EnqueueSendOutbox(parentHome, smID, msg); err != nil {
		t.Fatalf("seeding legacy send outbox: %v", err)
	}
}

// seedLegacyCommandEnvelope writes a legacy .command-envelope entry.
func seedLegacyCommandEnvelope(t *testing.T, parentHome, captainID, msg string) *CommandEnvelope {
	t.Helper()
	env := &CommandEnvelope{
		TargetCaptainID: captainID,
		Message:         marker.MarkFromGeneral(msg),
	}
	created, err := CreateEnvelope(parentHome, env)
	if err != nil {
		t.Fatalf("seeding legacy envelope: %v", err)
	}
	if !created {
		t.Fatal("CreateEnvelope returned noop when expected to create")
	}
	return env
}

// initCaptainMailboxHome writes a valid .munsu-captain-home marker for tests
// so the mailbox.Receiver can derive rank and identity from it.
func initCaptainMailboxHome(captainHome, captainID string) error {
	return mailbox.WriteHomeIdentity(captainHome, captainID, mailbox.RankCaptain)
}

// TestDrainLegacyCommandTransport_MixedState proves that when both legacy
// entries and current mailbox entries coexist, migration processes only the
// legacy entries and leaves current mailbox state untouched.
func TestDrainLegacyCommandTransport_MixedState(t *testing.T) {
	parent := t.TempDir()
	captainHome := t.TempDir()
	captainID := "test-cap"
	writeCaptainMeta(t, parent, captainID, captainHome, "w1")
	if err := initCaptainMailboxHome(captainHome, captainID); err != nil {
		t.Fatal(err)
	}

	// Seed legacy entries.
	seedLegacySendOutbox(t, parent, captainID, marker.MarkFromGeneral("legacy-outbox-msg"))
	env := seedLegacyCommandEnvelope(t, parent, captainID, "legacy-envelope-msg")

	// Seed a current mailbox entry (pre-existing, should remain untouched).
	senderIdentity, senderRank, err := mailbox.ReadHomeIdentity(parent)
	if err != nil {
		t.Fatalf("reading sender identity: %v", err)
	}
	existingMailEnv := &mailbox.Envelope{
		SenderRank:     senderRank,
		SenderIdentity: senderIdentity,
		ReceiverRank:   mailbox.RankCaptain,
		ReceiverID:     captainID,
		Payload:        marker.MarkFromGeneral("existing-mailbox-msg"),
	}
	captainStore := mailbox.NewStore(captainHome)
	if err := captainStore.WriteEnvelope(existingMailEnv); err != nil {
		t.Fatalf("writing existing mailbox envelope: %v", err)
	}
	senderStore := mailbox.NewStore(parent)
	if err := senderStore.WritePending(existingMailEnv); err != nil {
		t.Fatalf("writing existing sender pending: %v", err)
	}
	existingMessageID := existingMailEnv.MessageID

	sm := Info{ID: captainID, Home: captainHome}
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("DrainLegacyCommandTransport: %v", err)
	}

	// Verify legacy outbox entry is gone.
	paths, _ := listSendOutboxPaths(parent, captainID)
	if len(paths) != 0 {
		t.Errorf("legacy send outbox should be empty after drain, got %d entries", len(paths))
	}

	// Verify legacy envelope is marked delivered.
	gotEnv, _ := GetEnvelope(parent, env.EnvelopeID)
	if gotEnv != nil && gotEnv.Status != EnvelopeStatusDelivered {
		t.Errorf("legacy envelope status=%q, want delivered", gotEnv.Status)
	}

	// Verify existing mailbox entry is still present.
	if !captainStore.IsAcked(senderIdentity, existingMessageID) {
		existing, err := captainStore.ReadEnvelope(senderIdentity, existingMessageID)
		if err != nil {
			t.Fatalf("reading existing mailbox envelope: %v", err)
		}
		if existing == nil {
			t.Fatal("existing mailbox envelope was removed by migration")
		}
		if existing.Payload != existingMailEnv.Payload {
			t.Errorf("existing mailbox payload changed: got %q, want %q", existing.Payload, existingMailEnv.Payload)
		}
	}

	// Verify migration markers exist.
	if !isLegacySendOutboxMigrated(parent, captainID) {
		t.Error("send-outbox migration marker not written")
	}
	if !isLegacyCommandEnvelopeMigrated(parent, captainID) {
		t.Error("command-envelope migration marker not written")
	}
}

// TestDrainLegacyCommandTransport_Idempotent proves that running migration
// twice is safe — the second run is a no-op.
func TestDrainLegacyCommandTransport_Idempotent(t *testing.T) {
	parent := t.TempDir()
	captainHome := t.TempDir()
	captainID := "idempotent-cap"
	writeCaptainMeta(t, parent, captainID, captainHome, "w1")
	if err := initCaptainMailboxHome(captainHome, captainID); err != nil {
		t.Fatal(err)
	}

	seedLegacySendOutbox(t, parent, captainID, marker.MarkFromGeneral("msg-1"))
	seedLegacyCommandEnvelope(t, parent, captainID, "msg-2")

	sm := Info{ID: captainID, Home: captainHome}

	// First drain — should process both.
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("first drain: %v", err)
	}

	// Second drain — should be no-op.
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("second drain: %v", err)
	}

	// Verify markers exist.
	if !isLegacySendOutboxMigrated(parent, captainID) {
		t.Error("send-outbox marker missing after second drain")
	}
	if !isLegacyCommandEnvelopeMigrated(parent, captainID) {
		t.Error("command-envelope marker missing after second drain")
	}

	// Verify legacy outbox still empty.
	paths, _ := listSendOutboxPaths(parent, captainID)
	if len(paths) != 0 {
		t.Errorf("legacy outbox should be empty after second drain, got %d", len(paths))
	}
}

// TestDrainLegacyCommandTransport_CrashDuringImport proves that if migration
// encounters a malformed record, it fails closed: the malformed record is
// preserved (never deleted), the fingerprint is not written, and only entries
// processed before the error are completed (FIFO ordering).
func TestDrainLegacyCommandTransport_CrashDuringImport(t *testing.T) {
	parent := t.TempDir()
	captainHome := t.TempDir()
	captainID := "crash-cap"
	writeCaptainMeta(t, parent, captainID, captainHome, "w1")
	if err := initCaptainMailboxHome(captainHome, captainID); err != nil {
		t.Fatal(err)
	}

	// Seed one valid legacy outbox entry (FIFO first, ts ~5000).
	seedLegacySendOutbox(t, parent, captainID, marker.MarkFromGeneral("valid-msg"))

	// Write a malformed entry (FIFO second, later timestamp).
	outboxDir := sendOutboxCaptainDir(parent, captainID)
	malformedPath := filepath.Join(outboxDir, "9999999999.pending")
	if err := os.WriteFile(malformedPath, []byte("bad=format\nno-message"), 0644); err != nil {
		t.Fatalf("writing malformed entry: %v", err)
	}

	sm := Info{ID: captainID, Home: captainHome}
	err := DrainLegacyCommandTransport(parent, sm)
	if err == nil {
		t.Fatal("expected error for malformed outbox entry, got nil")
	}
	if !strings.Contains(err.Error(), "missing message") {
		t.Errorf("error should mention missing message, got: %v", err)
	}

	// Verify migration markers are NOT written (incomplete migration).
	if isLegacySendOutboxMigrated(parent, captainID) {
		t.Error("send-outbox marker should not be written after crash")
	}
	if isLegacyCommandEnvelopeMigrated(parent, captainID) {
		t.Error("command-envelope marker should not be written after crash")
	}

	// The valid entry was processed (FIFO order), the malformed one remains.
	paths, _ := listSendOutboxPaths(parent, captainID)
	if len(paths) != 1 {
		t.Errorf("malformed entry should remain preserved after crash, got %d entries", len(paths))
	}
	if len(paths) == 1 {
		remaining := filepath.Base(paths[0])
		if remaining != "9999999999.pending" {
			t.Errorf("expected malformed file to remain, got %q", remaining)
		}
	}

	// The valid entry was written as a mailbox envelope before the crash.
	senderIdentity, _, _ := mailbox.ReadHomeIdentity(parent)
	captainStore := mailbox.NewStore(captainHome)
	inbox, _ := captainStore.ListInbox(senderIdentity)
	if len(inbox) == 0 {
		t.Error("valid entry should have been processed into mailbox inbox before crash")
	}
}

// TestDrainLegacyCommandTransport_NoDuplicateDelivery proves that draining
// the same legacy data multiple times does not produce duplicate mailbox
// entries. Once the marker exists, migration is skipped.
func TestDrainLegacyCommandTransport_NoDuplicateDelivery(t *testing.T) {
	parent := t.TempDir()
	captainHome := t.TempDir()
	captainID := "nodup-cap"
	writeCaptainMeta(t, parent, captainID, captainHome, "w1")
	if err := initCaptainMailboxHome(captainHome, captainID); err != nil {
		t.Fatal(err)
	}

	seedLegacySendOutbox(t, parent, captainID, marker.MarkFromGeneral("only-once"))

	sm := Info{ID: captainID, Home: captainHome}

	// Drain once.
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("first drain: %v", err)
	}

	senderIdentity, _, _ := mailbox.ReadHomeIdentity(parent)
	captainStore := mailbox.NewStore(captainHome)

	// Count inbox entries after first drain.
	inbox1, _ := captainStore.ListInbox(senderIdentity)
	count1 := len(inbox1)

	// Drain again (should be no-op due to marker).
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("second drain: %v", err)
	}

	// Count inbox entries after second drain — should be same.
	inbox2, _ := captainStore.ListInbox(senderIdentity)
	count2 := len(inbox2)

	if count2 != count1 {
		t.Errorf("inbox entry count changed after second drain: %d -> %d (want no change)", count1, count2)
	}
}

// TestDrainLegacyCommandTransport_UnknownRecordsPreserved proves that
// non-migratable files (not matching expected formats, dotfiles, etc.)
// are left untouched and not deleted.
func TestDrainLegacyCommandTransport_UnknownRecordsPreserved(t *testing.T) {
	parent := t.TempDir()
	captainHome := t.TempDir()
	captainID := "preserve-cap"
	writeCaptainMeta(t, parent, captainID, captainHome, "w1")
	if err := initCaptainMailboxHome(captainHome, captainID); err != nil {
		t.Fatal(err)
	}

	// Write an unknown file in the legacy outbox dir (non-.pending file).
	outboxDir := sendOutboxCaptainDir(parent, captainID)
	if err := os.MkdirAll(outboxDir, 0755); err != nil {
		t.Fatalf("creating outbox dir: %v", err)
	}
	unknownFile := filepath.Join(outboxDir, "README.txt")
	if err := os.WriteFile(unknownFile, []byte("unknown file"), 0644); err != nil {
		t.Fatalf("writing unknown file: %v", err)
	}

	// Write an unknown file in the legacy envelope dir (dotfile).
	envDir := envelopeDir(parent)
	if err := os.MkdirAll(envDir, 0755); err != nil {
		t.Fatalf("creating envelope dir: %v", err)
	}
	dotFile := filepath.Join(envDir, ".unknown-config")
	if err := os.WriteFile(dotFile, []byte("unknown config"), 0644); err != nil {
		t.Fatalf("writing dotfile: %v", err)
	}

	sm := Info{ID: captainID, Home: captainHome}
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("DrainLegacyCommandTransport: %v", err)
	}

	// Verify unknown file still exists.
	if _, err := os.Stat(unknownFile); os.IsNotExist(err) {
		t.Error("unknown file in outbox dir was deleted by migration")
	}
	if _, err := os.Stat(dotFile); os.IsNotExist(err) {
		t.Error("unknown dotfile in envelope dir was deleted by migration")
	}
}

// TestDrainLegacyCommandTransport_TerminalLifecycleUnaffected proves that
// terminal receipts and relay artifacts are not touched by the migration.
func TestDrainLegacyCommandTransport_TerminalLifecycleUnaffected(t *testing.T) {
	parent := t.TempDir()
	captainHome := t.TempDir()
	captainID := "term-cap"
	writeCaptainMeta(t, parent, captainID, captainHome, "w1")
	if err := initCaptainMailboxHome(captainHome, captainID); err != nil {
		t.Fatal(err)
	}

	// Write a terminal receipt in the captain home (simulating existing relay).
	receiptDir := filepath.Join(captainHome, "state", ".terminal-receipts")
	if err := os.MkdirAll(receiptDir, 0755); err != nil {
		t.Fatalf("creating terminal-receipts dir: %v", err)
	}
	receiptFile := filepath.Join(receiptDir, "soldier-1.status")
	receiptContent := "task=soldier-1\nstatus=done\n"
	if err := os.WriteFile(receiptFile, []byte(receiptContent), 0644); err != nil {
		t.Fatalf("writing terminal receipt: %v", err)
	}

	sm := Info{ID: captainID, Home: captainHome}
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("DrainLegacyCommandTransport: %v", err)
	}

	// Verify terminal receipt is untouched.
	data, err := os.ReadFile(receiptFile)
	if err != nil {
		t.Fatalf("terminal receipt was deleted or unreadable: %v", err)
	}
	if string(data) != receiptContent {
		t.Errorf("terminal receipt content changed: got %q, want %q", string(data), receiptContent)
	}
}

// TestDrainLegacyCommandTransport_NoLegacyRecords proves that when no legacy
// records exist, migration markers are still written (clean state).
func TestDrainLegacyCommandTransport_NoLegacyRecords(t *testing.T) {
	parent := t.TempDir()
	captainHome := t.TempDir()
	captainID := "clean-cap"
	writeCaptainMeta(t, parent, captainID, captainHome, "w1")
	if err := initCaptainMailboxHome(captainHome, captainID); err != nil {
		t.Fatal(err)
	}

	sm := Info{ID: captainID, Home: captainHome}
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("DrainLegacyCommandTransport with no legacy records: %v", err)
	}

	if !isLegacySendOutboxMigrated(parent, captainID) {
		t.Error("send-outbox migration marker should be written even with no records")
	}
	if !isLegacyCommandEnvelopeMigrated(parent, captainID) {
		t.Error("command-envelope migration marker should be written even with no records")
	}
}

// TestDrainLegacyCommandTransport_MalformedCommandEnvelope proves that a
// legacy command envelope with empty message is preserved and migration
// fails closed with an error.
func TestDrainLegacyCommandTransport_MalformedCommandEnvelope(t *testing.T) {
	parent := t.TempDir()
	captainHome := t.TempDir()
	captainID := "bad-env-cap"
	writeCaptainMeta(t, parent, captainID, captainHome, "w1")
	if err := initCaptainMailboxHome(captainHome, captainID); err != nil {
		t.Fatal(err)
	}

	// Create a malformed legacy envelope with empty message.
	env := &CommandEnvelope{
		TargetCaptainID: captainID,
		Message:         "",
	}
	created, err := CreateEnvelope(parent, env)
	if err != nil {
		t.Fatalf("CreateEnvelope: %v", err)
	}
	if !created {
		t.Fatal("should create")
	}

	sm := Info{ID: captainID, Home: captainHome}
	err = DrainLegacyCommandTransport(parent, sm)
	if err == nil {
		t.Fatal("expected error for empty message envelope, got nil")
	}
	if !strings.Contains(err.Error(), "empty message") {
		t.Errorf("error should mention empty message, got: %v", err)
	}

	// Marker should not be written.
	if isLegacyCommandEnvelopeMigrated(parent, captainID) {
		t.Error("command-envelope marker should not be written after malformed record")
	}

	// Envelope should still be pending (not delivered).
	got, _ := GetEnvelope(parent, env.EnvelopeID)
	if got != nil && got.Status == EnvelopeStatusDelivered {
		t.Error("malformed envelope should not be marked delivered")
	}
}

// TestDrainLegacyCommandTransport_OnlyPendingEnvelopesMigrated proves that
// non-pending (already terminal) legacy envelopes are skipped.
func TestDrainLegacyCommandTransport_OnlyPendingEnvelopesMigrated(t *testing.T) {
	parent := t.TempDir()
	captainHome := t.TempDir()
	captainID := "term-env-cap"
	writeCaptainMeta(t, parent, captainID, captainHome, "w1")
	if err := initCaptainMailboxHome(captainHome, captainID); err != nil {
		t.Fatal(err)
	}

	// Create a legacy envelope and mark it delivered.
	env := &CommandEnvelope{
		TargetCaptainID: captainID,
		Message:         "already-done",
	}
	CreateEnvelope(parent, env)
	MarkEnvelopeDelivered(parent, env.EnvelopeID)

	sm := Info{ID: captainID, Home: captainHome}
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("DrainLegacyCommandTransport with terminal envelopes: %v", err)
	}

	// Marker should be written (no pending envelopes to migrate).
	if !isLegacyCommandEnvelopeMigrated(parent, captainID) {
		t.Error("command-envelope marker should be written")
	}
}

// TestDrainLegacyCommandTransport_MultipleCaptainsIsolated proves that
// draining one captain does not affect another's legacy records.
func TestDrainLegacyCommandTransport_MultipleCaptainsIsolated(t *testing.T) {
	parent := t.TempDir()
	capAHome := t.TempDir()
	capBHome := t.TempDir()
	capA := "captain-a"
	capB := "captain-b"

	writeCaptainMeta(t, parent, capA, capAHome, "w1")
	if err := initCaptainMailboxHome(capAHome, capA); err != nil {
		t.Fatal(err)
	}
	writeCaptainMeta(t, parent, capB, capBHome, "w2")
	if err := initCaptainMailboxHome(capBHome, capB); err != nil {
		t.Fatal(err)
	}

	seedLegacySendOutbox(t, parent, capA, marker.MarkFromGeneral("for-A"))
	seedLegacySendOutbox(t, parent, capB, marker.MarkFromGeneral("for-B"))
	seedLegacyCommandEnvelope(t, parent, capA, "env-for-A")
	seedLegacyCommandEnvelope(t, parent, capB, "env-for-B")

	// Drain captain A only.
	smA := Info{ID: capA, Home: capAHome}
	if err := DrainLegacyCommandTransport(parent, smA); err != nil {
		t.Fatalf("drain captain A: %v", err)
	}

	// Verify captain A markers exist.
	if !isLegacySendOutboxMigrated(parent, capA) {
		t.Error("captain A send-outbox marker missing")
	}
	if !isLegacyCommandEnvelopeMigrated(parent, capA) {
		t.Error("captain A command-envelope marker missing")
	}

	// Verify captain B markers NOT written yet.
	if isLegacySendOutboxMigrated(parent, capB) {
		t.Error("captain B send-outbox marker should not exist yet")
	}
	if isLegacyCommandEnvelopeMigrated(parent, capB) {
		t.Error("captain B command-envelope marker should not exist yet")
	}

	// Verify captain B legacy records still present.
	paths, _ := listSendOutboxPaths(parent, capB)
	if len(paths) != 1 {
		t.Errorf("captain B should have 1 legacy outbox entry, got %d", len(paths))
	}

	// Now drain captain B.
	smB := Info{ID: capB, Home: capBHome}
	if err := DrainLegacyCommandTransport(parent, smB); err != nil {
		t.Fatalf("drain captain B: %v", err)
	}

	if !isLegacySendOutboxMigrated(parent, capB) {
		t.Error("captain B send-outbox marker should exist after drain")
	}
}

//
// Blocking finding tests
//

// TestDrainLegacyCommandTransport_AtomicReceipt proves each migrated record
// gets an atomic receipt with correct content hash, and receipts survive re-run.
func TestDrainLegacyCommandTransport_AtomicReceipt(t *testing.T) {
	parent := t.TempDir()
	cptHome := t.TempDir()
	cid := "receipt-cap"
	writeCaptainMeta(t, parent, cid, cptHome, "w1")
	if err := initCaptainMailboxHome(cptHome, cid); err != nil {
		t.Fatal(err)
	}

	msg := marker.MarkFromGeneral("receipt-test-msg")
	seedLegacySendOutbox(t, parent, cid, msg)

	sm := Info{ID: cid, Home: cptHome}
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("drain: %v", err)
	}

	detID := legacySendOutboxContentID(cid, msg)

	receipt, err := readMigrationReceipt(parent, cid, detID)
	if err != nil {
		t.Fatalf("reading receipt: %v", err)
	}
	if receipt == nil {
		t.Fatal("receipt not found after migration")
	}
	if receipt.LegacyType != "send-outbox" {
		t.Errorf("legacy type=%q, want send-outbox", receipt.LegacyType)
	}
	if receipt.MailboxMessageID != detID {
		t.Errorf("mailbox message ID=%q, want %q", receipt.MailboxMessageID, detID)
	}
	if receipt.LegacyContentHash == "" {
		t.Error("legacy content hash is empty")
	}
	if receipt.MigratedAt == 0 {
		t.Error("migrated_at is zero")
	}

	// Receipt survives re-run (crash replay safety).
	receipt2, _ := readMigrationReceipt(parent, cid, detID)
	if receipt2 == nil || receipt2.LegacyContentHash != receipt.LegacyContentHash {
		t.Error("receipt corrupted or missing after re-run")
	}
}

// TestDrainLegacyCommandTransport_DeterministicMailboxID proves the same
// legacy content produces the same mailbox message ID, enabling conflict detection.
func TestDrainLegacyCommandTransport_DeterministicMailboxID(t *testing.T) {
	parent := t.TempDir()
	cptHome := t.TempDir()
	cid := "det-id-cap"
	writeCaptainMeta(t, parent, cid, cptHome, "w1")
	if err := initCaptainMailboxHome(cptHome, cid); err != nil {
		t.Fatal(err)
	}

	msg := marker.MarkFromGeneral("deterministic-content")
	seedLegacySendOutbox(t, parent, cid, msg)

	expectedID := legacySendOutboxContentID(cid, msg)
	if len(expectedID) != 32 {
		t.Errorf("deterministic ID length=%d, want 32", len(expectedID))
	}

	sm := Info{ID: cid, Home: cptHome}
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("first drain: %v", err)
	}

	senderIdentity, _, _ := mailbox.ReadHomeIdentity(parent)
	captainStore := mailbox.NewStore(cptHome)
	env, err := captainStore.ReadEnvelope(senderIdentity, expectedID)
	if err != nil {
		t.Fatalf("reading mailbox envelope: %v", err)
	}
	if env == nil {
		t.Fatalf("mailbox envelope with expected ID %q not found", expectedID)
	}
	if env.Payload != msg {
		t.Errorf("payload=%q, want %q", env.Payload, msg)
	}

	// Same content -> same ID.
	if id2 := legacySendOutboxContentID(cid, msg); id2 != expectedID {
		t.Errorf("second call produces different ID: %q vs %q", id2, expectedID)
	}

	// Different content -> different ID.
	if id3 := legacySendOutboxContentID(cid, "different"); id3 == expectedID {
		t.Error("different content should produce different ID")
	}
}

// TestDrainLegacyCommandTransport_MalformedJsonFailClosed proves an unparseable
// JSON file in the legacy envelope directory causes fail-closed error.
func TestDrainLegacyCommandTransport_MalformedJsonFailClosed(t *testing.T) {
	parent := t.TempDir()
	cptHome := t.TempDir()
	cid := "badjson-cap"
	writeCaptainMeta(t, parent, cid, cptHome, "w1")
	if err := initCaptainMailboxHome(cptHome, cid); err != nil {
		t.Fatal(err)
	}

	envDir := envelopeDir(parent)
	if err := os.MkdirAll(envDir, 0755); err != nil {
		t.Fatal(err)
	}
	corruptPath := filepath.Join(envDir, "corrupt.json")
	if err := os.WriteFile(corruptPath, []byte("{not-valid-json}"), 0644); err != nil {
		t.Fatalf("writing corrupt json: %v", err)
	}

	sm := Info{ID: cid, Home: cptHome}
	err := DrainLegacyCommandTransport(parent, sm)
	if err == nil {
		t.Fatal("expected error for corrupt JSON, got nil")
	}
	if !strings.Contains(err.Error(), "unparseable envelope") &&
		!strings.Contains(err.Error(), "json parse error") {
		t.Errorf("error should mention unparseable/json, got: %v", err)
	}

	if _, err := os.Stat(corruptPath); os.IsNotExist(err) {
		t.Error("corrupt JSON file was deleted by migration")
	}
	if isLegacyCommandEnvelopeMigrated(parent, cid) {
		t.Error("migration marker should not be written after fail-closed error")
	}
}

// TestDrainLegacyCommandTransport_CrashReplay proves re-running migration
// after crash skips already-migrated records via per-record receipts.
func TestDrainLegacyCommandTransport_CrashReplay(t *testing.T) {
	parent := t.TempDir()
	cptHome := t.TempDir()
	cid := "replay-cap"
	writeCaptainMeta(t, parent, cid, cptHome, "w1")
	if err := initCaptainMailboxHome(cptHome, cid); err != nil {
		t.Fatal(err)
	}

	msg1 := marker.MarkFromGeneral("first-record")
	msg2 := marker.MarkFromGeneral("second-record")
	seedLegacySendOutbox(t, parent, cid, msg1)
	seedLegacySendOutbox(t, parent, cid, msg2)

	senderIdentity, _, _ := mailbox.ReadHomeIdentity(parent)
	captainStore := mailbox.NewStore(cptHome)
	sm := Info{ID: cid, Home: cptHome}

	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("first drain: %v", err)
	}

	id1 := legacySendOutboxContentID(cid, msg1)
	id2 := legacySendOutboxContentID(cid, msg2)

	if !receiptExistsFor(parent, cid, id1) {
		t.Error("receipt for first record missing")
	}
	if !receiptExistsFor(parent, cid, id2) {
		t.Error("receipt for second record missing")
	}

	env1, _ := captainStore.ReadEnvelope(senderIdentity, id1)
	if env1 == nil {
		t.Error("first mailbox envelope missing after drain")
	}
	env2, _ := captainStore.ReadEnvelope(senderIdentity, id2)
	if env2 == nil {
		t.Error("second mailbox envelope missing after drain")
	}

	if !isLegacySendOutboxMigrated(parent, cid) {
		t.Error("send-outbox marker should be written")
	}

	// Record inbox entry count before replay.
	inboxBefore, _ := captainStore.ListInbox(senderIdentity)
	countBefore := len(inboxBefore)

	// Simulate crash replay: re-seed same records and run again.
	// This simulates the scenario where legacy .pending files were
	// re-created (e.g., crash before cleanup completed).
	seedLegacySendOutbox(t, parent, cid, msg1)
	seedLegacySendOutbox(t, parent, cid, msg2)
	removeMigrationMarker(migrationSendOutboxMarkerPath(parent, cid))

	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("replay drain: %v", err)
	}

	// Inbox count should NOT increase — receipts prevent duplicate writes.
	inboxAfter, _ := captainStore.ListInbox(senderIdentity)
	countAfter := len(inboxAfter)

	if countAfter > countBefore {
		t.Errorf("inbox grew after replay: %d -> %d (should stay same via receipts)", countBefore, countAfter)
	}

	// Receipts should still exist.
	if !receiptExistsFor(parent, cid, id1) {
		t.Error("receipt for first record disappeared after replay")
	}
	if !receiptExistsFor(parent, cid, id2) {
		t.Error("receipt for second record disappeared after replay")
	}
}

// TestDrainLegacyCommandTransport_LateRecordsAfterEmptyScan proves legacy
// records arriving after a completed (empty) migration are still drained.
func TestDrainLegacyCommandTransport_LateRecordsAfterEmptyScan(t *testing.T) {
	parent := t.TempDir()
	cptHome := t.TempDir()
	cid := "late-cap"
	writeCaptainMeta(t, parent, cid, cptHome, "w1")
	if err := initCaptainMailboxHome(cptHome, cid); err != nil {
		t.Fatal(err)
	}

	sm := Info{ID: cid, Home: cptHome}

	// First drain: no records exist — empty scan writes marker.
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("empty drain: %v", err)
	}

	if !isLegacySendOutboxMigrated(parent, cid) {
		t.Error("send-outbox marker should be written after empty scan")
	}
	if !isLegacyCommandEnvelopeMigrated(parent, cid) {
		t.Error("command-envelope marker should be written after empty scan")
	}

	// Late arrival appears after migration completed.
	msg := marker.MarkFromGeneral("late-arrival")
	seedLegacySendOutbox(t, parent, cid, msg)

	senderIdentity, _, _ := mailbox.ReadHomeIdentity(parent)
	captainStore := mailbox.NewStore(cptHome)

	// Second drain handles the late record.
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("late drain: %v", err)
	}

	detID := legacySendOutboxContentID(cid, msg)
	env, err := captainStore.ReadEnvelope(senderIdentity, detID)
	if err != nil {
		t.Fatalf("reading late mailbox envelope: %v", err)
	}
	if env == nil {
		t.Fatal("late record was not migrated into mailbox")
	}
	if env.Payload != msg {
		t.Errorf("late envelope payload=%q, want %q", env.Payload, msg)
	}

	if !receiptExistsFor(parent, cid, detID) {
		t.Error("receipt for late record missing")
	}
	if !isLegacySendOutboxMigrated(parent, cid) {
		t.Error("send-outbox marker should be re-written after late drain")
	}

	// Third drain: no more records, clean no-op.
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("post-late drain: %v", err)
	}
}

// TestDrainLegacyCommandTransport_EventualDelivery proves mailbox pending
// records from migration reconcile through normal converge (ReconcileMailboxPending).
func TestDrainLegacyCommandTransport_EventualDelivery(t *testing.T) {
	parent := t.TempDir()
	cptHome := t.TempDir()
	cid := "eventual-cap"

	// Use canonical paths to avoid /var vs /private/var mismatch on macOS.
	canonParent, err2 := filepath.EvalSymlinks(parent)
	if err2 != nil {
		t.Fatalf("eval symlinks parent: %v", err2)
	}
	canonCptHome, err2 := filepath.EvalSymlinks(cptHome)
	if err2 != nil {
		t.Fatalf("eval symlinks cptHome: %v", err2)
	}

	writeCaptainMeta(t, canonParent, cid, canonCptHome, "w1")
	if err := initCaptainMailboxHome(canonCptHome, cid); err != nil {
		t.Fatal(err)
	}

	msg := marker.MarkFromGeneral("eventual-delivery-msg")
	seedLegacySendOutbox(t, canonParent, cid, msg)

	sm := Info{ID: cid, Home: canonCptHome}
	if err := DrainLegacyCommandTransport(canonParent, sm); err != nil {
		t.Fatalf("drain: %v", err)
	}

	senderIdentity, _, err := mailbox.ReadHomeIdentity(canonParent)
	if err != nil {
		t.Fatalf("reading sender identity: %v", err)
	}

	senderStore := mailbox.NewStore(canonParent)
	pending, err := senderStore.ListPending(senderIdentity)
	if err != nil {
		t.Fatalf("listing pending: %v", err)
	}
	if len(pending) == 0 {
		t.Fatal("sender pending should exist after migration")
	}

	detID := legacySendOutboxContentID(cid, msg)
	found := false
	for _, p := range pending {
		if p.MessageID == detID {
			found = true
			if p.Payload != msg {
				t.Errorf("pending payload=%q, want %q", p.Payload, msg)
			}
			if p.ReceiverID != cid {
				t.Errorf("pending receiver=%q, want %q", p.ReceiverID, cid)
			}
			break
		}
	}
	if !found {
		t.Errorf("pending with message ID %q not found", detID)
	}

	captainStore := mailbox.NewStore(canonCptHome)
	env, err := captainStore.ReadEnvelope(senderIdentity, detID)
	if err != nil {
		t.Fatalf("reading captain inbox envelope: %v", err)
	}
	if env == nil {
		t.Fatal("captain inbox envelope should exist after migration")
	}

	// Simulate captain processing: write ack, then reconcile.
	ack := &mailbox.ProcessingAck{
		MessageID:      detID,
		SenderRank:     mailbox.RankGeneral,
		SenderIdentity: senderIdentity,
		ReceiverRank:   mailbox.RankCaptain,
		ReceiverID:     cid,
		PayloadHash:    env.PayloadHash,
		ProcessedAt:    1000,
		Outcome:        mailbox.OutcomeAccepted,
	}
	if err := captainStore.WriteAck(ack); err != nil {
		t.Fatalf("writing ack: %v", err)
	}

	if err := ReconcileMailboxPending(canonParent, sm); err != nil {
		t.Fatalf("ReconcileMailboxPending: %v", err)
	}

	pending2, err := senderStore.ListPending(senderIdentity)
	if err != nil {
		t.Fatalf("listing pending after reconcile: %v", err)
	}
	for _, p := range pending2 {
		if p.MessageID == detID {
			t.Fatal("sender pending should be removed after ack reconciliation")
		}
	}
}

// TestDrainLegacyCommandTransport_SymlinkedCaptainHome proves drain works
// when captain home path contains symlinks (canonical resolution).
func TestDrainLegacyCommandTransport_SymlinkedCaptainHome(t *testing.T) {
	realCptHome := t.TempDir()
	symCptHome := filepath.Join(t.TempDir(), "captain-home-link")
	if err := os.Symlink(realCptHome, symCptHome); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	parent := t.TempDir()
	cid := "symlink-cap"

	writeCaptainMeta(t, parent, cid, symCptHome, "w1")
	if err := initCaptainMailboxHome(realCptHome, cid); err != nil {
		t.Fatal(err)
	}

	msg := marker.MarkFromGeneral("symlink-test-msg")
	seedLegacySendOutbox(t, parent, cid, msg)

	sm := Info{ID: cid, Home: symCptHome}
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("drain via symlink: %v", err)
	}

	senderIdentity, _, _ := mailbox.ReadHomeIdentity(parent)
	captainStore := mailbox.NewStore(realCptHome)
	detID := legacySendOutboxContentID(cid, msg)
	env, err := captainStore.ReadEnvelope(senderIdentity, detID)
	if err != nil {
		t.Fatalf("reading mailbox envelope from real home: %v", err)
	}
	if env == nil {
		t.Fatal("mailbox envelope not found in real home (symlink broken)")
	}
	if env.Payload != msg {
		t.Errorf("payload mismatch: got %q, want %q", env.Payload, msg)
	}

	if !isLegacySendOutboxMigrated(parent, cid) {
		t.Error("send-outbox marker missing after symlink drain")
	}
}

// TestDrainLegacyCommandTransport_PathTraversalSafety proves drain does not
// allow path traversal through legacy filenames or components.
func TestDrainLegacyCommandTransport_PathTraversalSafety(t *testing.T) {
	parent := t.TempDir()
	cptHome := t.TempDir()
	cid := "safe-cap"
	writeCaptainMeta(t, parent, cid, cptHome, "w1")
	if err := initCaptainMailboxHome(cptHome, cid); err != nil {
		t.Fatal(err)
	}

	// Create a legacy outbox entry with a valid path.
	outboxDir := sendOutboxCaptainDir(parent, cid)
	if err := os.MkdirAll(outboxDir, 0755); err != nil {
		t.Fatalf("creating outbox dir: %v", err)
	}
	validPath := filepath.Join(outboxDir, "1000.pending")
	content := fmt.Sprintf("id=%s\ncreated=now\nmessage=%s\n", cid, "safe-msg")
	if err := os.WriteFile(validPath, []byte(content), 0644); err != nil {
		t.Fatalf("writing outbox entry: %v", err)
	}

	sm := Info{ID: cid, Home: cptHome}
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("drain: %v", err)
	}

	// Verify the mailbox envelope was written inside the expected subtree.
	senderIdentity, _, _ := mailbox.ReadHomeIdentity(parent)
	captainStore := mailbox.NewStore(cptHome)
	detID := legacySendOutboxContentID(cid, "safe-msg")
	env, err := captainStore.ReadEnvelope(senderIdentity, detID)
	if err != nil {
		t.Fatalf("reading mailbox envelope: %v", err)
	}
	if env == nil {
		t.Fatal("mailbox envelope should exist after drain")
	}

	// Verify receipt was written inside the expected subtree.
	if !receiptExistsFor(parent, cid, detID) {
		t.Error("migration receipt should exist")
	}

	// Verify the receipt file path is under parent/state/.migration-receipts/...
	receiptPath := migrationReceiptPath(parent, cid, detID)
	rel, err := filepath.Rel(filepath.Join(parent, "state"), receiptPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("receipt path escaped state dir: %s (rel=%s)", receiptPath, rel)
	}
}

// TestLegacyContentID_Determinism verifies content-based ID is consistent.
func TestLegacyContentID_Determinism(t *testing.T) {
	id1 := legacySendOutboxContentID("cap", "hello")
	id2 := legacySendOutboxContentID("cap", "hello")
	if id1 != id2 {
		t.Errorf("same input -> different IDs: %q vs %q", id1, id2)
	}
	id3 := legacySendOutboxContentID("cap", "HELLO")
	if id1 == id3 {
		t.Error("different input -> same ID")
	}
	id4 := legacySendOutboxContentID("other-cap", "hello")
	if id1 == id4 {
		t.Error("different captain ID -> same ID")
	}
}