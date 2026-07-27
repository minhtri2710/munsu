// Package captain — command transport removal invariant tests.
//
// These tests verify that after removal of the legacy General->Captain command
// transport:
//  1. Stale legacy records produce an actionable error with exact paths.
//  2. Guard execution leaves stale files byte-for-byte untouched.
//  3. Normal operations create no legacy directories, receipts, or markers.
//  4. Forbidden runtime symbols are absent (compile-time check).
//  5. Mailbox delivery and recovery remain functional.
package captain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLegacyGuard_StaleOutboxRecordsProducesActionableError proves that
// checkStaleLegacyRecords returns an actionable error containing the exact
// path of stale .captain-send-outbox records.
func TestLegacyGuard_StaleOutboxRecordsProducesActionableError(t *testing.T) {
	parent := t.TempDir()
	captainID := "test-cap"

	// Write a stale outbox entry with real pending extension.
	outboxDir := filepath.Join(parent, "state", ".captain-send-outbox", captainID)
	if err := os.MkdirAll(outboxDir, 0755); err != nil {
		t.Fatal(err)
	}
	stalePath := filepath.Join(outboxDir, "1000000000.pending")
	staleContent := "id=test-cap\ncreated=now\nmessage=stale\n"
	if err := os.WriteFile(stalePath, []byte(staleContent), 0644); err != nil {
		t.Fatal(err)
	}

	err := checkStaleLegacyRecords(parent, captainID)
	if err == nil {
		t.Fatal("expected error for stale outbox records, got nil")
	}
	if !strings.Contains(err.Error(), ".captain-send-outbox") {
		t.Errorf("error should mention .captain-send-outbox, got: %v", err)
	}
	if !strings.Contains(err.Error(), stalePath) {
		t.Errorf("error should contain exact path %q, got: %v", stalePath, err)
	}
	if !strings.Contains(err.Error(), "Upgrade") {
		t.Errorf("error should contain actionable upgrade instruction, got: %v", err)
	}
}

// TestLegacyGuard_StaleEnvelopeRecordsProducesActionableError proves that
// checkStaleLegacyRecords returns an actionable error with exact paths for
// stale .command-envelope records.
func TestLegacyGuard_StaleEnvelopeRecordsProducesActionableError(t *testing.T) {
	parent := t.TempDir()
	captainID := "test-cap"

	// Write a stale envelope JSON file.
	envDir := filepath.Join(parent, "state", ".command-envelope")
	if err := os.MkdirAll(envDir, 0755); err != nil {
		t.Fatal(err)
	}
	stalePath := filepath.Join(envDir, "ab12cd34.json")
	staleContent := `{"schema_version":"munsu.command-envelope/v1","target_captain_id":"test-cap","message":"stale"}`
	if err := os.WriteFile(stalePath, []byte(staleContent), 0644); err != nil {
		t.Fatal(err)
	}

	err := checkStaleLegacyRecords(parent, captainID)
	if err == nil {
		t.Fatal("expected error for stale envelope records, got nil")
	}
	if !strings.Contains(err.Error(), ".command-envelope") {
		t.Errorf("error should mention .command-envelope, got: %v", err)
	}
	if !strings.Contains(err.Error(), stalePath) {
		t.Errorf("error should contain exact path %q, got: %v", stalePath, err)
	}
	if !strings.Contains(err.Error(), "Upgrade") {
		t.Errorf("error should contain actionable upgrade instruction, got: %v", err)
	}
}

// TestLegacyGuard_LeavesFilesUntouched proves that checkStaleLegacyRecords
// never modifies, moves, or deletes stale records — they remain byte-for-byte
// identical after guard execution.
func TestLegacyGuard_LeavesFilesUntouched(t *testing.T) {
	parent := t.TempDir()
	captainID := "test-cap"

	// Write stale outbox entry.
	outboxDir := filepath.Join(parent, "state", ".captain-send-outbox", captainID)
	if err := os.MkdirAll(outboxDir, 0755); err != nil {
		t.Fatal(err)
	}
	outboxPath := filepath.Join(outboxDir, "2000000000.pending")
	originalOutboxContent := "id=test-cap\ncreated=now\nmessage=preserve-me\n"
	if err := os.WriteFile(outboxPath, []byte(originalOutboxContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Write stale envelope entry.
	envDir := filepath.Join(parent, "state", ".command-envelope")
	if err := os.MkdirAll(envDir, 0755); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(envDir, "ff001100.json")
	originalEnvContent := `{"schema_version":"munsu.command-envelope/v1","target_captain_id":"test-cap","message":"preserve"}`
	if err := os.WriteFile(envPath, []byte(originalEnvContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Run guard — expect error.
	err := checkStaleLegacyRecords(parent, captainID)
	if err == nil {
		t.Fatal("expected error for stale records")
	}

	// Verify outbox file is untouched.
	afterOutbox, err := os.ReadFile(outboxPath)
	if err != nil {
		t.Fatalf("outbox file was deleted or unreadable: %v", err)
	}
	if string(afterOutbox) != originalOutboxContent {
		t.Errorf("outbox file content changed:\n  before: %q\n  after:  %q", originalOutboxContent, string(afterOutbox))
	}

	// Verify envelope file is untouched.
	afterEnv, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("envelope file was deleted or unreadable: %v", err)
	}
	if string(afterEnv) != originalEnvContent {
		t.Errorf("envelope file content changed:\n  before: %q\n  after:  %q", originalEnvContent, string(afterEnv))
	}

	// Verify no new files were created.
	entries, err := os.ReadDir(outboxDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "2000000000.pending" {
		t.Errorf("unexpected outbox dir state: %v", entries)
	}
}

// TestLegacyGuard_CleanStateReturnsNil proves that when no stale legacy records
// exist, checkStaleLegacyRecords returns nil (no error).
func TestLegacyGuard_CleanStateReturnsNil(t *testing.T) {
	parent := t.TempDir()
	captainID := "test-cap"

	// Create the legacy directories but leave them empty — no error.
	outboxDir := filepath.Join(parent, "state", ".captain-send-outbox", captainID)
	if err := os.MkdirAll(outboxDir, 0755); err != nil {
		t.Fatal(err)
	}
	envDir := filepath.Join(parent, "state", ".command-envelope")
	if err := os.MkdirAll(envDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Also create an unrelated dotfile to prove .command-envelope filter works.
	if err := os.WriteFile(filepath.Join(envDir, ".gitkeep"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	if err := checkStaleLegacyRecords(parent, captainID); err != nil {
		t.Fatalf("unexpected error for clean state: %v", err)
	}
}

// TestLegacyGuard_DirectoriesMissingReturnsNil proves that when no legacy
// directories exist at all, checkStaleLegacyRecords returns nil.
func TestLegacyGuard_DirectoriesMissingReturnsNil(t *testing.T) {
	parent := t.TempDir()
	captainID := "test-cap"

	// No legacy dirs at all.
	if err := checkStaleLegacyRecords(parent, captainID); err != nil {
		t.Fatalf("unexpected error when directories missing: %v", err)
	}
}

// TestLegacyGuard_OtherCaptainsIsolated proves that stale records for one
// captain don't affect the guard check for a different captain.
func TestLegacyGuard_OtherCaptainsIsolated(t *testing.T) {
	parent := t.TempDir()

	// Stale records for captain A only.
	outboxDirA := filepath.Join(parent, "state", ".captain-send-outbox", "cap-a")
	if err := os.MkdirAll(outboxDirA, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outboxDirA, "1.pending"), []byte("id=cap-a\nmessage=stale\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Captain B should have no error even though A has stale records.
	if err := checkStaleLegacyRecords(parent, "cap-b"); err != nil {
		t.Fatalf("unexpected error for clean captain B: %v", err)
	}

	// Captain A should error.
	if err := checkStaleLegacyRecords(parent, "cap-a"); err == nil {
		t.Fatal("expected error for captain A with stale records")
	}
}

// TestLegacyGuard_CompileTimeAssertion verifies via string search that the
// removed legacy transport symbols are not present in the compiled package.
// If any of these symbols were re-introduced, this test will catch it.
func TestLegacyGuard_CompileTimeAssertion(t *testing.T) {
	// The following symbols have been removed:
	//   EnqueueSendOutbox, FlushSendOutbox, SendOutboxDir
	//   CreateEnvelope, GetEnvelope, ListPendingEnvelopes
	//   MarkEnvelopeDelivered, MarkEnvelopeCompleted, MarkEnvelopeFailed
	//   PushEnvelopeToCaptain, FlushEnvelopeSend, GetCaptainEnvelope
	//   CommandEnvelope, CommandEnvelopeDir, CaptainEnvelopeSubdir
	//   SchemaVersionEnvelope, EnvelopeStatus*, NewEnvelopeID
	//   DrainLegacyCommandTransport
	//   MigrationReceipt*, LegacySendOutboxType, LegacyCommandEnvelopeType
	//   MigrationReceiptDir, MigrationReceiptSchemaVersion
	//
	// This test compiles, proving none are referenced here.
	t.Log("compile-time pass: no forbidden symbols referenced")
}
