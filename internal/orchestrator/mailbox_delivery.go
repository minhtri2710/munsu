package orchestrator

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/home"
)

// DeliveryResult describes the outcome of trying to deliver an envelope.
type DeliveryResult struct {
	MessageID    string
	PromptSent   bool // true if Session.SubmitPrompt (SendKeys) succeeded
	ProcessAcked bool // true if receiver has acked the envelope
	Err          error
}

// DeliverEnvelopeWithSender sends an envelope's payload through the supplied
// task-bound sender capability. It does not mutate the envelope; ack
// tracking is handled through separate ProcessingAck records.
//
// The sender must have already written the envelope to the receiver's inbox
// and saved their own pending record.
func DeliverEnvelopeWithSender(sender BoundSender, receiverHome, senderIdentity string, env *Envelope, meta map[string]string) *DeliveryResult {
	result := &DeliveryResult{
		MessageID: env.MessageID,
	}

	alive, err := sender.Alive(receiverHome, meta)
	if err != nil {
		result.Err = fmt.Errorf("resolve bound sender: %w", err)
		return result
	}
	if !alive {
		result.Err = fmt.Errorf("endpoint not alive")
		return result
	}

	sent := sender.Send(receiverHome, meta, env.Payload)
	if sent.Err != nil {
		result.Err = fmt.Errorf("send: %w", sent.Err)
		return result
	}
	if !sent.Acknowledged {
		result.Err = fmt.Errorf("send status %s: %s", sent.Status, sent.Detail)
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
func DeliverEnvelopeWithMetaAndSender(sender BoundSender, homeDir, taskID, senderIdentity string, env *Envelope) *DeliveryResult {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return &DeliveryResult{
			MessageID: env.MessageID,
			Err:       fmt.Errorf("reading meta: %w", err),
		}
	}
	return DeliverEnvelopeWithSender(sender, homeDir, senderIdentity, env, meta)
}

// SendReport is the top-level helper for the happy path:
//  1. Create the rank-validated envelope in the receiver's inbox
//  2. Save the sender's pending record
//  3. Deliver directly (no watcher)
//  4. Return the delivery result
//
// The caller retains the sender's pending record until exact ack.
// Use AwaitedSendReport for the complete send-and-wait-for-ack flow.
func SendReportWithSender(sender BoundSender, env *Envelope, receiverHome, senderHome string, meta map[string]string) *DeliveryResult {
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

	return DeliverEnvelopeWithSender(sender, receiverHome, env.SenderIdentity, env, meta)
}

// AwaitedSendReport extends SendReport by also checking for the receiver's
// exact ack after successful prompt submission.
func AwaitedSendReportWithSender(sender BoundSender, env *Envelope, receiverHome, senderHome string, meta map[string]string) *DeliveryResult {
	result := SendReportWithSender(sender, env, receiverHome, senderHome, meta)
	if result.Err != nil {
		return result
	}
	result.ProcessAcked = NewStore(receiverHome).IsAcked(env.SenderIdentity, env.MessageID)
	return result
}
