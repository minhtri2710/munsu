package cli

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

type activationPromptBackend struct {
	capture     string
	err         error
	aware       bool
	status      string
	result      backend.PromptResult
	promptCalls int
}

func (b activationPromptBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b activationPromptBackend) SendKeys(string, string) error            { return nil }
func (b activationPromptBackend) Capture(string, int) (string, error)      { return b.capture, b.err }
func (b activationPromptBackend) Alive(string) bool                        { return true }
func (b activationPromptBackend) Teardown(string) error                    { return nil }
func (b activationPromptBackend) CheckAgentAlive(string) (bool, bool, error) {
	return true, b.aware, nil
}
func (b *activationPromptBackend) AgentPrompt(string, string) backend.PromptResult {
	b.promptCalls++
	return b.result
}
func (b *activationPromptBackend) IsRecognizedAgent(string) (bool, string) {
	return b.aware, b.status
}

func TestActivationComposerSafeRecognizesIdleGlyphsAndGhostText(t *testing.T) {
	for _, content := range []string{"", "❯ \n", "› \n", "> \n", "o \n", "\x1b[1m> \x1b[0m\x1b[2mType a message...\x1b[0m\n"} {
		if !activationComposerSafe(activationPromptBackend{capture: content}, "pane") {
			t.Errorf("content %q should be safe", content)
		}
	}
}

func TestActivationComposerSafeRejectsBusyTypedAndCaptureFailure(t *testing.T) {
	for _, tt := range []struct {
		name string
		back activationPromptBackend
	}{
		{"busy", activationPromptBackend{capture: "Working...\n"}},
		{"typed", activationPromptBackend{capture: "\x1b[1m> \x1b[0mactual command\n"}},
		{"capture failure", activationPromptBackend{err: errors.New("capture failed")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if activationComposerSafe(tt.back, "pane") {
				t.Fatal("expected unsafe")
			}
		})
	}
}

func TestSessionActivationTransportMapsTypedOutcomes(t *testing.T) {
	for _, status := range []backend.PromptStatus{
		backend.PromptSubmitted, backend.PromptQueuedWhileBusy, backend.PromptStalled,
		backend.PromptEndpointDead, backend.PromptBackendFailed, backend.PromptUnsupported,
	} {
		bk := &activationPromptBackend{capture: "❯\n", aware: true, status: "idle", result: backend.PromptResult{Status: status}}
		transport := sessionActivationTransport{resolve: func(string, string) (backend.Backend, string, error) {
			return bk, "tmux", nil
		}}
		got := transport.Attempt("home", orchestrator.TargetResult{Handle: "pane"}, "payload")
		want := status == backend.PromptSubmitted || status == backend.PromptQueuedWhileBusy
		if got.Acknowledged != want || got.SubmitStatus != string(status) {
			t.Fatalf("status %s: %+v", status, got)
		}
	}
}

func TestSessionActivationTransportDefersWorkingAgent(t *testing.T) {
	bk := &activationPromptBackend{
		capture: "\x1b[1m> \x1b[0m\x1b[2mType a message...\x1b[0m\n",
		aware:   true,
		status:  "working",
		result:  backend.PromptResult{Status: backend.PromptSubmitted},
	}
	transport := sessionActivationTransport{resolve: func(string, string) (backend.Backend, string, error) {
		return bk, "herdr", nil
	}}
	got := transport.Attempt("home", orchestrator.TargetResult{Handle: "pane"}, "payload")
	if got.Acknowledged || bk.promptCalls != 0 {
		t.Fatalf("working agent should defer without submission: result=%+v calls=%d", got, bk.promptCalls)
	}
}

func TestSessionActivationTransportUsesRecognizedAgentOverride(t *testing.T) {
	bk := &activationPromptBackend{
		capture: "\x1b[1m> \x1b[0m\x1b[2mType a message...\x1b[0m\n",
		aware:   true,
		status:  "idle",
		result:  backend.PromptResult{Status: backend.PromptSubmitted},
	}
	transport := sessionActivationTransport{resolve: func(string, string) (backend.Backend, string, error) {
		return bk, "tmux", nil
	}}
	got := transport.Attempt("home", orchestrator.TargetResult{Handle: "pane"}, "payload")
	if !got.Acknowledged || got.SafetyVerdict != "empty" {
		t.Fatalf("recognized-agent result = %+v", got)
	}
}

func TestSessionActivationTransportQueuesResolutionFailure(t *testing.T) {
	transport := sessionActivationTransport{resolve: func(string, string) (backend.Backend, string, error) {
		return nil, "", errors.New("unavailable")
	}}
	got := transport.Attempt("home", orchestrator.TargetResult{Handle: "pane"}, "payload")
	if got.Acknowledged || got.SafetyError == "" {
		t.Fatalf("resolution result = %+v", got)
	}
}
