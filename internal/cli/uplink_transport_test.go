package cli

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/afk"
	"github.com/minhtri2710/munsu/internal/session"
)

type uplinkPromptBackend struct{ result session.PromptResult }

func (b uplinkPromptBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b uplinkPromptBackend) SendKeys(string, string) error            { return nil }
func (b uplinkPromptBackend) Capture(string, int) (string, error)      { return "❯ \n", nil }
func (b uplinkPromptBackend) Alive(string) bool                        { return true }
func (b uplinkPromptBackend) Teardown(string) error                    { return nil }
func (b uplinkPromptBackend) AgentPrompt(string, string) session.PromptResult {
	return b.result
}

func TestSessionUplinkTransportMapsTypedPromptOutcomes(t *testing.T) {
	for _, status := range []session.PromptStatus{session.PromptSubmitted, session.PromptQueuedWhileBusy, session.PromptStalled, session.PromptEndpointDead, session.PromptBackendFailed} {
		transport := sessionUplinkTransport{resolve: func(string, string) (session.Backend, string, error) {
			return uplinkPromptBackend{result: session.PromptResult{Status: status}}, "tmux", nil
		}}
		got := transport.Notify("home", afk.TargetResult{Source: afk.RuntimeSource, Handle: "pane"}, "payload")
		wantAck := status == session.PromptSubmitted || status == session.PromptQueuedWhileBusy
		if got.Acknowledged != wantAck || got.Queued == wantAck {
			t.Fatalf("status %s: %+v", status, got)
		}
	}
}

func TestSessionUplinkTransportQueuesResolutionFailure(t *testing.T) {
	transport := sessionUplinkTransport{resolve: func(string, string) (session.Backend, string, error) {
		return nil, "", errors.New("unavailable")
	}}
	got := transport.Notify("home", afk.TargetResult{Source: afk.RuntimeSource, Handle: "pane"}, "payload")
	if !got.Queued || got.Acknowledged {
		t.Fatalf("result = %+v", got)
	}
}
