package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/session"
)

type captainProbeBackend struct {
	alive       bool
	paneAlive   bool
	agentAlive  bool
	agentErr    error
	prompt      session.PromptResult
	promptCalls int
}

func (b *captainProbeBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b *captainProbeBackend) SendKeys(string, string) error            { return nil }
func (b *captainProbeBackend) Capture(string, int) (string, error)      { return "", nil }
func (b *captainProbeBackend) Alive(string) bool                        { return b.alive }
func (b *captainProbeBackend) Teardown(string) error                    { return nil }
func (b *captainProbeBackend) CheckAgentAlive(string) (bool, bool, error) {
	return b.paneAlive, b.agentAlive, b.agentErr
}
func (b *captainProbeBackend) AgentPrompt(string, string) session.PromptResult {
	b.promptCalls++
	return b.prompt
}

type ordinaryCaptainProbeBackend struct {
	alive         bool
	aliveResults  []bool
	aliveCalls    int
	sendErr       error
	teardownErr   error
	sendCalls     int
	teardownCalls int
}

func (b *ordinaryCaptainProbeBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b *ordinaryCaptainProbeBackend) SendKeys(string, string) error            { b.sendCalls++; return b.sendErr }
func (b *ordinaryCaptainProbeBackend) Capture(string, int) (string, error)      { return "", nil }
func (b *ordinaryCaptainProbeBackend) Alive(string) bool {
	result := b.alive
	if b.aliveCalls < len(b.aliveResults) {
		result = b.aliveResults[b.aliveCalls]
	}
	b.aliveCalls++
	return result
}
func (b *ordinaryCaptainProbeBackend) Teardown(string) error { b.teardownCalls++; return b.teardownErr }

func TestSessionProbeEndpointResolutionAndOwnership(t *testing.T) {
	backend := &ordinaryCaptainProbeBackend{alive: true}
	tests := []struct {
		name       string
		meta       map[string]string
		resolved   string
		resolveErr error
		want       string
	}{
		{name: "resolution", meta: map[string]string{"window": "pane"}, resolveErr: errors.New("resolve failed"), want: "resolve failed"},
		{name: "backend", meta: map[string]string{"backend": "tmux", "window": "pane"}, resolved: "herdr", want: "backend ownership mismatch"},
		{name: "herdr session", meta: map[string]string{"backend": "herdr", "window": "other:pane", "herdr_session": "owned"}, resolved: "herdr", want: "herdr session ownership mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep := sessionProbeEndpoint{resolve: func(string, map[string]string) (session.Backend, string, error) {
				return backend, tt.resolved, tt.resolveErr
			}}
			_, err := ep.Probe("home", tt.meta)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v, want %q", err, tt.want)
			}
		})
	}
}

func TestSessionProbeEndpointOrdinaryOutcomes(t *testing.T) {
	for _, alive := range []bool{false, true} {
		backend := &ordinaryCaptainProbeBackend{alive: alive}
		ep := sessionProbeEndpoint{resolve: func(string, map[string]string) (session.Backend, string, error) { return backend, "tmux", nil }}
		got, err := ep.Probe("home", map[string]string{"window": "pane"})
		if err != nil {
			t.Fatal(err)
		}
		if got.PaneAlive != alive || got.AgentAlive != alive {
			t.Fatalf("got=%+v, alive=%v", got, alive)
		}
	}
}

func TestSessionNudgeEndpointTypedOutcomes(t *testing.T) {
	statuses := []session.PromptStatus{session.PromptSubmitted, session.PromptQueuedWhileBusy, session.PromptStalled, session.PromptUnsupported, session.PromptEndpointDead, session.PromptUnsafeComposer, session.PromptBackendFailed}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			backend := &captainProbeBackend{paneAlive: true, agentAlive: true, prompt: session.PromptResult{Status: status}}
			resolves := 0
			ep := sessionNudgeEndpoint{resolve: func(string, map[string]string) (session.Backend, string, error) {
				resolves++
				return backend, "herdr", nil
			}}
			got, err := ep.Nudge("home", map[string]string{"window": "owned:pane", "herdr_session": "owned"}, "payload")
			if err != nil {
				t.Fatal(err)
			}
			if resolves != 1 {
				t.Fatalf("resolve calls=%d, want 1", resolves)
			}
			if got.Acknowledged != status.Acknowledged() {
				t.Fatalf("ack=%v status=%s", got.Acknowledged, status)
			}
		})
	}
}

func TestSessionNudgeEndpointUnavailableDoesNotPrompt(t *testing.T) {
	for _, backend := range []*captainProbeBackend{{paneAlive: false}, {paneAlive: true, agentAlive: false}, {agentErr: session.ErrPaneNotFound}} {
		ep := sessionNudgeEndpoint{resolve: func(string, map[string]string) (session.Backend, string, error) { return backend, "herdr", nil }}
		got, err := ep.Nudge("home", map[string]string{"window": "owned:pane", "herdr_session": "owned"}, "payload")
		if err != nil || got.Status != "unavailable" || backend.promptCalls != 0 {
			t.Fatalf("got=%+v err=%v calls=%d", got, err, backend.promptCalls)
		}
	}
}

func TestSessionRetireEndpointOutcomes(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name         string
		backend      *ordinaryCaptainProbeBackend
		resolveErr   error
		meta         map[string]string
		wantErr      bool
		wantSend     int
		wantTeardown int
	}{
		{name: "resolution", backend: &ordinaryCaptainProbeBackend{}, resolveErr: boom, meta: map[string]string{"window": "pane"}, wantErr: true},
		{name: "backend mismatch", backend: &ordinaryCaptainProbeBackend{}, meta: map[string]string{"backend": "tmux", "window": "pane"}, wantErr: true},
		{name: "herdr mismatch", backend: &ordinaryCaptainProbeBackend{}, meta: map[string]string{"backend": "herdr", "window": "other:pane", "herdr_session": "owned"}, wantErr: true},
		{name: "quit failure", backend: &ordinaryCaptainProbeBackend{alive: true, sendErr: boom}, meta: map[string]string{"window": "pane"}, wantErr: true, wantSend: 1},
		{name: "graceful exit", backend: &ordinaryCaptainProbeBackend{aliveResults: []bool{true, false}}, meta: map[string]string{"window": "pane"}, wantSend: 1},
		{name: "teardown fallback", backend: &ordinaryCaptainProbeBackend{aliveResults: []bool{true, true}}, meta: map[string]string{"window": "pane"}, wantSend: 1, wantTeardown: 1},
		{name: "dead teardown failure", backend: &ordinaryCaptainProbeBackend{teardownErr: boom}, meta: map[string]string{"window": "pane"}, wantErr: true, wantTeardown: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep := sessionRetireEndpoint{resolve: func(string, map[string]string) (session.Backend, string, error) {
				return tt.backend, "herdr", tt.resolveErr
			}, sleep: func(time.Duration) {}}
			err := ep.Retire("home", tt.meta)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.backend.sendCalls != tt.wantSend || tt.backend.teardownCalls != tt.wantTeardown {
				t.Fatalf("send=%d teardown=%d", tt.backend.sendCalls, tt.backend.teardownCalls)
			}
		})
	}
}

func TestSessionProbeEndpointAgentAwareOutcomes(t *testing.T) {
	boom := errors.New("protocol failed")
	tests := []struct {
		name      string
		backend   *captainProbeBackend
		wantPane  bool
		wantAgent bool
		wantErr   error
	}{
		{name: "alive", backend: &captainProbeBackend{paneAlive: true, agentAlive: true}, wantPane: true, wantAgent: true},
		{name: "agent absent", backend: &captainProbeBackend{paneAlive: true}, wantPane: true},
		{name: "pane absent", backend: &captainProbeBackend{agentErr: session.ErrPaneNotFound}},
		{name: "other error", backend: &captainProbeBackend{agentErr: boom}, wantErr: boom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep := sessionProbeEndpoint{resolve: func(string, map[string]string) (session.Backend, string, error) { return tt.backend, "herdr", nil }}
			got, err := ep.Probe("home", map[string]string{"window": "owned:pane", "herdr_session": "owned"})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error=%v, want=%v", err, tt.wantErr)
			}
			if got.PaneAlive != tt.wantPane || got.AgentAlive != tt.wantAgent {
				t.Fatalf("got=%+v", got)
			}
		})
	}
}
