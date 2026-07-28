package cli

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

type uplinkPromptBackend struct{ result backend.PromptResult }

func (b uplinkPromptBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b uplinkPromptBackend) SendKeys(string, string) error            { return nil }
func (b uplinkPromptBackend) Capture(string, int) (string, error)      { return "❯ \n", nil }
func (b uplinkPromptBackend) Alive(string) bool                        { return true }
func (b uplinkPromptBackend) Teardown(string) error                    { return nil }
func (b uplinkPromptBackend) AgentPrompt(string, string) backend.PromptResult {
	return b.result
}

func TestSessionUplinkTransportMapsTypedPromptOutcomes(t *testing.T) {
	for _, status := range []backend.PromptStatus{backend.PromptSubmitted, backend.PromptQueuedWhileBusy, backend.PromptStalled, backend.PromptEndpointDead, backend.PromptBackendFailed} {
		transport := sessionUplinkTransport{resolve: func(string, string) (backend.Backend, string, error) {
			return uplinkPromptBackend{result: backend.PromptResult{Status: status}}, "tmux", nil
		}}
		got := transport.Notify("home", orchestrator.TargetResult{Source: orchestrator.RuntimeSource, Handle: "pane"}, "payload")
		wantAck := status == backend.PromptSubmitted || status == backend.PromptQueuedWhileBusy
		if got.Acknowledged != wantAck || got.Queued == wantAck {
			t.Fatalf("status %s: %+v", status, got)
		}
	}
}

func TestSessionUplinkTransportQueuesResolutionFailure(t *testing.T) {
	transport := sessionUplinkTransport{resolve: func(string, string) (backend.Backend, string, error) {
		return nil, "", errors.New("unavailable")
	}}
	got := transport.Notify("home", orchestrator.TargetResult{Source: orchestrator.RuntimeSource, Handle: "pane"}, "payload")
	if !got.Queued || got.Acknowledged {
		t.Fatalf("result = %+v", got)
	}
}
