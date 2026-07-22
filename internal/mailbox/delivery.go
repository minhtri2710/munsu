package mailbox

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

// backendForTask is the session backend resolver, overridable in tests.
var backendForTask = session.BackendForTask

// DeliveryResult describes the outcome of trying to deliver an envelope.
type DeliveryResult struct {
	MessageID    string
	PromptSent   bool // true if Session.SubmitPrompt (SendKeys) succeeded
	ProcessAcked bool // true if receiver has processed the envelope
	Err          error
}

// DeliverEnvelope sends an envelope's payload to the receiver via direct
// session.SubmitPrompt (SendKeys). It updates the inbox envelope status
// on success (→delivered) or failure (→failed with incremented attempts).
//
// The sender must have already written the envelope to the receiver's inbox
// and saved their own pending record.
func DeliverEnvelope(receiverHome, senderIdentity string, env *Envelope, meta map[string]string) *DeliveryResult {
	result := &DeliveryResult{
		MessageID: env.MessageID,
	}

	// Resolve the receiver's session backend from task meta.
	bk, _, err := backendForTask(receiverHome, meta)
	if err != nil {
		result.Err = fmt.Errorf("resolve backend: %w", err)
		MarkInboxFailed(receiverHome, senderIdentity, env.MessageID)
		return result
	}

	windowID := meta["window"]
	if windowID == "" {
		result.Err = fmt.Errorf("no window in meta")
		MarkInboxFailed(receiverHome, senderIdentity, env.MessageID)
		return result
	}

	// Check if the pane is alive.
	if !bk.Alive(windowID) {
		result.Err = fmt.Errorf("endpoint not alive")
		// Don't mark failed — just return the error so the envelope stays pending.
		return result
	}

	// Send the payload directly (acknowledged SubmitPrompt).
	// This uses the same SendKeys mechanism as the existing outbox flush.
	if err := bk.SendKeys(windowID, env.Payload); err != nil {
		result.Err = fmt.Errorf("send: %w", err)
		MarkInboxFailed(receiverHome, senderIdentity, env.MessageID)
		return result
	}

	// Mark delivered in the inbox.
	if err := MarkInboxDelivered(receiverHome, senderIdentity, env.MessageID); err != nil {
		result.Err = fmt.Errorf("mark delivered: %w", err)
		result.PromptSent = true
		return result
	}

	result.PromptSent = true

	// Check if the receiver already processed it (e.g., if the receiver
	// processed during a prior turn and we're in recovery).
	if IsAcked(receiverHome, senderIdentity, env.MessageID) {
		result.ProcessAcked = true
	}

	return result
}

// DeliverEnvelopeWithMeta reads task meta and delivers.
func DeliverEnvelopeWithMeta(homeDir, taskID, senderIdentity string, env *Envelope) *DeliveryResult {
	meta, err := task.ReadMeta(homeDir, taskID)
	if err != nil {
		return &DeliveryResult{
			MessageID: env.MessageID,
			Err:       fmt.Errorf("reading meta: %w", err),
		}
	}
	return DeliverEnvelope(homeDir, senderIdentity, env, meta)
}

// SendReport is the top-level helper for the happy path:
//  1. Create the rank-validated envelope in the receiver's inbox
//  2. Save the sender's pending record
//  3. Deliver directly (no watcher)
//  4. Return the delivery result
//
// The caller retains the sender's pending record until exact ack.
// Use AwaitedSendReport for the complete send-and-wait-for-ack flow.
func SendReport(env *Envelope, receiverHome, senderHome string, meta map[string]string) *DeliveryResult {
	// Step 1: Write to receiver's inbox.
	if err := NewEnvelope(receiverHome, env); err != nil {
		return &DeliveryResult{
			MessageID: env.MessageID,
			Err:       fmt.Errorf("writing inbox: %w", err),
		}
	}

	// Step 2: Save sender pending record.
	if _, err := SaveSenderPending(senderHome, env); err != nil {
		return &DeliveryResult{
			MessageID: env.MessageID,
			Err:       fmt.Errorf("saving pending: %w", err),
		}
	}

	// Step 3: Deliver.
	senderIdentity := env.SenderIdentity
	result := DeliverEnvelope(receiverHome, senderIdentity, env, meta)

	return result
}

// AwaitedSendReport extends SendReport by also waiting for the receiver's
// exact ack after successful prompt submission.
func AwaitedSendReport(env *Envelope, receiverHome, senderHome string, meta map[string]string) *DeliveryResult {
	result := SendReport(env, receiverHome, senderHome, meta)
	if result.Err != nil {
		return result
	}

	// PromptSent is notification success. We need to check for processing ack.
	// The envelope is in delivered state; the receiver will ack when done.
	result.ProcessAcked = IsAcked(receiverHome, env.SenderIdentity, env.MessageID)

	return result
}
