package cli

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/session"
)

type mailboxPromptBackend struct{ result session.PromptResult }

func (b mailboxPromptBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b mailboxPromptBackend) SendKeys(string, string) error            { return nil }
func (b mailboxPromptBackend) Capture(string, int) (string, error)      { return "", nil }
func (b mailboxPromptBackend) Alive(string) bool                        { return true }
func (b mailboxPromptBackend) Teardown(string) error                    { return nil }
func (b mailboxPromptBackend) AgentPrompt(string, string) session.PromptResult {
	return b.result
}

func TestSessionMailboxSenderPreservesTypedPromptResult(t *testing.T) {
	for _, status := range []session.PromptStatus{session.PromptSubmitted, session.PromptQueuedWhileBusy, session.PromptStalled, session.PromptUnsupported, session.PromptEndpointDead, session.PromptBackendFailed} {
		backend := mailboxPromptBackend{result: session.PromptResult{Status: status, Detail: "detail", Err: errors.New("typed")}}
		sender := sessionMailboxSender{resolve: func(string, map[string]string) (session.Backend, string, error) {
			return backend, "tmux", nil
		}}
		got := sender.Send("/home", map[string]string{"backend": "tmux", "window": "pane"}, "payload")
		if got.Status != string(status) || got.Detail != "detail" || got.Err == nil {
			t.Fatalf("status %s: %+v", status, got)
		}
	}
}

func TestSessionMailboxSenderRejectsBoundBackendMismatch(t *testing.T) {
	sender := sessionMailboxSender{resolve: func(string, map[string]string) (session.Backend, string, error) {
		return mailboxPromptBackend{}, "herdr", nil
	}}
	got := sender.Send("/home", map[string]string{"backend": "tmux", "window": "pane"}, "payload")
	if got.Err == nil {
		t.Fatal("expected bound backend mismatch")
	}
}

func TestSessionMailboxSenderRejectsHerdrOwnershipMismatch(t *testing.T) {
	sender := sessionMailboxSender{resolve: func(string, map[string]string) (session.Backend, string, error) {
		return mailboxPromptBackend{}, "herdr", nil
	}}
	got := sender.Send("/home", map[string]string{"backend": "herdr", "window": "actual:tab", "herdr_session": "expected"}, "payload")
	if got.Err == nil {
		t.Fatal("expected Herdr ownership mismatch")
	}
}
