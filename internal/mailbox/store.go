package mailbox

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
	return filepath.Join(s.homeDir, "state", InboxDir, senderIdentity)
}

func (s *Store) inboxPath(senderIdentity, messageID string) string {
	return filepath.Join(s.inboxDir(senderIdentity), messageID+".json")
}

func (s *Store) ackPath(senderIdentity, messageID string) string {
	return filepath.Join(s.inboxDir(senderIdentity), messageID+".ack")
}

func (s *Store) pendingDir(senderIdentity string) string {
	return filepath.Join(s.homeDir, "state", OutboxDir, senderIdentity)
}

func (s *Store) pendingPath(senderIdentity, messageID string) string {
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
func (s *Store) WriteEnvelope(env *Envelope) error {
	if env.MessageID == "" {
		id, err := NewMessageID()
		if err != nil {
			return err
		}
		env.MessageID = id
	}
	env.SchemaVersion = SchemaVersion
	env.PayloadHash = PayloadHashHex(env.Payload)
	env.CreatedAt = time.Now().UnixNano()

	if err := ValidateEnvelope(env); err != nil {
		return fmt.Errorf("write envelope: %w", err)
	}
	dir := s.inboxDir(env.SenderIdentity)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create inbox dir: %w", err)
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	return atomicWrite(s.inboxPath(env.SenderIdentity, env.MessageID), data)
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
func (s *Store) WriteAck(ack *ProcessingAck) error {
	ack.SchemaVersion = AckSchemaVersion
	if ack.ProcessedAt == 0 {
		ack.ProcessedAt = time.Now().UnixNano()
	}
	dir := s.inboxDir(ack.SenderIdentity)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create ack dir: %w", err)
	}
	data, err := json.MarshalIndent(ack, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ack: %w", err)
	}
	return atomicWrite(s.ackPath(ack.SenderIdentity, ack.MessageID), data)
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

// RemovePending removes a pending record after the caller has verified
// a matching ack via ValidateAck.
func (s *Store) RemovePending(senderIdentity, messageID string) error {
	err := os.Remove(s.pendingPath(senderIdentity, messageID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ListAllPending returns all pending records across all sender identities.
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
// Deprecated: use Store.RemovePending.
func RemoveSenderPending(senderHome, senderIdentity, messageID string) error {
	return NewStore(senderHome).RemovePending(senderIdentity, messageID)
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
