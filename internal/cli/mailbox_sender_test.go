package cli

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
)

type mailboxPromptBackend struct {
	result      backend.PromptResult
	recognized  bool
	status      string
	promptCalls int
}

func (b mailboxPromptBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b mailboxPromptBackend) SendKeys(string, string) error            { return nil }
func (b mailboxPromptBackend) Capture(string, int) (string, error)      { return "", nil }
func (b mailboxPromptBackend) Alive(string) bool                        { return true }
func (b mailboxPromptBackend) Teardown(string) error                    { return nil }
func (b *mailboxPromptBackend) AgentPrompt(string, string) backend.PromptResult {
	b.promptCalls++
	return b.result
}
func (b *mailboxPromptBackend) IsRecognizedAgent(string) (bool, string) {
	return b.recognized, b.status
}

func TestSessionMailboxSenderAliveRequiresLiveAgent(t *testing.T) {
	sender := sessionMailboxSender{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return &captainProbeBackend{alive: true, paneAlive: true, agentAlive: false}, "herdr", nil
	}}

	alive, err := sender.Alive("/home", map[string]string{"backend": "herdr", "window": "session:pane", "herdr_session": "session"})
	if err != nil {
		t.Fatal(err)
	}
	if alive {
		t.Fatal("sender must report unavailable when the pane exists but the agent is dead")
	}
}

func TestSessionMailboxSenderDefersWorkingAgent(t *testing.T) {
	bk := &mailboxPromptBackend{recognized: true, status: "working", result: backend.PromptResult{Status: backend.PromptSubmitted}}
	sender := sessionMailboxSender{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return bk, "herdr", nil
	}}
	got := sender.Send("/home", map[string]string{"backend": "herdr", "window": "pane"}, "payload")
	if got.Status != "deferred" || got.Acknowledged || bk.promptCalls != 0 {
		t.Fatalf("working send = %+v calls=%d", got, bk.promptCalls)
	}
}

func TestSessionMailboxSenderPreservesTypedPromptResult(t *testing.T) {
	for _, status := range []backend.PromptStatus{backend.PromptSubmitted, backend.PromptQueuedWhileBusy, backend.PromptStalled, backend.PromptUnsupported, backend.PromptEndpointDead, backend.PromptBackendFailed} {
		bk := &mailboxPromptBackend{recognized: true, status: "idle", result: backend.PromptResult{Status: status, Detail: "detail", Err: errors.New("typed")}}
		sender := sessionMailboxSender{resolve: func(string, map[string]string) (backend.Backend, string, error) {
			return bk, "tmux", nil
		}}
		got := sender.Send("/home", map[string]string{"backend": "tmux", "window": "pane"}, "payload")
		if got.Status != string(status) || got.Detail != "detail" || got.Err == nil {
			t.Fatalf("status %s: %+v", status, got)
		}
	}
}

func TestSessionMailboxSenderRejectsBoundBackendMismatch(t *testing.T) {
	sender := sessionMailboxSender{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return mailboxPromptBackend{}, "herdr", nil
	}}
	got := sender.Send("/home", map[string]string{"backend": "tmux", "window": "pane"}, "payload")
	if got.Err == nil {
		t.Fatal("expected bound backend mismatch")
	}
}

func TestSessionMailboxSenderRejectsHerdrOwnershipMismatch(t *testing.T) {
	sender := sessionMailboxSender{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return mailboxPromptBackend{}, "herdr", nil
	}}
	got := sender.Send("/home", map[string]string{"backend": "herdr", "window": "actual:tab", "herdr_session": "expected"}, "payload")
	if got.Err == nil {
		t.Fatal("expected Herdr ownership mismatch")
	}
}
