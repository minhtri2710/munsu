package captain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
)

// migrationSendOutboxMarkerPath returns the one-shot marker path for send-outbox
// migration for a given captain.
func migrationSendOutboxMarkerPath(parentHome, captainID string) string {
	return filepath.Join(parentHome, "state", MigrationSendOutboxPrefix+"-"+captainID)
}

// migrationCommandEnvelopeMarkerPath returns the one-shot marker path for
// command-envelope migration for a given captain.
func migrationCommandEnvelopeMarkerPath(parentHome, captainID string) string {
	return filepath.Join(parentHome, "state", MigrationCommandEnvelopePrefix+"-"+captainID)
}

// isLegacySendOutboxMigrated returns true if the send-outbox migration marker exists.
func isLegacySendOutboxMigrated(parentHome, captainID string) bool {
	_, err := os.Stat(migrationSendOutboxMarkerPath(parentHome, captainID))
	return err == nil
}

// isLegacyCommandEnvelopeMigrated returns true if the command-envelope migration marker exists.
func isLegacyCommandEnvelopeMigrated(parentHome, captainID string) bool {
	_, err := os.Stat(migrationCommandEnvelopeMarkerPath(parentHome, captainID))
	return err == nil
}

// writeMigrationMarker writes a one-shot migration fingerprint marker.
func writeMigrationMarker(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating marker dir: %w", err)
	}
	return os.WriteFile(path, []byte("done\n"), 0644)
}

// DrainLegacyCommandTransport reconciles and retires both legacy send-outbox
// and command-envelope records for one captain. It is idempotent — once a
// fingerprint marker exists, the corresponding drain is skipped.
//
// Rules:
//   - Valid legacy records are converted to mailbox Envelopes (General→Captain)
//     and written to the captain's inbox and sender's pending.
//   - Malformed records are left untouched and an error is returned (fail-closed).
//   - Unknown/unexpected state files are never deleted.
//   - After successful drain of all records for a transport, a fingerprint marker
//     is written to prevent re-execution.
func DrainLegacyCommandTransport(parentHome string, sm Info) error {
	// Derive General sender identity from parent home.
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
func drainLegacySendOutbox(parentHome, captainHome, captainID, senderIdentity string, senderRank mailbox.Rank) error {
	markerPath := migrationSendOutboxMarkerPath(parentHome, captainID)
	if _, err := os.Stat(markerPath); err == nil {
		return nil // already migrated
	}

	paths, err := listSendOutboxPaths(parentHome, captainID)
	if err != nil {
		return fmt.Errorf("listing: %w", err)
	}
	if len(paths) == 0 {
		// No records to drain — write marker and done.
		return writeMigrationMarker(markerPath)
	}

	receiverStore := mailbox.NewStore(captainHome)
	senderStore := mailbox.NewStore(parentHome)

	for _, path := range paths {
		entry, readErr := readSendOutboxEntry(path)
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", path, readErr)
		}

		legacyID := entry["id"]
		msg := entry["message"]
		if legacyID == "" || msg == "" {
			return fmt.Errorf("malformed outbox entry %s: missing id or message — preserved", path)
		}
		if legacyID != captainID {
			return fmt.Errorf("outbox entry %s has id=%q, expected %q — preserved", path, legacyID, captainID)
		}

		// Convert legacy message to mailbox envelope.
		env := &mailbox.Envelope{
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

		// Remove only after successful mailbox writes.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing legacy outbox entry %s: %w", path, err)
		}
	}

	return writeMigrationMarker(markerPath)
}

// drainLegacyCommandEnvelopes drains one captain's legacy .command-envelope records.
func drainLegacyCommandEnvelopes(parentHome, captainHome, captainID, senderIdentity string, senderRank mailbox.Rank) error {
	markerPath := migrationCommandEnvelopeMarkerPath(parentHome, captainID)
	if _, err := os.Stat(markerPath); err == nil {
		return nil // already migrated
	}

	// List legacy pending envelopes for this captain.
	envs, err := listLegacyPendingEnvelopes(parentHome, captainID)
	if err != nil {
		return fmt.Errorf("listing legacy envelopes: %w", err)
	}
	if len(envs) == 0 {
		return writeMigrationMarker(markerPath)
	}

	receiverStore := mailbox.NewStore(captainHome)
	senderStore := mailbox.NewStore(parentHome)

	for _, env := range envs {
		if env.Status != EnvelopeStatusPending {
			// Non-pending envelopes are terminal — skip, don't error.
			continue
		}
		if env.Message == "" {
			return fmt.Errorf("legacy envelope %s: empty message — preserved", env.EnvelopeID)
		}
		if env.TargetCaptainID != captainID {
			return fmt.Errorf("legacy envelope %s: TargetCaptainID=%q, expected %q — preserved", env.EnvelopeID, env.TargetCaptainID, captainID)
		}

		// Convert legacy CommandEnvelope to mailbox Envelope.
		// Use the existing message (may already carry from-general marker).
		msg := env.Message
		if !marker.IsFromGeneral(msg) {
			msg = marker.MarkFromGeneral(msg)
		}

		mailEnv := &mailbox.Envelope{
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

		// Mark legacy envelope as delivered to prevent re-processing.
		if err := MarkEnvelopeDelivered(parentHome, env.EnvelopeID); err != nil {
			return fmt.Errorf("marking legacy envelope %s delivered: %w", env.EnvelopeID, err)
		}
	}

	return writeMigrationMarker(markerPath)
}

// listLegacyPendingEnvelopes returns all legacy CommandEnvelope records for
// the given captain that are pending. Legacy envelopes live in
// parentHome/state/.command-envelope/ as JSON files.
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
		env, readErr := readCommandEnvelope(filepath.Join(envDir, e.Name()))
		if readErr != nil {
			continue // skip unparseable files
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
		return nil, err
	}
	return &env, nil
}
