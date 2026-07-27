package mailbox

import (
	"os"
	"path/filepath"
	"testing"
)

type deliverySender struct {
	alive    bool
	result   BoundSendResult
	payloads []string
}

func (s *deliverySender) Alive(string, map[string]string) (bool, error) { return s.alive, nil }
func (s *deliverySender) Send(_ string, _ map[string]string, payload string) BoundSendResult {
	s.payloads = append(s.payloads, payload)
	return s.result
}

func TestDeliverEnvelopeWithMetaAndSenderUsesExplicitCapability(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state", "task-1.meta"), []byte("backend=tmux\nwindow=pane-1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sender := &deliverySender{alive: true, result: BoundSendResult{Status: "submitted", Acknowledged: true}}
	env := &Envelope{MessageID: "message-1", Payload: "payload"}
	result := DeliverEnvelopeWithMetaAndSender(sender, home, "task-1", "captain", env)
	if result.Err != nil || !result.PromptSent {
		t.Fatalf("delivery = %+v", result)
	}
	if len(sender.payloads) != 1 || sender.payloads[0] != "payload" {
		t.Fatalf("explicit sender payloads = %v", sender.payloads)
	}
}

func TestSendReportWithSenderPersistsBeforeDelivery(t *testing.T) {
	receiverHome, senderHome := t.TempDir(), t.TempDir()
	sender := &deliverySender{alive: true, result: BoundSendResult{Status: "submitted", Acknowledged: true}}
	env := &Envelope{
		MessageID:      "message-2",
		TaskID:         "task-2",
		Kind:           "command",
		SenderIdentity: "general",
		SenderRank:     RankGeneral,
		ReceiverRank:   RankCaptain,
		ReceiverID:     "captain-1",
		Payload:        "deliver",
		PayloadHash:    PayloadHashHex("deliver"),
		CreatedAt:      1,
	}
	result := SendReportWithSender(sender, env, receiverHome, senderHome, map[string]string{"backend": "tmux", "window": "pane-2"})
	if result.Err != nil || !result.PromptSent {
		t.Fatalf("send report = %+v", result)
	}
	if inbox, err := NewStore(receiverHome).ReadEnvelope("general", "message-2"); err != nil || inbox.Payload != "deliver" {
		t.Fatalf("inbox = %+v, %v", inbox, err)
	}
	if pending, err := NewStore(senderHome).ReadPending("general", "message-2"); err != nil || pending.Payload != "deliver" {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
}
