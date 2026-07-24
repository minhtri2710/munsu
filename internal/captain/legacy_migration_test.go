package captain

import (
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
