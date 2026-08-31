package home

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Store provides durable mailbox I/O for a single home directory.
// All writes use atomic temp-file + rename to prevent partial writes.
//
// Usage:
//
//	receiverStore := mailbox.NewStore(receiverHome)
//	receiverStore.WriteEnvelope(env)
//	receiverStore.WriteAck(ack)
//
//	senderStore := mailbox.NewStore(senderHome)
//	senderStore.WritePending(env)
type Store struct {
	homeDir string
}

// NewStore creates a Store rooted at the given home directory.
func NewStore(homeDir string) *Store {
	return &Store{homeDir: homeDir}
}

// --- path helpers (unexported) ---

func (s *Store) inboxDir(senderIdentity string) (string, error) {
	if err := ValidatePathComponent(senderIdentity, "sender identity"); err != nil {
		return "", err
	}
	return filepath.Join(s.homeDir, "state", InboxDir, senderIdentity), nil
}

func (s *Store) inboxPath(senderIdentity, messageID string) (string, error) {
	if err := ValidatePathComponent(senderIdentity, "sender identity"); err != nil {
		return "", err
	}
	if err := ValidatePathComponent(messageID, "message ID"); err != nil {
		return "", err
	}
	dir, err := s.inboxDir(senderIdentity)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, messageID+".json"), nil
}

func (s *Store) ackPath(senderIdentity, messageID string) (string, error) {
	if err := ValidatePathComponent(senderIdentity, "sender identity"); err != nil {
		return "", err
	}
	if err := ValidatePathComponent(messageID, "message ID"); err != nil {
		return "", err
	}
	dir, err := s.inboxDir(senderIdentity)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, messageID+".ack"), nil
}

func (s *Store) pendingDir(senderIdentity string) (string, error) {
	if err := ValidatePathComponent(senderIdentity, "sender identity"); err != nil {
		return "", err
	}
	return filepath.Join(s.homeDir, "state", OutboxDir, senderIdentity), nil
}

func (s *Store) SupersededPath(senderIdentity, messageID string) (string, error) {
	if err := ValidatePathComponent(senderIdentity, "sender identity"); err != nil {
		return "", err
	}
	if err := ValidatePathComponent(messageID, "message ID"); err != nil {
		return "", err
	}
	dir, err := s.inboxDir(senderIdentity)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, messageID+".superseded"), nil
}

func (s *Store) MarkSuperseded(senderIdentity, messageID string) error {
	path, err := s.SupersededPath(senderIdentity, messageID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := atomicWrite(path, []byte("superseded\n")); err != nil {
		return err
	}
	s.removeInboxPayload(senderIdentity, messageID)
	return nil
}

func (s *Store) removeInboxPayload(senderIdentity, messageID string) {
	path, err := s.inboxPath(senderIdentity, messageID)
	if err != nil {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return
	}
}

func (s *Store) IsSuperseded(senderIdentity, messageID string) bool {
	path, err := s.SupersededPath(senderIdentity, messageID)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func (s *Store) pendingPath(senderIdentity, messageID string) (string, error) {
	if err := ValidatePathComponent(senderIdentity, "sender identity"); err != nil {
		return "", err
	}
	if err := ValidatePathComponent(messageID, "message ID"); err != nil {
		return "", err
	}
	dir, err := s.pendingDir(senderIdentity)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, messageID+".pending"), nil
}

// --- atomic write ---

// atomicWrite writes data to path using a temp file and rename.
// This prevents partial writes from being observed.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := RenameDurable(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename temp: %w", err)
	}
	return nil
}

// --- Envelope I/O ---

// WriteEnvelope writes an immutable envelope to the receiver's inbox.
// The envelope is finalized (SchemaVersion, MessageID, PayloadHash, CreatedAt)
// and written atomically.
//
// Idempotent: writing the same envelope content twice is OK.
// Conflict: writing a different envelope with the same message ID returns an error.
func (s *Store) WriteEnvelope(env *Envelope) error {
	if env.MessageID == "" {
		id, err := NewMessageID()
		if err != nil {
			return err
		}
		env.MessageID = id
	}
	if err := ValidatePathComponent(env.MessageID, "message ID"); err != nil {
		return fmt.Errorf("write envelope: %w", err)
	}
	if err := ValidatePathComponent(env.SenderIdentity, "sender identity"); err != nil {
		return fmt.Errorf("write envelope: %w", err)
	}
	if err := ValidatePathComponent(env.ReceiverID, "receiver ID"); err != nil {
		return fmt.Errorf("write envelope: %w", err)
	}
	env.SchemaVersion = SchemaVersion
	env.PayloadHash = PayloadHashHex(env.Payload)
	env.CreatedAt = time.Now().UnixNano()

	if err := ValidateEnvelope(env); err != nil {
		return fmt.Errorf("write envelope: %w", err)
	}

	// Check for existing envelope with same message ID.
	path, err := s.inboxPath(env.SenderIdentity, env.MessageID)
	if err != nil {
		return fmt.Errorf("write envelope: %w", err)
	}
	if existing, err := os.ReadFile(path); err == nil {
		var old Envelope
		if err := json.Unmarshal(existing, &old); err == nil {
			// Compare identity fields — same content is idempotent.
			if old.SenderRank != env.SenderRank ||
				old.SenderIdentity != env.SenderIdentity ||
				old.ReceiverRank != env.ReceiverRank ||
				old.ReceiverID != env.ReceiverID ||
				old.Kind != env.Kind ||
				old.TaskID != env.TaskID ||
				old.Key != env.Key ||
				old.Payload != env.Payload {
				return fmt.Errorf("write envelope: conflict: message ID %q already exists with different content", env.MessageID)
			}
			return nil // idempotent: same content, OK
		}
		// Existing file is corrupt; overwrite is allowed.
	}

	dir, err := s.inboxDir(env.SenderIdentity)
	if err != nil {
		return fmt.Errorf("write envelope: %w", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create inbox dir: %w", err)
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	return atomicWrite(path, data)
}

// ReadEnvelope reads an envelope from the receiver's inbox.
// Returns nil, nil if not found. Reads both current and legacy v1 formats.
func (s *Store) ReadEnvelope(senderIdentity, messageID string) (*Envelope, error) {
	path, err := s.inboxPath(senderIdentity, messageID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("unmarshal envelope %s: %w", messageID, err)
	}
	if env.MessageID != messageID {
		return nil, fmt.Errorf("read envelope %s: path message ID %q does not match decoded message ID %q", path, messageID, env.MessageID)
	}
	return &env, nil
}

// ListInbox returns all valid, actionable envelopes in the inbox for a given
// sender that have neither a corresponding ack nor a supersession marker.
func (s *Store) ListInbox(senderIdentity string) ([]*Envelope, error) {
	dir, err := s.inboxDir(senderIdentity)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var envelopes []*Envelope
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		messageID := strings.TrimSuffix(e.Name(), ".json")
		if err := ValidatePathComponent(messageID, "message ID"); err != nil {
			continue
		}
		// Skip if ack or supersession exists. A tombstone is authoritative;
		// opportunistically remove any payload left by a crash between the
		// durable tombstone and the original unlink.
		ackPath, err := s.ackPath(senderIdentity, messageID)
		if err != nil {
			continue
		}
		if _, err := os.Stat(ackPath); err == nil {
			s.removeInboxPayload(senderIdentity, messageID)
			continue
		}
		if s.IsSuperseded(senderIdentity, messageID) {
			s.removeInboxPayload(senderIdentity, messageID)
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			continue
		}
		var env Envelope
		if unmarshalErr := json.Unmarshal(data, &env); unmarshalErr != nil {
			continue
		}
		if env.MessageID != messageID || ValidateEnvelope(&env) != nil || PayloadHashHex(env.Payload) != env.PayloadHash {
			continue
		}
		envelopes = append(envelopes, &env)
	}
	sort.Slice(envelopes, func(i, j int) bool {
		if envelopes[i].CreatedAt != envelopes[j].CreatedAt {
			return envelopes[i].CreatedAt < envelopes[j].CreatedAt
		}
		return envelopes[i].MessageID < envelopes[j].MessageID
	})
	return envelopes, nil
}

// --- Ack I/O ---

// WriteAck writes a ProcessingAck to the receiver's inbox alongside the
// envelope. The ack is written atomically.
//
// Idempotent: writing the exact same ack twice is OK.
// Conflict: writing a different ack for the same message ID returns an error.
func (s *Store) WriteAck(ack *ProcessingAck) error {
	// Validate ack fields.
	if ack.ProcessedAt <= 0 {
		return fmt.Errorf("write ack: processed_at must be > 0, got %d", ack.ProcessedAt)
	}
	if !ValidOutcome(ack.Outcome) {
		return fmt.Errorf("write ack: invalid outcome %q", ack.Outcome)
	}
	if err := ValidatePathComponent(ack.MessageID, "message ID"); err != nil {
		return fmt.Errorf("write ack: %w", err)
	}
	if err := ValidatePathComponent(ack.SenderIdentity, "sender identity"); err != nil {
		return fmt.Errorf("write ack: %w", err)
	}
	if err := ValidatePathComponent(ack.ReceiverID, "receiver ID"); err != nil {
		return fmt.Errorf("write ack: %w", err)
	}

	ack.SchemaVersion = AckSchemaVersion

	// Check for existing ack with same message ID.
	path, err := s.ackPath(ack.SenderIdentity, ack.MessageID)
	if err != nil {
		return fmt.Errorf("write ack: %w", err)
	}
	if existing, err := os.ReadFile(path); err == nil {
		var old ProcessingAck
		if err := json.Unmarshal(existing, &old); err == nil {
			// Compare all identity fields — exact same ack is idempotent.
			if old.MessageID == ack.MessageID &&
				old.SenderRank == ack.SenderRank &&
				old.SenderIdentity == ack.SenderIdentity &&
				old.ReceiverRank == ack.ReceiverRank &&
				old.ReceiverID == ack.ReceiverID &&
				old.TaskID == ack.TaskID &&
				old.Key == ack.Key &&
				old.PayloadHash == ack.PayloadHash &&
				old.Outcome == ack.Outcome {
				return nil // idempotent: exact same ack, OK
			}
			return fmt.Errorf("write ack: conflict: message ID %q already has a different ack", ack.MessageID)
		}
		// Existing file is corrupt; overwrite is allowed.
	}

	dir, err := s.inboxDir(ack.SenderIdentity)
	if err != nil {
		return fmt.Errorf("write ack: %w", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create ack dir: %w", err)
	}
	data, err := json.MarshalIndent(ack, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ack: %w", err)
	}
	return atomicWrite(path, data)
}

// ReadAck reads a ProcessingAck from the receiver's inbox.
// Returns nil, nil if not found.
func (s *Store) ReadAck(senderIdentity, messageID string) (*ProcessingAck, error) {
	path, err := s.ackPath(senderIdentity, messageID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ack ProcessingAck
	if err := json.Unmarshal(data, &ack); err != nil {
		return nil, fmt.Errorf("unmarshal ack %s: %w", messageID, err)
	}
	if ack.MessageID != messageID {
		return nil, fmt.Errorf("read ack %s: path message ID %q does not match decoded message ID %q", path, messageID, ack.MessageID)
	}
	return &ack, nil
}

// IsAcked returns true if an ack file exists for the given message.
func (s *Store) IsAcked(senderIdentity, messageID string) bool {
	path, err := s.ackPath(senderIdentity, messageID)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// --- Pending I/O ---

// WritePending writes a sender pending record scoped by sender identity.
// The record is written atomically.
func (s *Store) WritePending(env *Envelope) error {
	if err := ValidatePathComponent(env.SenderIdentity, "sender identity"); err != nil {
		return fmt.Errorf("write pending: %w", err)
	}
	if err := ValidatePathComponent(env.MessageID, "message ID"); err != nil {
		return fmt.Errorf("write pending: %w", err)
	}
	dir, err := s.pendingDir(env.SenderIdentity)
	if err != nil {
		return fmt.Errorf("write pending: %w", err)
	}
	path, err := s.pendingPath(env.SenderIdentity, env.MessageID)
	if err != nil {
		return fmt.Errorf("write pending: %w", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create pending dir: %w", err)
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pending: %w", err)
	}
	return atomicWrite(path, data)
}

// ReadPending reads a pending record for the given sender identity and
// message ID. Returns nil, nil if not found.
func (s *Store) ReadPending(senderIdentity, messageID string) (*Envelope, error) {
	path, err := s.pendingPath(senderIdentity, messageID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("unmarshal pending %s: %w", messageID, err)
	}
	return &env, nil
}

// ListPending returns all pending records for a given sender identity.
func (s *Store) ListPending(senderIdentity string) ([]*Envelope, error) {
	dir, err := s.pendingDir(senderIdentity)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var envelopes []*Envelope
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pending") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			continue
		}
		var env Envelope
		if unmarshalErr := json.Unmarshal(data, &env); unmarshalErr != nil {
			continue
		}
		envelopes = append(envelopes, &env)
	}
	sort.Slice(envelopes, func(i, j int) bool {
		if envelopes[i].CreatedAt != envelopes[j].CreatedAt {
			return envelopes[i].CreatedAt < envelopes[j].CreatedAt
		}
		return envelopes[i].MessageID < envelopes[j].MessageID
	})
	return envelopes, nil
}

// RemovePendingAfterAck removes a pending record only after validating that
// ack matches the sender's pending envelope. This replaces the unconditional
// RemovePending to ensure pending records are never removed without a
// validated matching ack. The ack must be provided by the caller (typically
// read from the receiver store, since ack lives on the receiver, not sender).
func (s *Store) RemovePendingAfterAck(senderIdentity, messageID string, ack *ProcessingAck) error {
	if ack == nil {
		return fmt.Errorf("remove pending: ack is nil")
	}
	pending, err := s.ReadPending(senderIdentity, messageID)
	if err != nil {
		return err
	}
	if pending == nil {
		return nil // already removed
	}
	if err := ValidateProcessingAck(ack); err != nil {
		return fmt.Errorf("remove pending: processing ack validation failed: %w", err)
	}
	if err := ValidateAck(pending, ack); err != nil {
		return fmt.Errorf("remove pending: ack validation failed: %w", err)
	}
	path, err := s.pendingPath(senderIdentity, messageID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ListAllPending returns all pending records across all sender identities.
// Deprecated: prefer scoped ListPending per sender identity.
func (s *Store) ListAllPending() ([]*Envelope, error) {
	outboxRoot := filepath.Join(s.homeDir, "state", OutboxDir)
	entries, err := os.ReadDir(outboxRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var envelopes []*Envelope
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		envs, err := s.ListPending(entry.Name())
		if err != nil {
			continue
		}
		envelopes = append(envelopes, envs...)
	}
	sort.Slice(envelopes, func(i, j int) bool {
		if envelopes[i].CreatedAt != envelopes[j].CreatedAt {
			return envelopes[i].CreatedAt < envelopes[j].CreatedAt
		}
		return envelopes[i].MessageID < envelopes[j].MessageID
	})
	return envelopes, nil
}

// --- Legacy standalone helpers (delegate to Store) ---

// GetInboxEnvelope reads an envelope from the receiver's inbox.
// Deprecated: use Store.ReadEnvelope.
func GetInboxEnvelope(receiverHome, senderIdentity, messageID string) (*Envelope, error) {
	return NewStore(receiverHome).ReadEnvelope(senderIdentity, messageID)
}
