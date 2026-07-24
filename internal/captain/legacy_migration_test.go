package captain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/mailbox"
	"github.com/minhtri2710/munsu/internal/marker"
)

// seedLegacySendOutboxResult captures what seedLegacySendOutbox created.
type seedLegacySendOutboxResult struct {
	Msg       string // the message text
	Filename  string // the ".pending" filename
	RecordID  string // filename without ".pending" suffix
	PayloadID string // deterministic mailbox message ID for this content
	RawData   []byte // raw file content (for exact re-creation in replay tests)
}

// seedLegacySendOutbox writes a legacy .captain-send-outbox entry
// and returns the result with the newly created file's identity.
func seedLegacySendOutbox(parentHome, smID, msg string) (*seedLegacySendOutboxResult, error) {
	outboxDir := sendOutboxCaptainDir(parentHome, smID)
	if err := os.MkdirAll(outboxDir, 0755); err != nil {
		return nil, err
	}

	baseTs := time.Now().UnixNano()
	ts := baseTs
	for {
		name := fmt.Sprintf("%d.pending", ts)
		path := filepath.Join(outboxDir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			created := time.Now().UTC().Format(time.RFC3339Nano)
			content := fmt.Sprintf("id=%s\ncreated=%s\nmessage=%s\n", smID, created, msg)
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return nil, err
			}
			recordID := fmt.Sprintf("%d", ts)
			payloadHash := messagePayloadHash(msg)
			payloadID := legacySendOutboxContentID(smID, recordID, payloadHash)
			return &seedLegacySendOutboxResult{
				Msg:       msg,
				Filename:  name,
				RecordID:  recordID,
				PayloadID: payloadID,
				RawData:   []byte(content),
			}, nil
		}
		ts++
	}
}

// seedLegacyCommandEnvelope writes a legacy .command-envelope entry.
// Returns the envelope and its deterministic mailbox message ID.
func seedLegacyCommandEnvelope(parentHome, captainID, msg string) (*CommandEnvelope, string, error) {
	env := &CommandEnvelope{
		TargetCaptainID: captainID,
		Message:         marker.MarkFromGeneral(msg),
	}
	created, err := CreateEnvelope(parentHome, env)
	if err != nil {
		return nil, "", err
	}
	if !created {
		return nil, "", nil
	}
	payloadHash := messagePayloadHash(env.Message)
	payloadID := legacyCommandEnvelopeContentID(captainID, env.EnvelopeID, payloadHash)
	return env, payloadID, nil
}

func seedLegacySendOutboxT(t *testing.T, parentHome, smID, msg string) *seedLegacySendOutboxResult {
	t.Helper()
	r, err := seedLegacySendOutbox(parentHome, smID, msg)
	if err != nil {
		t.Fatalf("seeding legacy send outbox: %v", err)
	}
	return r
}

func seedLegacyCommandEnvelopeT(t *testing.T, parentHome, captainID, msg string) (*CommandEnvelope, string) {
	t.Helper()
	env, pid, err := seedLegacyCommandEnvelope(parentHome, captainID, msg)
	if err != nil {
		t.Fatalf("seeding legacy envelope: %v", err)
	}
	if env == nil {
		t.Fatal("CreateEnvelope returned noop when expected to create")
	}
	return env, pid
}

// initCaptainMailboxHome writes a valid .munsu-captain-home marker for tests
// so the mailbox.Receiver can derive rank and identity from it.
func initCaptainMailboxHome(captainHome, captainID string) error {
	return mailbox.WriteHomeIdentity(captainHome, captainID, mailbox.RankCaptain)
}

// receiptPathExists checks whether a receipt file exists for a given mailbox message ID.
func receiptPathExists(parentHome, captainID, mailboxMessageID string) bool {
	_, err := os.Stat(migrationReceiptPath(parentHome, captainID, mailboxMessageID))
	return err == nil
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
	legacyResult := seedLegacySendOutboxT(t, parent, captainID, marker.MarkFromGeneral("legacy-outbox-msg"))
	env, envPayloadID := seedLegacyCommandEnvelopeT(t, parent, captainID, "legacy-envelope-msg")

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

	// Verify receipts exist.
	if !receiptPathExists(parent, captainID, legacyResult.PayloadID) {
		t.Error("send-outbox receipt not written")
	}
	if !receiptPathExists(parent, captainID, envPayloadID) {
		t.Error("command-envelope receipt not written")
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

	seedLegacySendOutboxT(t, parent, captainID, marker.MarkFromGeneral("msg-1"))
	seedLegacyCommandEnvelopeT(t, parent, captainID, "msg-2")

	sm := Info{ID: captainID, Home: captainHome}

	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("first drain: %v", err)
	}
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("second drain: %v", err)
	}

	if !isLegacySendOutboxMigrated(parent, captainID) {
		t.Error("send-outbox marker missing after second drain")
	}
	if !isLegacyCommandEnvelopeMigrated(parent, captainID) {
		t.Error("command-envelope marker missing after second drain")
	}

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

	seedLegacySendOutboxT(t, parent, captainID, marker.MarkFromGeneral("valid-msg"))

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

	if isLegacySendOutboxMigrated(parent, captainID) {
		t.Error("send-outbox marker should not be written after crash")
	}
	if isLegacyCommandEnvelopeMigrated(parent, captainID) {
		t.Error("command-envelope marker should not be written after crash")
	}

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

	seedLegacySendOutboxT(t, parent, captainID, marker.MarkFromGeneral("only-once"))

	sm := Info{ID: captainID, Home: captainHome}
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("first drain: %v", err)
	}

	senderIdentity, _, _ := mailbox.ReadHomeIdentity(parent)
	captainStore := mailbox.NewStore(captainHome)
	inbox1, _ := captainStore.ListInbox(senderIdentity)
	count1 := len(inbox1)

	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("second drain: %v", err)
	}

	inbox2, _ := captainStore.ListInbox(senderIdentity)
	count2 := len(inbox2)

	if count2 != count1 {
		t.Errorf("inbox entry count changed after second drain: %d -> %d (want no change)", count1, count2)
	}
}

// TestDrainLegacyCommandTransport_UnknownRecordsPreserved proves that
// non-migratable files are left untouched and not deleted.
func TestDrainLegacyCommandTransport_UnknownRecordsPreserved(t *testing.T) {
	parent := t.TempDir()
	captainHome := t.TempDir()
	captainID := "preserve-cap"
	writeCaptainMeta(t, parent, captainID, captainHome, "w1")
	if err := initCaptainMailboxHome(captainHome, captainID); err != nil {
		t.Fatal(err)
	}

	outboxDir := sendOutboxCaptainDir(parent, captainID)
	if err := os.MkdirAll(outboxDir, 0755); err != nil {
		t.Fatalf("creating outbox dir: %v", err)
	}
	unknownFile := filepath.Join(outboxDir, "README.txt")
	if err := os.WriteFile(unknownFile, []byte("unknown file"), 0644); err != nil {
		t.Fatalf("writing unknown file: %v", err)
	}

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

	data, err := os.ReadFile(receiptFile)
	if err != nil {
		t.Fatalf("terminal receipt was deleted or unreadable: %v", err)
	}
	if string(data) != receiptContent {
		t.Errorf("terminal receipt content changed: got %q, want %q", string(data), receiptContent)
	}
}

// TestDrainLegacyCommandTransport_NoLegacyRecords proves that when no legacy
// records exist, markers are still written (clean state).
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

	if isLegacyCommandEnvelopeMigrated(parent, captainID) {
		t.Error("command-envelope marker should not be written after malformed record")
	}

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

	seedLegacySendOutboxT(t, parent, capA, marker.MarkFromGeneral("for-A"))
	seedLegacySendOutboxT(t, parent, capB, marker.MarkFromGeneral("for-B"))
	seedLegacyCommandEnvelopeT(t, parent, capA, "env-for-A")
	seedLegacyCommandEnvelopeT(t, parent, capB, "env-for-B")

	smA := Info{ID: capA, Home: capAHome}
	if err := DrainLegacyCommandTransport(parent, smA); err != nil {
		t.Fatalf("drain captain A: %v", err)
	}

	if !isLegacySendOutboxMigrated(parent, capA) {
		t.Error("captain A send-outbox marker missing")
	}
	if !isLegacyCommandEnvelopeMigrated(parent, capA) {
		t.Error("captain A command-envelope marker missing")
	}

	if isLegacySendOutboxMigrated(parent, capB) {
		t.Error("captain B send-outbox marker should not exist yet")
	}
	if isLegacyCommandEnvelopeMigrated(parent, capB) {
		t.Error("captain B command-envelope marker should not exist yet")
	}

	paths, _ := listSendOutboxPaths(parent, capB)
	if len(paths) != 1 {
		t.Errorf("captain B should have 1 legacy outbox entry, got %d", len(paths))
	}

	smB := Info{ID: capB, Home: capBHome}
	if err := DrainLegacyCommandTransport(parent, smB); err != nil {
		t.Fatalf("drain captain B: %v", err)
	}

	if !isLegacySendOutboxMigrated(parent, capB) {
		t.Error("captain B send-outbox marker should exist after drain")
	}
}

// --- Blocking finding: per-record atomic receipts ---

// TestDrainLegacyCommandTransport_AtomicReceipt proves each migrated record
// gets an atomic receipt with correct content hash, captain identity, and
// that receipts survive re-run (crash replay safety).
func TestDrainLegacyCommandTransport_AtomicReceipt(t *testing.T) {
	parent := t.TempDir()
	cptHome := t.TempDir()
	cid := "receipt-cap"
	writeCaptainMeta(t, parent, cid, cptHome, "w1")
	if err := initCaptainMailboxHome(cptHome, cid); err != nil {
		t.Fatal(err)
	}

	msg := marker.MarkFromGeneral("receipt-test-msg")
	result := seedLegacySendOutboxT(t, parent, cid, msg)

	sm := Info{ID: cid, Home: cptHome}
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("drain: %v", err)
	}

	// Verify receipt exists and is valid.
	receipt, err := readMigrationReceipt(parent, cid, result.PayloadID)
	if err != nil {
		t.Fatalf("reading receipt: %v", err)
	}
	if receipt == nil {
		t.Fatal("receipt not found after migration")
	}
	if receipt.SchemaVersion != MigrationReceiptSchemaVersion {
		t.Errorf("schema version=%q, want %q", receipt.SchemaVersion, MigrationReceiptSchemaVersion)
	}
	if receipt.LegacyType != LegacySendOutboxType {
		t.Errorf("legacy type=%q, want %q", receipt.LegacyType, LegacySendOutboxType)
	}
	if receipt.LegacyIdentifier != result.RecordID {
		t.Errorf("legacy identifier=%q, want %q", receipt.LegacyIdentifier, result.RecordID)
	}
	if receipt.CaptainIdentity != cid {
		t.Errorf("captain identity=%q, want %q", receipt.CaptainIdentity, cid)
	}
	if receipt.MailboxMessageID != result.PayloadID {
		t.Errorf("mailbox message ID=%q, want %q", receipt.MailboxMessageID, result.PayloadID)
	}
	if receipt.MigratedAt == 0 {
		t.Error("migrated_at is zero")
	}

	// Receipt should survive re-run.
	receipt2, _ := readMigrationReceipt(parent, cid, result.PayloadID)
	if receipt2 == nil || receipt2.LegacyContentHash != receipt.LegacyContentHash {
		t.Error("receipt corrupted or missing after re-run")
	}
}

// --- Blocking finding: deterministic mailbox IDs ---

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
	result := seedLegacySendOutboxT(t, parent, cid, msg)

	if len(result.PayloadID) != 32 {
		t.Errorf("deterministic ID length=%d, want 32", len(result.PayloadID))
	}

	sm := Info{ID: cid, Home: cptHome}
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("first drain: %v", err)
	}

	senderIdentity, _, _ := mailbox.ReadHomeIdentity(parent)
	captainStore := mailbox.NewStore(cptHome)
	env, err := captainStore.ReadEnvelope(senderIdentity, result.PayloadID)
	if err != nil {
		t.Fatalf("reading mailbox envelope: %v", err)
	}
	if env == nil {
		t.Fatalf("mailbox envelope with expected ID %q not found", result.PayloadID)
	}
	if env.Payload != msg {
		t.Errorf("payload=%q, want %q", env.Payload, msg)
	}

	// Same content -> same ID.
	ph := messagePayloadHash(msg)
	if id2 := legacySendOutboxContentID(cid, result.RecordID, ph); id2 != result.PayloadID {
		t.Errorf("second call produces different ID: %q vs %q", id2, result.PayloadID)
	}
}

// --- Blocking finding: malformed JSON fail-closed ---

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

// --- Blocking finding: crash replay ---

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

	r1 := seedLegacySendOutboxT(t, parent, cid, marker.MarkFromGeneral("first-record"))
	r2 := seedLegacySendOutboxT(t, parent, cid, marker.MarkFromGeneral("second-record"))

	senderIdentity, _, _ := mailbox.ReadHomeIdentity(parent)
	captainStore := mailbox.NewStore(cptHome)
	sm := Info{ID: cid, Home: cptHome}

	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("first drain: %v", err)
	}

	if !receiptPathExists(parent, cid, r1.PayloadID) {
		t.Error("receipt for first record missing")
	}
	if !receiptPathExists(parent, cid, r2.PayloadID) {
		t.Error("receipt for second record missing")
	}

	env1, _ := captainStore.ReadEnvelope(senderIdentity, r1.PayloadID)
	if env1 == nil {
		t.Error("first mailbox envelope missing after drain")
	}
	env2, _ := captainStore.ReadEnvelope(senderIdentity, r2.PayloadID)
	if env2 == nil {
		t.Error("second mailbox envelope missing after drain")
	}

	if !isLegacySendOutboxMigrated(parent, cid) {
		t.Error("send-outbox marker should be written")
	}

	inboxBefore, _ := captainStore.ListInbox(senderIdentity)
	countBefore := len(inboxBefore)

	// Simulate crash replay: re-create the same .pending files with their
	// ORIGINAL content (same raw data, same filenames).
	outboxDir := sendOutboxCaptainDir(parent, cid)
	os.MkdirAll(outboxDir, 0755)

	for _, r := range []*seedLegacySendOutboxResult{r1, r2} {
		path := filepath.Join(outboxDir, r.Filename)
		if err := os.WriteFile(path, r.RawData, 0644); err != nil {
			t.Fatalf("re-creating pending file %s: %v", r.Filename, err)
		}
	}

	// Clear the marker so drain re-enters processing.
	removeMigrationMarker(sendOutboxMarkerPath(parent, cid))

	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("replay drain: %v", err)
	}

	// Inbox count should NOT increase — receipts prevent duplicate writes.
	inboxAfter, _ := captainStore.ListInbox(senderIdentity)
	countAfter := len(inboxAfter)
	if countAfter > countBefore {
		t.Errorf("inbox grew after replay: %d -> %d (should stay same via receipts)", countBefore, countAfter)
	}

	if !receiptPathExists(parent, cid, r1.PayloadID) {
		t.Error("receipt for first record disappeared after replay")
	}
	if !receiptPathExists(parent, cid, r2.PayloadID) {
		t.Error("receipt for second record disappeared after replay")
	}
}

// --- Blocking finding: late records after empty scan ---

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
	lateResult := seedLegacySendOutboxT(t, parent, cid, msg)

	senderIdentity, _, _ := mailbox.ReadHomeIdentity(parent)
	captainStore := mailbox.NewStore(cptHome)

	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("late drain: %v", err)
	}

	env, err := captainStore.ReadEnvelope(senderIdentity, lateResult.PayloadID)
	if err != nil {
		t.Fatalf("reading late mailbox envelope: %v", err)
	}
	if env == nil {
		t.Fatal("late record was not migrated into mailbox")
	}
	if env.Payload != msg {
		t.Errorf("late envelope payload=%q, want %q", env.Payload, msg)
	}

	if !receiptPathExists(parent, cid, lateResult.PayloadID) {
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

// --- Blocking finding: eventual delivery ---

// TestDrainLegacyCommandTransport_EventualDelivery proves mailbox pending
// records from migration reconcile through normal converge (ReconcileMailboxPending).
func TestDrainLegacyCommandTransport_EventualDelivery(t *testing.T) {
	parent := t.TempDir()
	cptHome := t.TempDir()
	cid := "eventual-cap"

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
	result := seedLegacySendOutboxT(t, canonParent, cid, msg)

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

	found := false
	for _, p := range pending {
		if p.MessageID == result.PayloadID {
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
		t.Errorf("pending with message ID %q not found", result.PayloadID)
	}

	captainStore := mailbox.NewStore(canonCptHome)
	env, err := captainStore.ReadEnvelope(senderIdentity, result.PayloadID)
	if err != nil {
		t.Fatalf("reading captain inbox envelope: %v", err)
	}
	if env == nil {
		t.Fatal("captain inbox envelope should exist after migration")
	}

	// Simulate captain processing: write ack, then reconcile.
	ack := &mailbox.ProcessingAck{
		MessageID:      result.PayloadID,
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
		if p.MessageID == result.PayloadID {
			t.Fatal("sender pending should be removed after ack reconciliation")
		}
	}
}

// --- Blocking finding: symlinked captain home ---

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
	result := seedLegacySendOutboxT(t, parent, cid, msg)

	sm := Info{ID: cid, Home: symCptHome}
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("drain via symlink: %v", err)
	}

	senderIdentity, _, _ := mailbox.ReadHomeIdentity(parent)
	captainStore := mailbox.NewStore(realCptHome)
	env, err := captainStore.ReadEnvelope(senderIdentity, result.PayloadID)
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

// --- Blocking finding: path traversal safety ---

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

	outboxDir := sendOutboxCaptainDir(parent, cid)
	if err := os.MkdirAll(outboxDir, 0755); err != nil {
		t.Fatalf("creating outbox dir: %v", err)
	}
	validPath := filepath.Join(outboxDir, "1000.pending")
	content := "id=" + cid + "\ncreated=now\nmessage=safe-msg\n"
	if err := os.WriteFile(validPath, []byte(content), 0644); err != nil {
		t.Fatalf("writing outbox entry: %v", err)
	}

	sm := Info{ID: cid, Home: cptHome}
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("drain: %v", err)
	}

	// Verify receipt was written inside the expected subtree.
	ph := messagePayloadHash("safe-msg")
	detID := legacySendOutboxContentID(cid, "1000", ph)
	if !receiptPathExists(parent, cid, detID) {
		t.Error("migration receipt should exist")
	}

	receiptPath := migrationReceiptPath(parent, cid, detID)
	rel, err := filepath.Rel(filepath.Join(parent, "state"), receiptPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("receipt path escaped state dir: %s (rel=%s)", receiptPath, rel)
	}
}

// --- Focused: corrupt receipt ---

// TestDrainLegacyCommandTransport_CorruptReceiptFailClosed proves that a
// corrupt (unparseable JSON) receipt file causes the migration to fail closed
// and preserves the source legacy record.
func TestDrainLegacyCommandTransport_CorruptReceiptFailClosed(t *testing.T) {
	parent := t.TempDir()
	cptHome := t.TempDir()
	cid := "corrupt-receipt-cap"
	writeCaptainMeta(t, parent, cid, cptHome, "w1")
	if err := initCaptainMailboxHome(cptHome, cid); err != nil {
		t.Fatal(err)
	}

	msg := marker.MarkFromGeneral("test-msg")
	r := seedLegacySendOutboxT(t, parent, cid, msg)

	// Write a corrupt receipt file at the expected path.
	receiptDir := migrationReceiptDir(parent, cid)
	if err := os.MkdirAll(receiptDir, 0755); err != nil {
		t.Fatal(err)
	}
	corruptReceiptPath := migrationReceiptPath(parent, cid, r.PayloadID)
	if err := os.WriteFile(corruptReceiptPath, []byte("{not-valid-json}"), 0644); err != nil {
		t.Fatalf("writing corrupt receipt: %v", err)
	}

	sm := Info{ID: cid, Home: cptHome}
	err := DrainLegacyCommandTransport(parent, sm)
	if err == nil {
		t.Fatal("expected error for corrupt receipt, got nil")
	}
	if !strings.Contains(err.Error(), "corrupt receipt") &&
		!strings.Contains(err.Error(), "json parse error") {
		t.Errorf("error should mention corrupt receipt, got: %v", err)
	}

	// Source legacy record should be preserved (not deleted, not migrated).
	paths, _ := listSendOutboxPaths(parent, cid)
	if len(paths) != 1 {
		t.Errorf("source legacy record should be preserved after corrupt receipt, got %d paths", len(paths))
	}

	// Marker should NOT be written.
	if isLegacySendOutboxMigrated(parent, cid) {
		t.Error("send-outbox marker should not be written after corrupt receipt")
	}
}

// --- Focused: conflicting receipt ---

// TestDrainLegacyCommandTransport_ConflictingReceiptFailClosed proves that a
// receipt with conflicting field values (different content hash, different
// type, different captain, etc.) causes fail-closed with source preserved.
func TestDrainLegacyCommandTransport_ConflictingReceiptFailClosed(t *testing.T) {
	parent := t.TempDir()
	cptHome := t.TempDir()
	cid := "conflict-receipt-cap"
	writeCaptainMeta(t, parent, cid, cptHome, "w1")
	if err := initCaptainMailboxHome(cptHome, cid); err != nil {
		t.Fatal(err)
	}

	msg := marker.MarkFromGeneral("test-msg")
	r := seedLegacySendOutboxT(t, parent, cid, msg)

	// Write a valid-format receipt with deliberately conflicting content hash.
	receiptDir := migrationReceiptDir(parent, cid)
	if err := os.MkdirAll(receiptDir, 0755); err != nil {
		t.Fatal(err)
	}
	badReceipt := MigrationReceipt{
		SchemaVersion:     MigrationReceiptSchemaVersion,
		LegacyType:        LegacySendOutboxType,
		LegacyIdentifier:  r.RecordID,
		LegacyContentHash: "0000000000000000000000000000000000000000000000000000000000000000", // wrong hash
		CaptainIdentity:   cid,
		MailboxMessageID:  r.PayloadID,
		MigratedAt:        1000,
	}
	receiptData, _ := json.MarshalIndent(badReceipt, "", "  ")
	conflictPath := migrationReceiptPath(parent, cid, r.PayloadID)
	if err := os.WriteFile(conflictPath, receiptData, 0644); err != nil {
		t.Fatalf("writing conflicting receipt: %v", err)
	}

	sm := Info{ID: cid, Home: cptHome}
	err := DrainLegacyCommandTransport(parent, sm)
	if err == nil {
		t.Fatal("expected error for conflicting receipt, got nil")
	}
	if !strings.Contains(err.Error(), "content hash") &&
		!strings.Contains(err.Error(), "conflicting") {
		t.Errorf("error should mention content hash/conflicting, got: %v", err)
	}

	// Source should be preserved (not deleted).
	paths, _ := listSendOutboxPaths(parent, cid)
	if len(paths) != 1 {
		t.Errorf("source should be preserved after conflicting receipt, got %d paths", len(paths))
	}

	if isLegacySendOutboxMigrated(parent, cid) {
		t.Error("marker should not be written after conflicting receipt")
	}
}

// --- Focused: same legacy ID with changed payload ---

// TestDrainLegacyCommandTransport_SameIdChangedPayload proves that when a
// legacy record with the same ID but different payload is encountered, the
// deterministic ID differs (payload hash in binding) so no conflict occurs.
func TestDrainLegacyCommandTransport_SameIdChangedPayload(t *testing.T) {
	parent := t.TempDir()
	cptHome := t.TempDir()
	cid := "changed-payload-cap"
	writeCaptainMeta(t, parent, cid, cptHome, "w1")
	if err := initCaptainMailboxHome(cptHome, cid); err != nil {
		t.Fatal(err)
	}

	// Seed legacy send outbox entries with the same ID/path pattern but
	// different payloads. Since the deterministic ID binds payload hash,
	// they produce different mailbox IDs.
	msg1 := marker.MarkFromGeneral("original-payload")
	msg2 := marker.MarkFromGeneral("changed-payload")

	r1 := seedLegacySendOutboxT(t, parent, cid, msg1)
	r2 := seedLegacySendOutboxT(t, parent, cid, msg2)

	// The two IDs must differ because payload hash differs.
	if r1.PayloadID == r2.PayloadID {
		t.Fatal("same payload ID for different payloads — binding missing payload hash")
	}

	sm := Info{ID: cid, Home: cptHome}
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("drain: %v", err)
	}

	senderIdentity, _, _ := mailbox.ReadHomeIdentity(parent)
	captainStore := mailbox.NewStore(cptHome)

	// Both should be present as distinct mailbox envelopes.
	env1, _ := captainStore.ReadEnvelope(senderIdentity, r1.PayloadID)
	if env1 == nil {
		t.Error("first mailbox envelope missing")
	}
	env2, _ := captainStore.ReadEnvelope(senderIdentity, r2.PayloadID)
	if env2 == nil {
		t.Error("second mailbox envelope missing")
	}

	if env1 != nil && env2 != nil && env1.MessageID == env2.MessageID {
		t.Error("same MessageID for different payloads")
	}

	// Both receipts should exist.
	if !receiptPathExists(parent, cid, r1.PayloadID) {
		t.Error("receipt for first record missing")
	}
	if !receiptPathExists(parent, cid, r2.PayloadID) {
		t.Error("receipt for second record missing")
	}
}

// --- Focused: distinct legacy IDs with identical payload ---

// TestDrainLegacyCommandTransport_DistinctIdsSamePayload proves that distinct
// legacy IDs with identical payloads produce distinct mailbox envelopes because
// the deterministic ID binds the legacy record identifier.
func TestDrainLegacyCommandTransport_DistinctIdsSamePayload(t *testing.T) {
	parent := t.TempDir()
	cptHome := t.TempDir()
	cid := "distinct-ids-cap"
	writeCaptainMeta(t, parent, cid, cptHome, "w1")
	if err := initCaptainMailboxHome(cptHome, cid); err != nil {
		t.Fatal(err)
	}

	// Same payload but written as two separate legacy records.
	samePayload := marker.MarkFromGeneral("identical-payload")

	r1 := seedLegacySendOutboxT(t, parent, cid, samePayload)
	r2 := seedLegacySendOutboxT(t, parent, cid, samePayload)

	// The two IDs must differ because the legacy record ID (timestamp) differs.
	if r1.PayloadID == r2.PayloadID {
		t.Fatal("same payload ID for distinct legacy records — binding missing legacy record ID")
	}

	sm := Info{ID: cid, Home: cptHome}
	if err := DrainLegacyCommandTransport(parent, sm); err != nil {
		t.Fatalf("drain: %v", err)
	}

	senderIdentity, _, _ := mailbox.ReadHomeIdentity(parent)
	captainStore := mailbox.NewStore(cptHome)

	// Both should be present as distinct mailbox envelopes.
	env1, _ := captainStore.ReadEnvelope(senderIdentity, r1.PayloadID)
	if env1 == nil {
		t.Error("first mailbox envelope missing (distinct ID same payload)")
	}
	env2, _ := captainStore.ReadEnvelope(senderIdentity, r2.PayloadID)
	if env2 == nil {
		t.Error("second mailbox envelope missing (distinct ID same payload)")
	}

	if env1 != nil && env2 != nil && env1.MessageID == env2.MessageID {
		t.Error("same MessageID for distinct legacy records with same payload")
	}

	if !receiptPathExists(parent, cid, r1.PayloadID) {
		t.Error("receipt for first distinct record missing")
	}
	if !receiptPathExists(parent, cid, r2.PayloadID) {
		t.Error("receipt for second distinct record missing")
	}
}

// --- Focused: source preservation ---

// TestDrainLegacyCommandTransport_SourcePreservationOnReceiptWriteConflict proves
// that if writeMigrationReceipt encounters an existing receipt with different
// content, the write is rejected, the source legacy record is preserved, and
// the migration step fails closed.
func TestDrainLegacyCommandTransport_SourcePreservationOnReceiptWriteConflict(t *testing.T) {
	parent := t.TempDir()
	cptHome := t.TempDir()
	cid := "source-preserve-cap"
	writeCaptainMeta(t, parent, cid, cptHome, "w1")
	if err := initCaptainMailboxHome(cptHome, cid); err != nil {
		t.Fatal(err)
	}

	msg := marker.MarkFromGeneral("source-preserve-test")
	r := seedLegacySendOutboxT(t, parent, cid, msg)

	// Pre-write a receipt with different content hash (pretend a different
	// run wrote a conflicting receipt for the same mailbox ID).
	receiptDir := migrationReceiptDir(parent, cid)
	if err := os.MkdirAll(receiptDir, 0755); err != nil {
		t.Fatal(err)
	}
	differentContent := []byte("this is completely different legacy data")
	badHash := contentHashHex(differentContent)
	conflictingReceipt := MigrationReceipt{
		SchemaVersion:     MigrationReceiptSchemaVersion,
		LegacyType:        LegacySendOutboxType,
		LegacyIdentifier:  r.RecordID,
		LegacyContentHash: badHash,
		CaptainIdentity:   cid,
		MailboxMessageID:  r.PayloadID,
		MigratedAt:        500,
	}
	data, _ := json.MarshalIndent(conflictingReceipt, "", "  ")
	if err := os.WriteFile(migrationReceiptPath(parent, cid, r.PayloadID), data, 0644); err != nil {
		t.Fatalf("writing conflicting receipt: %v", err)
	}

	sm := Info{ID: cid, Home: cptHome}
	err := DrainLegacyCommandTransport(parent, sm)
	if err == nil {
		t.Fatal("expected error for receipt write conflict, got nil")
	}
	if !strings.Contains(err.Error(), "receipt") || !strings.Contains(err.Error(), "conflict") {
		t.Errorf("error should mention receipt conflict, got: %v", err)
	}

	// Source legacy record must be preserved.
	paths, _ := listSendOutboxPaths(parent, cid)
	if len(paths) == 0 {
		t.Error("source legacy record was deleted despite receipt write conflict")
	}

	// No mailbox envelope should have been written for this record.
	senderIdentity, _, _ := mailbox.ReadHomeIdentity(parent)
	captainStore := mailbox.NewStore(cptHome)
	env, _ := captainStore.ReadEnvelope(senderIdentity, r.PayloadID)
	if env != nil {
		t.Error("mailbox envelope should not exist when receipt write was rejected")
	}

	// Marker should not be written.
	if isLegacySendOutboxMigrated(parent, cid) {
		t.Error("marker should not be written after receipt write conflict")
	}
}

// --- Focused: deterministic ID binding correctness ---

// TestLegacyContentID_Determinism verifies the content-based ID binds
// transport kind + captain identity + legacy record ID + payload hash.
func TestLegacyContentID_Determinism(t *testing.T) {
	ph := messagePayloadHash("hello")

	id1 := legacySendOutboxContentID("cap", "rec1", ph)
	id2 := legacySendOutboxContentID("cap", "rec1", ph)
	if id1 != id2 {
		t.Errorf("same input -> different IDs: %q vs %q", id1, id2)
	}

	// Different payload hash -> different ID.
	ph2 := messagePayloadHash("HELLO")
	id3 := legacySendOutboxContentID("cap", "rec1", ph2)
	if id1 == id3 {
		t.Error("different payload -> same ID (missing payload hash binding)")
	}

	// Different captain -> different ID.
	id4 := legacySendOutboxContentID("other-cap", "rec1", ph)
	if id1 == id4 {
		t.Error("different captain -> same ID (missing captain binding)")
	}

	// Different legacy record ID -> different ID.
	id5 := legacySendOutboxContentID("cap", "rec2", ph)
	if id1 == id5 {
		t.Error("different legacy record ID -> same ID (missing record ID binding)")
	}

	// Different transport kind -> different ID.
	id6 := legacyCommandEnvelopeContentID("cap", "rec1", ph)
	if id1 == id6 {
		t.Error("different transport kind -> same ID (missing transport kind binding)")
	}
}

// TestLegacyContentID_DeterminismForEnvelope verifies the command-envelope
// ID function also binds all components correctly.
func TestLegacyContentID_DeterminismForEnvelope(t *testing.T) {
	ph := messagePayloadHash("env-msg")

	id1 := legacyCommandEnvelopeContentID("cap", "env1", ph)
	id2 := legacyCommandEnvelopeContentID("cap", "env1", ph)
	if id1 != id2 {
		t.Errorf("same input -> different IDs: %q vs %q", id1, id2)
	}

	// Same payload, different envelope ID -> different mailbox ID.
	ph2 := messagePayloadHash("env-msg") // same payload
	id3 := legacyCommandEnvelopeContentID("cap", "env2", ph2)
	if id1 == id3 {
		t.Error("different envelope ID with same payload -> same mailbox ID (missing envelope ID binding)")
	}

	// Same envelope ID, different payload -> different mailbox ID.
	ph3 := messagePayloadHash("different-env-msg")
	id4 := legacyCommandEnvelopeContentID("cap", "env1", ph3)
	if id1 == id4 {
		t.Error("different payload with same envelope ID -> same mailbox ID (missing payload hash binding)")
	}
}
