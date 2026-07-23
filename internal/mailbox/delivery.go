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
	ProcessAcked bool // true if receiver has acked the envelope
	Err          error
}

// DeliverEnvelope sends an envelope's payload to the receiver via direct
// session.SubmitPrompt (SendKeys). It does not mutate the envelope; ack
// tracking is handled through separate ProcessingAck records.
//
// The sender must have already written the envelope to the receiver's inbox
// and saved their own pending record.
func DeliverEnvelope(receiverHome, senderIdentity string, env *Envelope, meta map[string]string) *DeliveryResult {
	result := &DeliveryResult{
		MessageID: env.MessageID,
	}

	bk, _, err := backendForTask(receiverHome, meta)
	if err != nil {
		result.Err = fmt.Errorf("resolve backend: %w", err)
		return result
	}

	windowID := meta["window"]
	if windowID == "" {
		result.Err = fmt.Errorf("no window in meta")
		return result
	}

	if !bk.Alive(windowID) {
		result.Err = fmt.Errorf("endpoint not alive")
		return result
	}

	if err := bk.SendKeys(windowID, env.Payload); err != nil {
		result.Err = fmt.Errorf("send: %w", err)
		return result
	}

	result.PromptSent = true

	// Check if receiver already acked (e.g., during prior turn).
	if NewStore(receiverHome).IsAcked(senderIdentity, env.MessageID) {
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
	if err := NewStore(receiverHome).WriteEnvelope(env); err != nil {
		return &DeliveryResult{
			MessageID: env.MessageID,
			Err:       fmt.Errorf("writing inbox: %w", err),
		}
	}

	if err := NewStore(senderHome).WritePending(env); err != nil {
		return &DeliveryResult{
			MessageID: env.MessageID,
			Err:       fmt.Errorf("saving pending: %w", err),
		}
	}

	return DeliverEnvelope(receiverHome, env.SenderIdentity, env, meta)
}

// AwaitedSendReport extends SendReport by also checking for the receiver's
// exact ack after successful prompt submission.
func AwaitedSendReport(env *Envelope, receiverHome, senderHome string, meta map[string]string) *DeliveryResult {
	result := SendReport(env, receiverHome, senderHome, meta)
	if result.Err != nil {
		return result
	}
	result.ProcessAcked = NewStore(receiverHome).IsAcked(env.SenderIdentity, env.MessageID)
	return result
}
