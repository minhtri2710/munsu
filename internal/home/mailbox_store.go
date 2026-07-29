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

func (s *Store) inboxDir(senderIdentity string) string {
	if err := ValidatePathComponent(senderIdentity, "sender identity"); err != nil {
		return filepath.Join(s.homeDir, "state", InboxDir, "_invalid_")
	}
	return filepath.Join(s.homeDir, "state", InboxDir, senderIdentity)
}

func (s *Store) inboxPath(senderIdentity, messageID string) string {
	if err := ValidatePathComponent(messageID, "message ID"); err != nil {
		return filepath.Join(s.inboxDir(senderIdentity), "_invalid_.json")
	}
	return filepath.Join(s.inboxDir(senderIdentity), messageID+".json")
}

func (s *Store) ackPath(senderIdentity, messageID string) string {
	if err := ValidatePathComponent(messageID, "message ID"); err != nil {
		return filepath.Join(s.inboxDir(senderIdentity), "_invalid_.ack")
	}
	return filepath.Join(s.inboxDir(senderIdentity), messageID+".ack")
}

func (s *Store) pendingDir(senderIdentity string) string {
	if err := ValidatePathComponent(senderIdentity, "sender identity"); err != nil {
		return filepath.Join(s.homeDir, "state", OutboxDir, "_invalid_")
	}
	return filepath.Join(s.homeDir, "state", OutboxDir, senderIdentity)
}

func (s *Store) SupersededPath(senderIdentity, messageID string) string {
	return filepath.Join(s.inboxDir(senderIdentity), messageID+".superseded")
}

func (s *Store) MarkSuperseded(senderIdentity, messageID string) error {
	if err := os.MkdirAll(s.inboxDir(senderIdentity), 0755); err != nil {
		return err
	}
	return atomicWrite(s.SupersededPath(senderIdentity, messageID), []byte("superseded\n"))
}

func (s *Store) IsSuperseded(senderIdentity, messageID string) bool {
	_, err := os.Stat(s.SupersededPath(senderIdentity, messageID))
	return err == nil
}

func (s *Store) pendingPath(senderIdentity, messageID string) string {
	if err := ValidatePathComponent(messageID, "message ID"); err != nil {
		return filepath.Join(s.pendingDir(senderIdentity), "_invalid_.pending")
	}
	return filepath.Join(s.pendingDir(senderIdentity), messageID+".pending")
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
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
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
	path := s.inboxPath(env.SenderIdentity, env.MessageID)
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

	dir := s.inboxDir(env.SenderIdentity)
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
	data, err := os.ReadFile(s.inboxPath(senderIdentity, messageID))
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
	return &env, nil
}

// ListInbox returns all envelopes in the inbox for a given sender
// that do not have a corresponding ack file.
func (s *Store) ListInbox(senderIdentity string) ([]*Envelope, error) {
	dir := s.inboxDir(senderIdentity)
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
		// Skip if ack exists.
		if _, err := os.Stat(s.ackPath(senderIdentity, messageID)); err == nil {
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
		return envelopes[i].CreatedAt < envelopes[j].CreatedAt
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
	path := s.ackPath(ack.SenderIdentity, ack.MessageID)
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

	dir := s.inboxDir(ack.SenderIdentity)
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
	data, err := os.ReadFile(s.ackPath(senderIdentity, messageID))
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
	return &ack, nil
}

// IsAcked returns true if an ack file exists for the given message.
func (s *Store) IsAcked(senderIdentity, messageID string) bool {
	_, err := os.Stat(s.ackPath(senderIdentity, messageID))
	return err == nil
}

// --- Pending I/O ---

// WritePending writes a sender pending record scoped by sender identity.
// The record is written atomically.
func (s *Store) WritePending(env *Envelope) error {
	dir := s.pendingDir(env.SenderIdentity)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create pending dir: %w", err)
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pending: %w", err)
	}
	return atomicWrite(s.pendingPath(env.SenderIdentity, env.MessageID), data)
}

// ReadPending reads a pending record for the given sender identity and
// message ID. Returns nil, nil if not found.
func (s *Store) ReadPending(senderIdentity, messageID string) (*Envelope, error) {
	data, err := os.ReadFile(s.pendingPath(senderIdentity, messageID))
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
	dir := s.pendingDir(senderIdentity)
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
		return envelopes[i].CreatedAt < envelopes[j].CreatedAt
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
	if err := ValidateAck(pending, ack); err != nil {
		return fmt.Errorf("remove pending: ack validation failed: %w", err)
	}
	if err := os.Remove(s.pendingPath(senderIdentity, messageID)); err != nil && !os.IsNotExist(err) {
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
		return envelopes[i].CreatedAt < envelopes[j].CreatedAt
	})
	return envelopes, nil
}

// --- Legacy standalone helpers (delegate to Store) ---

// GetInboxEnvelope reads an envelope from the receiver's inbox.
// Deprecated: use Store.ReadEnvelope.
func GetInboxEnvelope(receiverHome, senderIdentity, messageID string) (*Envelope, error) {
	return NewStore(receiverHome).ReadEnvelope(senderIdentity, messageID)
}

// SaveSenderPending writes a pending record scoped by sender identity.
// Deprecated: use Store.WritePending.
func SaveSenderPending(senderHome string, env *Envelope) (string, error) {
	s := NewStore(senderHome)
	if err := s.WritePending(env); err != nil {
		return "", err
	}
	return s.pendingPath(env.SenderIdentity, env.MessageID), nil
}

// RemoveSenderPending removes a pending record scoped by sender identity.
// Deprecated: use Store.RemovePendingAfterAck.
func RemoveSenderPending(senderHome, senderIdentity, messageID string) error {
	// Legacy helper cannot call RemovePendingAfterAck since it doesn't have
	// the ack. It reads the pending and ack from the receiver's inbox.
	// The receiver home is the same as sender home (single-node case) —
	// in the legacy path both are the same directory.
	store := NewStore(senderHome)
	pending, err := store.ReadPending(senderIdentity, messageID)
	if err != nil || pending == nil {
		return err
	}
	ack, err := store.ReadAck(senderIdentity, messageID)
	if err != nil {
		return err
	}
	if ack == nil {
		return fmt.Errorf("remove pending: no ack found for message %q", messageID)
	}
	if err := ValidateAck(pending, ack); err != nil {
		return fmt.Errorf("remove pending: ack validation failed: %w", err)
	}
	return os.Remove(store.pendingPath(senderIdentity, messageID))
}

// ListSenderPending returns all pending records for a sender identity.
// Deprecated: use Store.ListPending.
func ListSenderPending(senderHome, senderIdentity string) ([]*Envelope, error) {
	return NewStore(senderHome).ListPending(senderIdentity)
}

// NewEnvelope creates, validates, and atomically writes an envelope to the
// receiver's inbox.
// Deprecated: use Store.WriteEnvelope.
func NewEnvelope(receiverHome string, env *Envelope) error {
	return NewStore(receiverHome).WriteEnvelope(env)
}

// ListPendingInbox returns all envelopes in the inbox for a sender identity
// that have not been acked.
// Deprecated: use Store.ListInbox.
func ListPendingInbox(receiverHome, senderIdentity string) ([]*Envelope, error) {
	return NewStore(receiverHome).ListInbox(senderIdentity)
}

// IsAcked returns true if an ack file exists for the given message.
// Deprecated: use Store.IsAcked.
func IsAcked(receiverHome, senderIdentity, messageID string) bool {
	return NewStore(receiverHome).IsAcked(senderIdentity, messageID)
}
