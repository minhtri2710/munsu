package captain

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/mailbox"
	"github.com/minhtri2710/munsu/internal/marker"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

// SendMailboxResult describes the outcome of a General→Captain mailbox send.
type SendMailboxResult struct {
	MessageID      string
	Acknowledged   bool // true if SubmitPrompt was acknowledged
	Err            error
}

// SendMailboxToCaptain sends a line to a captain via the mailbox Store/Receiver system.
//
// Steps:
//  1. Validates task meta fully: kind=captain, sm_id matches, canonical home, window/backend
//  2. Derives General sender identity/rank from durable parent home provenance
//  3. Creates a mailbox Envelope (General→Captain) with marker.MarkFromGeneral(line) as payload
//  4. Writes envelope to the captain's inbox (receiver-owned)
//  5. Writes pending record in the General's outbox (sender-identity scoped)
//  6. Sends canonical NotificationRef via session.SubmitPrompt (NOT the raw line)
//  7. Returns result — pending is NOT removed on acknowledgment; it is removed only after
//     exact ack reconciles via converge.
//
// Preserves command marker semantics: the marker is in the envelope payload, not in the
// notification text. The notification is a structured NotificationRef JSON.
func SendMailboxToCaptain(sm Info, parentHome, line string) *SendMailboxResult {
	result := &SendMailboxResult{}

	// 1. Validate task meta fully.
	taskID := taskIDForCaptain(sm.ID)
	meta, err := task.ReadMeta(parentHome, taskID)
	if err != nil {
		result.Err = fmt.Errorf("reading meta for %s: %w", sm.ID, err)
		return result
	}
	if meta["kind"] != "captain" {
		result.Err = fmt.Errorf("task %s has kind=%q, expected captain", sm.ID, meta["kind"])
		return result
	}
	if meta["sm_id"] != sm.ID {
		result.Err = fmt.Errorf("meta sm_id=%q does not match registry id %q", meta["sm_id"], sm.ID)
		return result
	}
	canonSM, err := canonicalHome(sm.Home)
	if err != nil {
		result.Err = fmt.Errorf("cannot canonicalize captain home: %w", err)
		return result
	}
	if meta["home"] != canonSM {
		result.Err = fmt.Errorf("meta home=%q does not match canonical captain home %q", meta["home"], canonSM)
		return result
	}
	if _, err := ValidateProvenance(sm.Home); err != nil {
		result.Err = fmt.Errorf("captain provenance validation failed: %w", err)
		return result
	}

	windowID := meta["window"]
	if windowID == "" {
		result.Err = fmt.Errorf("no window in meta")
		return result
	}

	bk, _, bkErr := backendForTask(parentHome, meta)
	if bkErr != nil {
		result.Err = fmt.Errorf("resolving backend: %w", bkErr)
		return result
	}

	// 2. Derive General sender identity/rank from durable parent home provenance.
	senderIdentity, senderRank, err := mailbox.ReadHomeIdentity(parentHome)
	if err != nil {
		result.Err = fmt.Errorf("deriving sender identity from parent home: %w", err)
		return result
	}

	// 3. Create envelope with marked line as payload.
	// The marker preserves command semantics so the captain agent can distinguish
	// General-routed commands from human chat. The notification ref (not the payload)
	// is what gets sent via SubmitPrompt.
	markedLine := marker.MarkFromGeneral(line)
	env := &mailbox.Envelope{
		SenderRank:     senderRank,
		SenderIdentity: senderIdentity,
		ReceiverRank:   mailbox.RankCaptain,
		ReceiverID:     sm.ID,
		Payload:        markedLine,
	}

	// 4. Write envelope to the captain's inbox.
	receiverStore := mailbox.NewStore(canonSM)
	if err := receiverStore.WriteEnvelope(env); err != nil {
		result.Err = fmt.Errorf("writing inbox envelope: %w", err)
		return result
	}
	result.MessageID = env.MessageID

	// 5. Write pending record in the General's outbox (sender-identity scoped).
	senderStore := mailbox.NewStore(parentHome)
	if err := senderStore.WritePending(env); err != nil {
		result.Err = fmt.Errorf("writing sender pending: %w", err)
		return result
	}

	// 6. Send canonical NotificationRef via SubmitPrompt.
	ref := mailbox.NotificationRef{
		MessageID:      env.MessageID,
		SenderIdentity: senderIdentity,
	}
	notificationText := ref.Encode()

	promptResult := session.SubmitPrompt(bk, windowID, notificationText)
	result.Acknowledged = promptResult.Acknowledged()

	if !result.Acknowledged {
		// Pending is retained — never remove on unacknowledged submit.
		// The caller (munsu send or converge) sees the typed failure.
		result.Err = fmt.Errorf("send not acknowledged (status=%s)", promptResult.Status)
		return result
	}

	return result
}

// ReconcileMailboxPending reconciles sender-side pending records for one captain.
//
// For each pending envelope in the General's outbox (sender-identity scoped) that
// targets this captain:
//   - Check if an ack exists in the captain's inbox
//   - If ack exists and validates via ValidateAck, remove the pending record
//   - If no ack exists, resend the NotificationRef (duplicate notification is idempotent)
//   - If ack exists but validation fails, fail closed (return error)
//
// This is called from converge to clean up pending records after the captain agent
// has processed the notification and written the ack.
func ReconcileMailboxPending(parentHome string, sm Info) error {
	// Derive General sender identity from parent home.
	senderIdentity, _, err := mailbox.ReadHomeIdentity(parentHome)
	if err != nil {
		return fmt.Errorf("%s: deriving sender identity: %w", sm.ID, err)
	}

	// List all pending records for the General sender identity.
	parentStore := mailbox.NewStore(parentHome)
	pending, err := parentStore.ListPending(senderIdentity)
	if err != nil {
		return fmt.Errorf("%s: listing pending: %w", sm.ID, err)
	}
	if len(pending) == 0 {
		return nil
	}

	// Canonicalize captain home for inbox lookup.
	canonSM, err := canonicalHome(sm.Home)
	if err != nil {
		return fmt.Errorf("%s: canonicalizing captain home: %w", sm.ID, err)
	}
	if _, err := ValidateProvenance(sm.Home); err != nil {
		return fmt.Errorf("%s: provenance validation: %w", sm.ID, err)
	}
	captainStore := mailbox.NewStore(canonSM)

	for _, env := range pending {
		// Skip envelopes not targeting this captain.
		if env.ReceiverID != sm.ID {
			continue
		}

		// Check for ack in captain's inbox.
		ack, err := captainStore.ReadAck(senderIdentity, env.MessageID)
		if err != nil {
			return fmt.Errorf("%s: reading ack for %s: %w", sm.ID, env.MessageID, err)
		}
		if ack == nil {
			// No ack yet — captain hasn't processed it.
			// Resend NotificationRef (duplicate notification is idempotent).
			if err := resendNotification(parentHome, sm, env); err != nil {
				return fmt.Errorf("%s: resending notification for %s: %w", sm.ID, env.MessageID, err)
			}
			continue
		}

		// Ack exists — validate and remove pending.
		if err := parentStore.RemovePendingAfterAck(senderIdentity, env.MessageID, ack); err != nil {
			return fmt.Errorf("%s: removing pending for %s: ack validation failed: %w", sm.ID, env.MessageID, err)
		}
	}

	return nil
}

// resendNotification re-sends a NotificationRef for a pending envelope that
// hasn't been acked yet. Duplicate notification is idempotent — the captain's
// Receiver.Process returns the existing ack if already processed.
func resendNotification(parentHome string, sm Info, env *mailbox.Envelope) error {
	taskID := taskIDForCaptain(sm.ID)
	meta, err := task.ReadMeta(parentHome, taskID)
	if err != nil {
		return fmt.Errorf("reading meta: %w", err)
	}

	windowID := meta["window"]
	if windowID == "" {
		return fmt.Errorf("no window in meta")
	}

	bk, _, bkErr := backendForTask(parentHome, meta)
	if bkErr != nil {
		return fmt.Errorf("resolving backend: %w", bkErr)
	}

	ref := mailbox.NotificationRef{
		MessageID:      env.MessageID,
		SenderIdentity: env.SenderIdentity,
	}
	result := session.SubmitPrompt(bk, windowID, ref.Encode())
	if !result.Acknowledged() {
		return fmt.Errorf("resend not acknowledged (status=%s)", result.Status)
	}
	return nil
}
