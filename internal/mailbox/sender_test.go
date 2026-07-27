package mailbox

import (
	"errors"
	"github.com/minhtri2710/munsu/internal/session"
	"testing"
)

type promptBackend struct{ result session.PromptResult }

func (b promptBackend) NewWindow(string, string) (string, error)        { return "", nil }
func (b promptBackend) SendKeys(string, string) error                   { return nil }
func (b promptBackend) Capture(string, int) (string, error)             { return "", nil }
func (b promptBackend) Alive(string) bool                               { return true }
func (b promptBackend) Teardown(string) error                           { return nil }
func (b promptBackend) AgentPrompt(string, string) session.PromptResult { return b.result }
func TestSessionBoundSenderPreservesPromptResult(t *testing.T) {
	for _, status := range []session.PromptStatus{session.PromptSubmitted, session.PromptQueuedWhileBusy, session.PromptStalled, session.PromptUnsupported, session.PromptEndpointDead, session.PromptBackendFailed} {
		resolve := func(string, map[string]string) (session.Backend, string, error) {
			return promptBackend{result: session.PromptResult{Status: status, Detail: "detail", Err: errors.New("typed")}}, "tmux", nil
		}
		got := sessionBoundSender{resolve: resolve}.Send("/home", map[string]string{"backend": "tmux", "window": "p"}, "x")
		if got.Status != string(status) || got.Detail != "detail" || got.Err == nil {
			t.Fatalf("status=%+v", got)
		}
	}
}
func TestSessionBoundSenderRejectsMismatch(t *testing.T) {
	resolve := func(string, map[string]string) (session.Backend, string, error) { return promptBackend{}, "herdr", nil }
	got := sessionBoundSender{resolve: resolve}.Send("/home", map[string]string{"backend": "tmux", "window": "p"}, "x")
	if got.Err == nil {
		t.Fatal("expected mismatch")
	}
}
