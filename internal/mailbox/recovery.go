package mailbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/task"
)

// RecoveryAttempt describes one recovery retry attempt.
type RecoveryAttempt struct {
	MessageID  string
	Delivered  bool // true if delivery succeeded
	AlreadyAck bool // true if already acknowledged (no retry needed)
	Skipped    bool // true if skipped (already handled)
	Err        error
}

// RecoveryFingerprint returns a deterministic string for a recovery attempt.
// Used to deduplicate recovery attempts (one-shot retry).
func RecoveryFingerprint(receiverHome, senderIdentity, messageID string) string {
	return fmt.Sprintf("recover-%s-%s-%s", filepath.Base(receiverHome), senderIdentity, messageID)
}

// RecoveryMarkerPath returns the path for a recovery fingerprint marker file.
func RecoveryMarkerPath(receiverHome, messageID string) string {
	safeID := strings.NewReplacer("/", "_", ":", "_", ".", "_").Replace(messageID)
	return filepath.Join(receiverHome, "state", ".recovered-"+safeID)
}

// RecoverInbox attempts one recovery delivery for a single pending envelope.
// It checks if the envelope was already processed (acked) and skips if so.
// It writes a fingerprint marker on completion to prevent repeated retries.
//
// This is a one-shot retry: it runs once per envelope per process lifetime
// (protected by the fingerprint marker). Bounded backoff is applied via the
// marker — subsequent watcher restarts will see the marker and skip.
func RecoverInbox(receiverHome string, env *Envelope) *RecoveryAttempt {
	ra := &RecoveryAttempt{
		MessageID: env.MessageID,
	}

	// Check fingerprint marker — already attempted recovery.
	markerPath := RecoveryMarkerPath(receiverHome, env.MessageID)
	if _, err := os.Stat(markerPath); err == nil {
		ra.Skipped = true
		return ra
	}

	// Check if already acked (concurrent processing or prior success).
	if env.DeliveryStatus == StatusAcked || IsAcked(receiverHome, env.SenderIdentity, env.MessageID) {
		ra.AlreadyAck = true
		// Write marker to prevent future retry.
		os.WriteFile(markerPath, []byte("acked\n"), 0644)
		return ra
	}

	senderIdentity := env.SenderIdentity
	taskID := env.TaskID

	// Resolve the receiver's backend.
	meta, err := task.ReadMeta(receiverHome, taskID)
	if err != nil {
		ra.Err = fmt.Errorf("reading meta for recovery: %w", err)
		return ra
	}

	bk, _, err := backendForTask(receiverHome, meta)
	if err != nil {
		ra.Err = fmt.Errorf("resolving backend for recovery: %w", err)
		return ra
	}

	windowID := meta["window"]
	if windowID == "" {
		ra.Err = fmt.Errorf("no window in meta for recovery")
		return ra
	}

	// Check pane liveness.
	if !bk.Alive(windowID) {
		ra.Err = fmt.Errorf("endpoint not alive for recovery")
		return ra
	}

	// Send the payload via direct session.SubmitPrompt.
	if err := bk.SendKeys(windowID, env.Payload); err != nil {
		ra.Err = fmt.Errorf("recovery send failed: %w", err)
		return ra
	}

	// Mark delivered.
	if err := MarkInboxDelivered(receiverHome, senderIdentity, env.MessageID); err != nil {
		ra.Err = fmt.Errorf("recovery mark delivered: %w", err)
		ra.Delivered = true // prompt was sent
		return ra
	}

	ra.Delivered = true

	// Write fingerprint marker to prevent further retries.
	markerContent := fmt.Sprintf("recovered_at=%d\nmessage_id=%s\n",
		time.Now().UnixNano(), env.MessageID)
	os.WriteFile(markerPath, []byte(markerContent), 0644)

	return ra
}

// RecoverAllInboxes scans the given home for all pending inbox envelopes
// from all senders and attempts recovery delivery for each.
// This is the entry point for watcher startup recovery.
func RecoverAllInboxes(receiverHome string) ([]*RecoveryAttempt, error) {
	inboxRoot := filepath.Join(receiverHome, "state", InboxDir)

	entries, err := os.ReadDir(inboxRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading inbox root: %w", err)
	}

	var attempts []*RecoveryAttempt
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		senderIdentity := entry.Name()
		envs, listErr := ListPendingInbox(receiverHome, senderIdentity)
		if listErr != nil || len(envs) == 0 {
			continue
		}
		for _, env := range envs {
			attempt := RecoverInbox(receiverHome, env)
			attempts = append(attempts, attempt)
		}
	}

	return attempts, nil
}

// CleanRecoveryMarkers removes old recovery fingerprint markers.
// This is a housekeeping function, not required for correctness.
func CleanRecoveryMarkers(receiverHome string, maxAge time.Duration) error {
	stateDir := filepath.Join(receiverHome, "state")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return nil
	}
	now := time.Now()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, ".recovered-") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(fi.ModTime()) > maxAge {
			os.Remove(filepath.Join(stateDir, name))
		}
	}
	return nil
}
