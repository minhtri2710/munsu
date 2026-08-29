package orchestrator

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

// RecoveryAttempt describes one recovery retry attempt.
type RecoveryAttempt struct {
	MessageID  string
	Delivered  bool // true if delivery succeeded
	AlreadyAck bool // true if already acknowledged (no retry needed)
	Skipped    bool // true if skipped (already handled)
	Err        error
}

// RecoveryMarkerPath returns the path for a recovery fingerprint marker file.
func RecoveryMarkerPath(receiverHome, messageID string) string {
	safeID := strings.NewReplacer("/", "_", ":", "_", ".", "_").Replace(messageID)
	return filepath.Join(receiverHome, "state", ".recovered-"+safeID)
}

// RecoverInbox attempts one recovery delivery for a single pending envelope.
// It checks if the envelope was already acked and skips if so.
// It writes a fingerprint marker on completion to prevent repeated retries.
func RecoverInboxWithSender(sender BoundSender, receiverHome string, env *Envelope) *RecoveryAttempt {
	ra := &RecoveryAttempt{
		MessageID: env.MessageID,
	}
	// Uplink Reports have a dedicated recovery module that resolves the parent
	// endpoint independently from TaskID and submits only NotificationRef.
	if env.Kind == "uplink-report" {
		ra.Skipped = true
		return ra
	}

	markerPath := RecoveryMarkerPath(receiverHome, env.MessageID)
	if _, err := os.Stat(markerPath); err == nil {
		ra.Skipped = true
		return ra
	}

	store := NewStore(receiverHome)
	if store.IsAcked(env.SenderIdentity, env.MessageID) {
		ra.AlreadyAck = true
		os.WriteFile(markerPath, []byte("acked\n"), 0644)
		return ra
	}

	taskID := env.TaskID
	meta, err := home.ReadMeta(receiverHome, taskID)
	if err != nil {
		ra.Err = fmt.Errorf("reading meta for recovery: %w", err)
		return ra
	}

	alive, err := sender.Alive(receiverHome, meta)
	if err != nil {
		ra.Err = fmt.Errorf("resolving bound sender for recovery: %w", err)
		return ra
	}
	if !alive {
		ra.Err = fmt.Errorf("endpoint not alive for recovery")
		return ra
	}

	sent := sender.Send(receiverHome, meta, env.Payload)
	if sent.Err != nil {
		ra.Err = fmt.Errorf("recovery send failed: %w", sent.Err)
		return ra
	}
	if !sent.Acknowledged {
		ra.Err = fmt.Errorf("recovery send status %s: %s", sent.Status, sent.Detail)
		return ra
	}

	ra.Delivered = true

	markerContent := fmt.Sprintf("recovered_at=%d\nmessage_id=%s\n",
		time.Now().UnixNano(), env.MessageID)
	os.WriteFile(markerPath, []byte(markerContent), 0644)

	return ra
}

// RecoverAllInboxes scans the given home for all pending inbox envelopes
// from all senders and attempts recovery delivery for each.
// This is the entry point for watcher startup recovery.
func RecoverAllInboxesWithSender(sender BoundSender, receiverHome string) ([]*RecoveryAttempt, error) {
	return recoverAllInboxesWithFS(sender, receiverHome, os.Stat, os.ReadDir)
}

func recoverAllInboxesWithFS(sender BoundSender, receiverHome string, stat func(string) (os.FileInfo, error), readDir func(string) ([]os.DirEntry, error)) ([]*RecoveryAttempt, error) {
	// Recovery validates state and inbox roots, then stops at state: the home
	// itself is internal/home's contract to validate, where the guard at
	// internal/home/canonical.go:346 is still gated on os.IsNotExist (#707's
	// own predicate). os.Stat follows symlinks, so dangling links at either
	// checked root are treated as genuine absence.
	stateRoot := filepath.Join(receiverHome, "state")
	stateInfo, err := stat(stateRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading inbox root: %w", err)
	}
	if !stateInfo.IsDir() {
		return nil, fmt.Errorf("reading inbox root: %s is not a directory", stateRoot)
	}

	inboxRoot := filepath.Join(stateRoot, InboxDir)
	info, err := stat(inboxRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading inbox root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("reading inbox root: %s is not a directory", inboxRoot)
	}

	entries, err := readDir(inboxRoot)
	if err != nil {
		return nil, fmt.Errorf("reading inbox root: %w", err)
	}

	var attempts []*RecoveryAttempt
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		senderIdentity := entry.Name()
		envs, listErr := NewStore(receiverHome).ListInbox(senderIdentity)
		if listErr != nil || len(envs) == 0 {
			continue
		}
		for _, env := range envs {
			attempt := RecoverInboxWithSender(sender, receiverHome, env)
			attempts = append(attempts, attempt)
		}
	}
	return attempts, nil
}
