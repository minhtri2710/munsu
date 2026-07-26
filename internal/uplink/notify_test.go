package uplink

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/afk"
	"github.com/minhtri2710/munsu/internal/mailbox"
	"github.com/minhtri2710/munsu/internal/session"
)

type notifyBackend struct{ submitted string }

func (*notifyBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b *notifyBackend) SendKeys(_ string, text string) error   { b.submitted = text; return nil }
func (*notifyBackend) Capture(string, int) (string, error)      { return "❯ \n", nil }
func (*notifyBackend) Alive(string) bool                        { return true }
func (*notifyBackend) Teardown(string) error                    { return nil }
func (b *notifyBackend) AgentPrompt(_ string, text string) session.PromptResult {
	b.submitted = text
	return session.PromptResult{Status: session.PromptSubmitted}
}

func TestNotifyParentWithAdaptersSubmitsOnlyNotificationRef(t *testing.T) {
	ref := mailbox.NotificationRef{MessageID: "message-one", SenderIdentity: "soldier-one"}
	backend := &notifyBackend{}
	var receiverSeen string
	result := NotifyParentWithAdapters("sender", "receiver", ref,
		func(receiver string, got mailbox.NotificationRef) (afk.TargetResult, error) {
			receiverSeen = receiver
			return afk.TargetResult{Source: afk.RuntimeSource, Handle: "fleet:p9"}, nil
		},
		func(sender string) (session.Backend, error) { return backend, nil },
	)
	if !result.Acknowledged {
		t.Fatal("notification should be acknowledged")
	}
	if receiverSeen != "receiver" {
		t.Fatalf("receiver=%q", receiverSeen)
	}
	if backend.submitted != ref.Encode() {
		t.Fatalf("submitted=%q want=%q", backend.submitted, ref.Encode())
	}
	if backend.submitted == "done: raw payload [task=task:with/slash]" {
		t.Fatal("raw payload submitted")
	}
}
