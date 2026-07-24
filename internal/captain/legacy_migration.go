package captain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/mailbox"
	"github.com/minhtri2710/munsu/internal/marker"
)

// Constants for legacy transport migration.
const (
	// MigrationReceiptDir is the subdirectory under state/ for per-record migration receipts.
	MigrationReceiptDir = ".migration-receipts"

	// MigrationReceiptSchemaVersion is the schema identifier for receipt files.
	MigrationReceiptSchemaVersion = "munsu.legacy-migration-receipt/v1"

	// LegacySendOutboxType is the legacy transport type for send-outbox records.
	LegacySendOutboxType = "send-outbox"

	// LegacyCommandEnvelopeType is the legacy transport type for command-envelope records.
	LegacyCommandEnvelopeType = "command-envelope"
)

// MigrationReceipt records the one-to-one mapping from one legacy transport
// record to the mailbox envelope that replaced it. Written atomically after
// both the inbox envelope and sender pending succeed. Every field is validated
// on replay; corrupt or conflicting receipts fail closed and preserve the source.
type MigrationReceipt struct {
	SchemaVersion     string `json:"schema_version"`
	LegacyType        string `json:"legacy_type"`          // "send-outbox" or "command-envelope"
	LegacyIdentifier  string `json:"legacy_identifier"`    // filename stem or envelope ID
	LegacyContentHash string `json:"legacy_content_hash"`  // SHA-256 of raw legacy file content
	CaptainIdentity   string `json:"captain_identity"`     // captain ID this receipt belongs to
	MailboxMessageID  string `json:"mailbox_message_id"`   // deterministic mailbox envelope ID
	MigratedAt        int64  `json:"migrated_at"`
}

// --- path helpers ---

func migrationReceiptDir(parentHome, captainID string) string {
	return filepath.Join(parentHome, "state", MigrationReceiptDir, captainID)
}

func migrationReceiptPath(parentHome, captainID, mailboxMessageID string) string {
	// Use mailbox message ID as the filename stem. The captainID is already
	// in the directory path so the filename only needs the message ID.
	return filepath.Join(migrationReceiptDir(parentHome, captainID), mailboxMessageID+".receipt")
}

// --- deterministic envelope IDs for migration ---
//
// Binding: transport kind + Captain identity + legacy record ID + payload hash.
// This means:
//   - Same legacy ID with changed payload → different payload hash → different ID → distinct envelope
//   - Different legacy IDs with identical payload → different legacy record ID → different ID → distinct envelopes
//   - Same content from different captains → different IDs (captain identity in binding)
//   - Same content from different transport kinds → different IDs (kind prefix)

// legacySendOutboxContentID returns a deterministic 32-char hex mailbox message ID
// for a legacy send-outbox record.
func legacySendOutboxContentID(captainID, legacyRecordID, payloadHash string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("send-outbox:%s:%s:%s", captainID, legacyRecordID, payloadHash)))
	return hex.EncodeToString(h[:16])
}

// legacyCommandEnvelopeContentID returns a deterministic 32-char hex mailbox message ID
// for a legacy CommandEnvelope record.
func legacyCommandEnvelopeContentID(captainID, envelopeID, payloadHash string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("command-envelope:%s:%s:%s", captainID, envelopeID, payloadHash)))
	return hex.EncodeToString(h[:16])
}

// contentHashHex returns the hex SHA-256 hash of raw content bytes.
func contentHashHex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// messagePayloadHash returns the hex SHA-256 hash of the message string.
func messagePayloadHash(msg string) string {
	h := sha256.Sum256([]byte(msg))
	return hex.EncodeToString(h[:])
}

// --- per-record receipt I/O ---

// validateMigrationReceipt parses and validates a receipt file for the expected
// legacy record. Returns an error if the receipt is corrupt, schema-invalid, or
// has conflicting field values (fail-closed). When an error is returned, the
// caller MUST preserve the source legacy record and fail the migration step.
//
// Validation checks:
//  1. File exists and is valid JSON
//  2. SchemaVersion matches the current schema
//  3. LegacyType matches the expected type
//  4. LegacyIdentifier matches the expected identifier
//  5. LegacyContentHash matches the legacy data
//  6. CaptainIdentity matches the captain ID
//  7. MailboxMessageID matches the deterministic ID
func validateMigrationReceipt(parentHome, captainID, mailboxMessageID string,
	expectedLegacyType, expectedLegacyID string, expectedLegacyData []byte) error {

	data, err := os.ReadFile(migrationReceiptPath(parentHome, captainID, mailboxMessageID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil // receipt does not exist — not a validation error
		}
		return fmt.Errorf("reading receipt: %w", err)
	}

	var r MigrationReceipt
	if err := json.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("corrupt receipt %s: json parse error: %w — fail-closed, preserved", mailboxMessageID, err)
	}

	if r.SchemaVersion != MigrationReceiptSchemaVersion {
		return fmt.Errorf("receipt %s: schema version %q, expected %q — fail-closed, preserved",
			mailboxMessageID, r.SchemaVersion, MigrationReceiptSchemaVersion)
	}

	if r.LegacyType != expectedLegacyType {
		return fmt.Errorf("receipt %s: legacy type %q, expected %q — fail-closed, preserved",
			mailboxMessageID, r.LegacyType, expectedLegacyType)
	}

	if r.LegacyIdentifier != expectedLegacyID {
		return fmt.Errorf("receipt %s: legacy identifier %q, expected %q — fail-closed, preserved",
			mailboxMessageID, r.LegacyIdentifier, expectedLegacyID)
	}

	expectedHash := contentHashHex(expectedLegacyData)
	if r.LegacyContentHash != expectedHash {
		return fmt.Errorf("receipt %s: content hash %q, expected %q — conflicting content, fail-closed, preserved",
			mailboxMessageID, r.LegacyContentHash, expectedHash)
	}

	if r.CaptainIdentity != captainID {
		return fmt.Errorf("receipt %s: captain identity %q, expected %q — fail-closed, preserved",
			mailboxMessageID, r.CaptainIdentity, captainID)
	}

	if r.MailboxMessageID != mailboxMessageID {
		return fmt.Errorf("receipt %s: mailbox message ID %q, expected %q — fail-closed, preserved",
			mailboxMessageID, r.MailboxMessageID, mailboxMessageID)
	}

	return nil
}

// writeMigrationReceipt writes an atomic per-record receipt after a legacy
// record has been successfully converted and written as a mailbox envelope.
//
// If a receipt already exists at the target path and its content differs
// from what would be written, the write is rejected (fail-closed) to prevent
// silently overwriting conflicting migration state.
func writeMigrationReceipt(parentHome, captainID, legacyType, legacyIdentifier string,
	legacyData []byte, captainIdentity, mailboxMessageID string) error {

	contentHash := contentHashHex(legacyData)
	receipt := MigrationReceipt{
		SchemaVersion:     MigrationReceiptSchemaVersion,
		LegacyType:        legacyType,
		LegacyIdentifier:  legacyIdentifier,
		LegacyContentHash: contentHash,
		CaptainIdentity:   captainIdentity,
		MailboxMessageID:  mailboxMessageID,
		MigratedAt:        time.Now().UnixNano(),
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling receipt: %w", err)
	}

	path := migrationReceiptPath(parentHome, captainID, mailboxMessageID)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating receipt dir: %w", err)
	}

	// Atomic write with conflict detection: if existing file has different content, reject.
	if existing, readErr := os.ReadFile(path); readErr == nil {
		var existingR MigrationReceipt
		if unmarshalErr := json.Unmarshal(existing, &existingR); unmarshalErr == nil {
			// Compare all identity fields — same receipt is idempotent.
			if existingR.SchemaVersion == receipt.SchemaVersion &&
				existingR.LegacyType == receipt.LegacyType &&
				existingR.LegacyIdentifier == receipt.LegacyIdentifier &&
				existingR.LegacyContentHash == receipt.LegacyContentHash &&
				existingR.CaptainIdentity == receipt.CaptainIdentity &&
				existingR.MailboxMessageID == receipt.MailboxMessageID {
				return nil // idempotent: exact same receipt, OK
			}
			return fmt.Errorf("receipt write conflict: %s differs from existing receipt — fail-closed", mailboxMessageID)
		}
		// Existing file is corrupt; overwrite is allowed.
	}

	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return fmt.Errorf("creating temp receipt: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writing temp receipt: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing temp receipt: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("renaming receipt: %w", err)
	}
	return nil
}

// readMigrationReceipt reads a receipt for the given mailbox message ID.
// Returns nil, nil if not found.
func readMigrationReceipt(parentHome, captainID, mailboxMessageID string) (*MigrationReceipt, error) {
	data, err := os.ReadFile(migrationReceiptPath(parentHome, captainID, mailboxMessageID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var r MigrationReceipt
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// -------- migration markers --------
//
// Global done markers are never the sole authority for suppressing late records.
// The drain functions always scan for legacy records regardless of marker state.
// Markers are used only as an optimization hint: if a marker exists and no records
// are found, skip. If records are found despite a marker, the marker is cleared,
// records are processed, and the marker is re-written on completion.

// sendOutboxMarkerPath returns the path for the send-outbox completion marker.
func sendOutboxMarkerPath(parentHome, captainID string) string {
	return filepath.Join(parentHome, "state", ".migrated-send-outbox-"+captainID)
}

// commandEnvelopeMarkerPath returns the path for the command-envelope completion marker.
func commandEnvelopeMarkerPath(parentHome, captainID string) string {
	return filepath.Join(parentHome, "state", ".migrated-command-envelope-"+captainID)
}

func markerExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// -------- marker helpers (used by tests) --------

func isLegacySendOutboxMigrated(parentHome, captainID string) bool {
	return markerExists(sendOutboxMarkerPath(parentHome, captainID))
}

func isLegacyCommandEnvelopeMigrated(parentHome, captainID string) bool {
	return markerExists(commandEnvelopeMarkerPath(parentHome, captainID))
}

func migrationSendOutboxMarkerPath(parentHome, captainID string) string {
	return sendOutboxMarkerPath(parentHome, captainID)
}

func migrationCommandEnvelopeMarkerPath(parentHome, captainID string) string {
	return commandEnvelopeMarkerPath(parentHome, captainID)
}

// removeMigrationMarker removes a migration completion marker.
func removeMigrationMarker(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// -------- main drain entry point --------

// DrainLegacyCommandTransport reconciles and retires both legacy send-outbox
// and command-envelope records for one captain.
//
// Idempotency mechanisms (in priority order):
//  1. Per-record receipts with strict parse+validation against schema, legacy type,
//     legacy identifier, content hash, captain identity, and mailbox message ID.
//     Corrupt/conflicting receipts fail closed and preserve the source record.
//  2. Deterministic mailbox message IDs bound to transport kind + Captain identity
//     + legacy record ID + payload hash. Same legacy ID with changed payload
//     produces a different ID (no conflict). Different legacy IDs with identical
//     payload produce different IDs (distinct envelopes).
//  3. Receipt writes reject conflicting content atomically (temp+rename + pre-check).
//  4. Global done markers never suppress late records — the drain always scans
//     for records regardless of marker state.
//
// Rules:
//   - Valid legacy records are converted to mailbox Envelopes (General->Captain)
//     and written to the captain's inbox and sender's pending.
//   - Each migrated record gets an atomic per-record receipt.
//   - Malformed JSON files in the envelope directory fail closed (error returned).
//   - Malformed outbox entries fail closed (error returned, record preserved).
//   - Unknown/unexpected state files are never deleted.
func DrainLegacyCommandTransport(parentHome string, sm Info) error {
	senderIdentity, senderRank, err := mailbox.ReadHomeIdentity(parentHome)
	if err != nil {
		return fmt.Errorf("%s: deriving sender identity: %w", sm.ID, err)
	}

	canonSM, err := canonicalHome(sm.Home)
	if err != nil {
		return fmt.Errorf("%s: canonicalizing captain home: %w", sm.ID, err)
	}

	if err := drainLegacySendOutbox(parentHome, canonSM, sm.ID, senderIdentity, senderRank); err != nil {
		return fmt.Errorf("%s: drain send-outbox: %w", sm.ID, err)
	}

	if err := drainLegacyCommandEnvelopes(parentHome, canonSM, sm.ID, senderIdentity, senderRank); err != nil {
		return fmt.Errorf("%s: drain command-envelopes: %w", sm.ID, err)
	}

	return nil
}

// drainLegacySendOutbox drains one captain's legacy .captain-send-outbox records.
// Always scans for records regardless of marker state (marker never suppresses).
func drainLegacySendOutbox(parentHome, captainHome, captainID, senderIdentity string, senderRank mailbox.Rank) error {
	mPath := sendOutboxMarkerPath(parentHome, captainID)
	hadMarker := markerExists(mPath)

	paths, err := listSendOutboxPaths(parentHome, captainID)
	if err != nil {
		return fmt.Errorf("listing: %w", err)
	}
	if len(paths) == 0 {
		if !hadMarker {
			if err := os.WriteFile(mPath, []byte("done\n"), 0644); err != nil {
				return fmt.Errorf("writing marker: %w", err)
			}
		}
		return nil
	}

	// Records exist despite marker — clear marker, process late arrivals.
	if hadMarker {
		os.Remove(mPath)
	}

	receiverStore := mailbox.NewStore(captainHome)
	senderStore := mailbox.NewStore(parentHome)

	for _, path := range paths {
		rawData, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", path, readErr)
		}

		entry, parseErr := readSendOutboxEntry(path)
		if parseErr != nil {
			return fmt.Errorf("parsing %s: %w — preserved", path, parseErr)
		}

		legacyID := entry["id"]
		msg := entry["message"]
		if legacyID == "" || msg == "" {
			return fmt.Errorf("malformed outbox entry %s: missing id or message — preserved", path)
		}
		if legacyID != captainID {
			return fmt.Errorf("outbox entry %s has id=%q, expected %q — preserved", path, legacyID, captainID)
		}

		legacyRecordID := filepath.Base(path)                                          // e.g., "1734567890.pending"
		legacyRecordID = strings.TrimSuffix(legacyRecordID, ".pending")                // e.g., "1734567890"
		payloadHash := messagePayloadHash(msg)
		detID := legacySendOutboxContentID(captainID, legacyRecordID, payloadHash)

		// Strict receipt validation: parse, schema, legacy type, identifier, content
		// hash, captain identity, mailbox message ID. On any mismatch or corruption,
		// fail closed and preserve the source.
		if valErr := validateMigrationReceipt(parentHome, captainID, detID,
			LegacySendOutboxType, legacyRecordID, rawData); valErr != nil {
			return fmt.Errorf("receipt validation failed for %s: %w — source preserved", path, valErr)
		}
		// nil from validateMigrationReceipt means either no receipt exists (first run)
		// or receipt validates cleanly (replay). Either way we only skip if receipt
		// is present AND valid. But we already know nil means no receipt or valid.
		// We need to re-check existence to distinguish "valid replay skip" from "first run".
		existingReceipt, _ := os.ReadFile(migrationReceiptPath(parentHome, captainID, detID))
		if existingReceipt != nil {
			// Receipt exists and validated — crash replay skip. Clean up re-created .pending.
			os.Remove(path)
			continue
		}

		// Build mailbox envelope with deterministic ID.
		env := &mailbox.Envelope{
			MessageID:      detID,
			SenderRank:     senderRank,
			SenderIdentity: senderIdentity,
			ReceiverRank:   mailbox.RankCaptain,
			ReceiverID:     captainID,
			Payload:        msg,
		}

		if err := receiverStore.WriteEnvelope(env); err != nil {
			return fmt.Errorf("writing inbox envelope (from legacy %s): %w", path, err)
		}
		if err := senderStore.WritePending(env); err != nil {
			return fmt.Errorf("writing sender pending (from legacy %s): %w", path, err)
		}

		// Write atomic receipt AFTER both mailbox writes succeed.
		if err := writeMigrationReceipt(parentHome, captainID, LegacySendOutboxType, legacyRecordID,
			rawData, captainID, detID); err != nil {
			return fmt.Errorf("writing migration receipt (from legacy %s): %w", path, err)
		}

		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing legacy outbox entry %s: %w", path, err)
		}
	}

	return os.WriteFile(mPath, []byte("done\n"), 0644)
}

// drainLegacyCommandEnvelopes drains one captain's legacy .command-envelope records.
// Always scans for records regardless of marker state (marker never suppresses).
func drainLegacyCommandEnvelopes(parentHome, captainHome, captainID, senderIdentity string, senderRank mailbox.Rank) error {
	mPath := commandEnvelopeMarkerPath(parentHome, captainID)
	hadMarker := markerExists(mPath)

	envs, err := listLegacyPendingEnvelopes(parentHome, captainID)
	if err != nil {
		return fmt.Errorf("listing legacy envelopes: %w", err)
	}
	if len(envs) == 0 {
		if !hadMarker {
			if err := os.WriteFile(mPath, []byte("done\n"), 0644); err != nil {
				return fmt.Errorf("writing marker: %w", err)
			}
		}
		return nil
	}

	// Records exist despite marker — clear marker, process late arrivals.
	if hadMarker {
		os.Remove(mPath)
	}

	receiverStore := mailbox.NewStore(captainHome)
	senderStore := mailbox.NewStore(parentHome)

	for _, env := range envs {
		if env.Status != EnvelopeStatusPending {
			continue
		}
		if env.Message == "" {
			return fmt.Errorf("legacy envelope %s: empty message — preserved", env.EnvelopeID)
		}
		if env.TargetCaptainID != captainID {
			return fmt.Errorf("legacy envelope %s: TargetCaptainID=%q, expected %q — preserved", env.EnvelopeID, env.TargetCaptainID, captainID)
		}

		msg := env.Message
		if !marker.IsFromGeneral(msg) {
			msg = marker.MarkFromGeneral(msg)
		}

		payloadHash := messagePayloadHash(msg)
		detID := legacyCommandEnvelopeContentID(captainID, env.EnvelopeID, payloadHash)

		// Read raw file content for receipt hash before any mutations.
		envPath := envelopePath(parentHome, env.EnvelopeID)
		rawData, readErr := os.ReadFile(envPath)
		if readErr != nil {
			return fmt.Errorf("reading legacy envelope file %s: %w", env.EnvelopeID, readErr)
		}

		// Strict receipt validation. On any mismatch or corruption, fail closed.
		if valErr := validateMigrationReceipt(parentHome, captainID, detID,
			LegacyCommandEnvelopeType, env.EnvelopeID, rawData); valErr != nil {
			return fmt.Errorf("receipt validation failed for %s: %w — source preserved", envPath, valErr)
		}
		existingReceipt, _ := os.ReadFile(migrationReceiptPath(parentHome, captainID, detID))
		if existingReceipt != nil {
			// Receipt exists and validated — crash replay skip.
			MarkEnvelopeDelivered(parentHome, env.EnvelopeID)
			continue
		}

		mailEnv := &mailbox.Envelope{
			MessageID:      detID,
			SenderRank:     senderRank,
			SenderIdentity: senderIdentity,
			ReceiverRank:   mailbox.RankCaptain,
			ReceiverID:     captainID,
			Payload:        msg,
		}

		if err := receiverStore.WriteEnvelope(mailEnv); err != nil {
			return fmt.Errorf("writing inbox envelope (from legacy %s): %w", env.EnvelopeID, err)
		}
		if err := senderStore.WritePending(mailEnv); err != nil {
			return fmt.Errorf("writing sender pending (from legacy %s): %w", env.EnvelopeID, err)
		}

		// Write atomic receipt AFTER both mailbox writes succeed.
		if err := writeMigrationReceipt(parentHome, captainID, LegacyCommandEnvelopeType, env.EnvelopeID,
			rawData, captainID, detID); err != nil {
			return fmt.Errorf("writing migration receipt (from legacy %s): %w", env.EnvelopeID, err)
		}

		if err := MarkEnvelopeDelivered(parentHome, env.EnvelopeID); err != nil {
			return fmt.Errorf("marking legacy envelope %s delivered: %w", env.EnvelopeID, err)
		}
	}

	return os.WriteFile(mPath, []byte("done\n"), 0644)
}

// listLegacyPendingEnvelopes returns all legacy CommandEnvelope records for
// the given captain that are pending. Legacy envelopes live in
// parentHome/state/.command-envelope/ as JSON files.
//
// Malformed or unparseable JSON files fail closed — an error is returned
// immediately so the operator can resolve the corrupt file.
func listLegacyPendingEnvelopes(home, captainID string) ([]*CommandEnvelope, error) {
	envDir := envelopeDir(home)
	entries, err := os.ReadDir(envDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []*CommandEnvelope
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		envPath := filepath.Join(envDir, e.Name())
		env, readErr := readCommandEnvelope(envPath)
		if readErr != nil {
			return nil, fmt.Errorf("unparseable envelope file %s: %w — fail-closed, preserved", envPath, readErr)
		}
		if env.TargetCaptainID != captainID {
			continue
		}
		result = append(result, env)
	}
	return result, nil
}

// readCommandEnvelope reads a single legacy CommandEnvelope from a JSON file.
func readCommandEnvelope(path string) (*CommandEnvelope, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var env CommandEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("json parse error: %w", err)
	}
	return &env, nil
}
