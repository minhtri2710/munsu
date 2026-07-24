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

// Migration marker constants — fingerprint files written under parent state
// after each legacy transport record set is drained once.
const (
	// MigrationSendOutboxPrefix is the prefix for send-outbox migration markers.
	MigrationSendOutboxPrefix = ".migrated-send-outbox"

	// MigrationCommandEnvelopePrefix is the prefix for command-envelope migration markers.
	MigrationCommandEnvelopePrefix = ".migrated-command-envelope"

	// MigrationReceiptDir is the subdirectory under state/ for per-record migration receipts.
	MigrationReceiptDir = ".migration-receipts"

	// MigrationReceiptSchemaVersion is the schema identifier for receipt files.
	MigrationReceiptSchemaVersion = "munsu.legacy-migration-receipt/v1"

	// MigrationLateRecordFingerprint identifies the late-arrival check marker prefix.
	MigrationLateRecordPrefix = ".migrated-late"
)

// MigrationReceipt records the one-to-one mapping from one legacy transport
// record to the mailbox envelope that replaced it. Written atomically after
// both the inbox envelope and sender pending succeed. Provides crash replay
// evidence: on re-run, an existing receipt means the record is already migrated.
type MigrationReceipt struct {
	SchemaVersion    string `json:"schema_version"`
	LegacyType       string `json:"legacy_type"`       // "send-outbox" or "command-envelope"
	LegacyIdentifier string `json:"legacy_identifier"` // filename or envelope ID
	LegacyContentHash string `json:"legacy_content_hash"` // SHA-256 of raw legacy file content
	MailboxMessageID string `json:"mailbox_message_id"`
	MigratedAt       int64  `json:"migrated_at"`
}

// --- path helpers ---

func migrationSendOutboxMarkerPath(parentHome, captainID string) string {
	return filepath.Join(parentHome, "state", MigrationSendOutboxPrefix+"-"+captainID)
}

func migrationCommandEnvelopeMarkerPath(parentHome, captainID string) string {
	return filepath.Join(parentHome, "state", MigrationCommandEnvelopePrefix+"-"+captainID)
}

func migrationLateMarkerPath(parentHome, captainID string) string {
	return filepath.Join(parentHome, "state", MigrationLateRecordPrefix+"-"+captainID)
}

func migrationReceiptDir(parentHome, captainID string) string {
	return filepath.Join(parentHome, "state", MigrationReceiptDir, captainID)
}

func migrationReceiptPath(parentHome, captainID, identifier string) string {
	return filepath.Join(migrationReceiptDir(parentHome, captainID), identifier+".receipt")
}

func isLegacySendOutboxMigrated(parentHome, captainID string) bool {
	_, err := os.Stat(migrationSendOutboxMarkerPath(parentHome, captainID))
	return err == nil
}

func isLegacyCommandEnvelopeMigrated(parentHome, captainID string) bool {
	_, err := os.Stat(migrationCommandEnvelopeMarkerPath(parentHome, captainID))
	return err == nil
}

func writeMigrationMarker(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating marker dir: %w", err)
	}
	return os.WriteFile(path, []byte("done\n"), 0644)
}

// removeMigrationMarker removes a migration completion marker.
func removeMigrationMarker(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// --- deterministic envelope IDs for migration ---

// legacySendOutboxContentID returns a deterministic 32-char hex ID for a
// legacy send-outbox record based on its content. Same content always
// produces the same ID, enabling Mailbox.WriteEnvelope conflict detection.
func legacySendOutboxContentID(captainID, message string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("send-outbox:%s:%s", captainID, message)))
	return hex.EncodeToString(h[:16])
}

// legacyCommandEnvelopeContentID returns a deterministic 32-char hex ID for
// a legacy CommandEnvelope record.
func legacyCommandEnvelopeContentID(captainID, message string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("command-envelope:%s:%s", captainID, message)))
	return hex.EncodeToString(h[:16])
}

// contentHashHex returns the hex SHA-256 hash of raw content bytes.
func contentHashHex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// --- per-record receipt I/O ---

// writeMigrationReceipt writes an atomic per-record receipt after a legacy
// record has been successfully converted and written as a mailbox envelope.
func writeMigrationReceipt(parentHome, captainID, legacyType string, legacyData []byte, mailboxMessageID string) error {
	contentHash := contentHashHex(legacyData)
	identifier := mailboxMessageID // use mailbox message ID as receipt filename
	receipt := MigrationReceipt{
		SchemaVersion:     MigrationReceiptSchemaVersion,
		LegacyType:        legacyType,
		LegacyIdentifier:  identifier,
		LegacyContentHash: contentHash,
		MailboxMessageID:  mailboxMessageID,
		MigratedAt:        time.Now().UnixNano(),
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling receipt: %w", err)
	}
	path := migrationReceiptPath(parentHome, captainID, identifier)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating receipt dir: %w", err)
	}
	// Write atomically via temp + rename.
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

// listMigrationReceipts returns all receipts for a given captain.
func listMigrationReceipts(parentHome, captainID string) ([]*MigrationReceipt, error) {
	dir := migrationReceiptDir(parentHome, captainID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var receipts []*MigrationReceipt
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".receipt") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			continue
		}
		var r MigrationReceipt
		if err := json.Unmarshal(data, &r); err != nil {
			continue
		}
		receipts = append(receipts, &r)
	}
	return receipts, nil
}

// receiptExistsFor checks whether a receipt file exists for a given mailbox message ID.
func receiptExistsFor(parentHome, captainID, mailboxMessageID string) bool {
	_, err := os.Stat(migrationReceiptPath(parentHome, captainID, mailboxMessageID))
	return err == nil
}

// --- legacy legacyState for tracking what's been seen ---

// legacyState returns the migration marker state for a captain.
// returned booleans: (markerExists, hasLateMarker)
func legacyMarkerState(parentHome, captainID string) (migrated bool) {
	return isLegacySendOutboxMigrated(parentHome, captainID)
}

// --- main drain entry point ---

// DrainLegacyCommandTransport reconciles and retires both legacy send-outbox
// and command-envelope records for one captain. It is idempotent via:
//   - Migration markers (one-shot completion skip)
//   - Per-record receipts (crash replay: already-migrated records are skipped)
//   - Deterministic mailbox message IDs (same content → same ID, conflict detection)
//
// Rules:
//   - Valid legacy records are converted to mailbox Envelopes (General→Captain)
//     and written to the captain's inbox and sender's pending.
//   - Each migrated record gets an atomic per-record receipt with content hash.
//   - Malformed JSON files in the envelope directory fail closed (error returned).
//   - Malformed outbox entries fail closed (error returned, record preserved).
//   - Unknown/unexpected state files are never deleted.
//   - Late-arriving records after a completion marker are still drained
//     (the marker is removed and re-written after processing).
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
// Uses per-record receipts for crash replay: on re-run, records with an existing
// receipt are skipped. Uses deterministic IDs for conflict detection.
func drainLegacySendOutbox(parentHome, captainHome, captainID, senderIdentity string, senderRank mailbox.Rank) error {
	markerPath := migrationSendOutboxMarkerPath(parentHome, captainID)
	markerExists := false
	if _, err := os.Stat(markerPath); err == nil {
		markerExists = true
	}

	paths, err := listSendOutboxPaths(parentHome, captainID)
	if err != nil {
		return fmt.Errorf("listing: %w", err)
	}
	if len(paths) == 0 {
		if markerExists {
			return nil // already fully migrated
		}
		return writeMigrationMarker(markerPath)
	}

	// Late records: if marker exists but records remain, clear marker and
	// process the late arrivals. The marker will be re-written on completion.
	if markerExists {
		if err := removeMigrationMarker(markerPath); err != nil {
			return fmt.Errorf("clearing marker for late records: %w", err)
		}
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

		// Deterministic mailbox message ID from content.
		detID := legacySendOutboxContentID(captainID, msg)

		// Crash replay: if receipt already exists for this deterministic ID,
		// the record was already migrated in a prior run. Skip it.
		if receiptExistsFor(parentHome, captainID, detID) {
			// Legacy .pending file may have been re-created — remove it.
			os.Remove(path)
			continue
		}

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
		if err := writeMigrationReceipt(parentHome, captainID, "send-outbox", rawData, detID); err != nil {
			return fmt.Errorf("writing migration receipt (from legacy %s): %w", path, err)
		}

		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing legacy outbox entry %s: %w", path, err)
		}
	}

	return writeMigrationMarker(markerPath)
}

// drainLegacyCommandEnvelopes drains one captain's legacy .command-envelope records.
// Uses per-record receipts for crash replay and deterministic IDs for conflict detection.
func drainLegacyCommandEnvelopes(parentHome, captainHome, captainID, senderIdentity string, senderRank mailbox.Rank) error {
	markerPath := migrationCommandEnvelopeMarkerPath(parentHome, captainID)
	markerExists := false
	if _, err := os.Stat(markerPath); err == nil {
		markerExists = true
	}

	envs, err := listLegacyPendingEnvelopes(parentHome, captainID)
	if err != nil {
		return fmt.Errorf("listing legacy envelopes: %w", err)
	}
	if len(envs) == 0 {
		if markerExists {
			return nil // already fully migrated
		}
		return writeMigrationMarker(markerPath)
	}

	// Late records: if marker exists but records remain, clear marker.
	if markerExists {
		if err := removeMigrationMarker(markerPath); err != nil {
			return fmt.Errorf("clearing marker for late records: %w", err)
		}
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

		// Deterministic mailbox message ID from content.
		detID := legacyCommandEnvelopeContentID(captainID, msg)

		// Crash replay: if receipt already exists, skip (already migrated).
		if receiptExistsFor(parentHome, captainID, detID) {
			// Still mark legacy envelope delivered if not already.
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

		// Read raw file content for the receipt hash before any mutations.
		rawData, readErr := os.ReadFile(envelopePath(parentHome, env.EnvelopeID))
		if readErr != nil {
			return fmt.Errorf("reading legacy envelope file %s: %w", env.EnvelopeID, readErr)
		}

		if err := receiverStore.WriteEnvelope(mailEnv); err != nil {
			return fmt.Errorf("writing inbox envelope (from legacy %s): %w", env.EnvelopeID, err)
		}
		if err := senderStore.WritePending(mailEnv); err != nil {
			return fmt.Errorf("writing sender pending (from legacy %s): %w", env.EnvelopeID, err)
		}

		// Write atomic receipt AFTER both mailbox writes succeed.
		if err := writeMigrationReceipt(parentHome, captainID, "command-envelope", rawData, detID); err != nil {
			return fmt.Errorf("writing migration receipt (from legacy %s): %w", env.EnvelopeID, err)
		}

		if err := MarkEnvelopeDelivered(parentHome, env.EnvelopeID); err != nil {
			return fmt.Errorf("marking legacy envelope %s delivered: %w", env.EnvelopeID, err)
		}
	}

	return writeMigrationMarker(markerPath)
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
			// Fail closed: malformed JSON is a blocking error, never silently skipped.
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
// Returns an error for unparseable or non-JSON content (fail-closed).
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
