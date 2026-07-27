package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/session"
)

type soldierEndpointBackend struct {
	alive      bool
	busy       bool
	busyErr    error
	recognized bool
	status     string
	prompt     session.PromptResult
}

func (b *soldierEndpointBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b *soldierEndpointBackend) SendKeys(string, string) error            { return nil }
func (b *soldierEndpointBackend) Capture(string, int) (string, error)      { return "", nil }
func (b *soldierEndpointBackend) Alive(string) bool                        { return b.alive }
func (b *soldierEndpointBackend) Teardown(string) error                    { return nil }
func (b *soldierEndpointBackend) AgentBusy(string) (bool, error)           { return b.busy, b.busyErr }
func (b *soldierEndpointBackend) IsRecognizedAgent(string) (bool, string) {
	return b.recognized, b.status
}
func (b *soldierEndpointBackend) AgentPrompt(string, string) session.PromptResult { return b.prompt }

type recognizedOnlyBackend struct{ base *soldierEndpointBackend }

func (b recognizedOnlyBackend) NewWindow(a, c string) (string, error)   { return b.base.NewWindow(a, c) }
func (b recognizedOnlyBackend) SendKeys(a, c string) error              { return b.base.SendKeys(a, c) }
func (b recognizedOnlyBackend) Capture(a string, c int) (string, error) { return b.base.Capture(a, c) }
func (b recognizedOnlyBackend) Alive(a string) bool                     { return b.base.Alive(a) }
func (b recognizedOnlyBackend) Teardown(a string) error                 { return b.base.Teardown(a) }
func (b recognizedOnlyBackend) IsRecognizedAgent(a string) (bool, string) {
	return b.base.IsRecognizedAgent(a)
}

func TestSoldierEndpointsAllowsConfiguredBackendWhenMetaBackendMissing(t *testing.T) {
	backend := &soldierEndpointBackend{alive: true}
	endpoint := sessionSoldierEndpoints{resolve: func(string, map[string]string) (session.Backend, string, error) {
		return backend, "tmux", nil
	}}
	if _, err := endpoint.backend("home", map[string]string{"window": "pane"}); err != nil {
		t.Fatal(err)
	}
}

func TestSoldierEndpointsRejectsBackendAndHerdrOwnershipMismatch(t *testing.T) {
	backend := &soldierEndpointBackend{alive: true}
	for _, meta := range []map[string]string{
		{"backend": "tmux", "window": "pane"},
		{"backend": "herdr", "window": "other:pane", "herdr_session": "owned"},
	} {
		endpoint := sessionSoldierEndpoints{resolve: func(string, map[string]string) (session.Backend, string, error) {
			return backend, "herdr", nil
		}}
		if _, err := endpoint.backend("home", meta); err == nil {
			t.Fatalf("meta=%v: expected ownership error", meta)
		}
	}
}

func TestSoldierEndpointsBusyCheckerOutcomes(t *testing.T) {
	for _, tt := range []struct {
		busy bool
		err  error
	}{{false, nil}, {true, nil}, {false, errors.New("unknown")}} {
		backend := &soldierEndpointBackend{alive: true, busy: tt.busy, busyErr: tt.err}
		endpoint := sessionSoldierEndpoints{resolve: func(string, map[string]string) (session.Backend, string, error) { return backend, "tmux", nil }}
		got, err := endpoint.Busy("home", map[string]string{"window": "pane"})
		if got != tt.busy || (err != nil) != (tt.err != nil) {
			t.Fatalf("got=%v err=%v", got, err)
		}
	}
}

func TestSoldierEndpointsRecognizedAgentOutcomes(t *testing.T) {
	for _, tt := range []struct {
		status                           string
		alive, recognized, busy, wantErr bool
	}{
		{"working", true, true, true, false}, {"idle", true, true, false, false},
		{"review-ready", true, true, false, false}, {"mystery", true, true, false, true},
		{"", true, false, false, true}, {"", false, false, false, true},
	} {
		backend := recognizedOnlyBackend{base: &soldierEndpointBackend{alive: tt.alive, recognized: tt.recognized, status: tt.status}}
		endpoint := sessionSoldierEndpoints{resolve: func(string, map[string]string) (session.Backend, string, error) { return backend, "custom", nil }}
		got, err := endpoint.Busy("home", map[string]string{"window": "pane"})
		if got != tt.busy || (err != nil) != tt.wantErr {
			t.Fatalf("%+v: got=%v err=%v", tt, got, err)
		}
	}
}

func TestSoldierEndpointsMapsPromptOutcomes(t *testing.T) {
	for _, status := range []session.PromptStatus{session.PromptSubmitted, session.PromptQueuedWhileBusy, session.PromptStalled, session.PromptEndpointDead, session.PromptBackendFailed, session.PromptUnsupported} {
		backend := &soldierEndpointBackend{prompt: session.PromptResult{Status: status}}
		endpoint := sessionSoldierEndpoints{resolve: func(string, map[string]string) (session.Backend, string, error) { return backend, "tmux", nil }}
		got := endpoint.Send("home", map[string]string{"window": "pane"}, "payload")
		wantAck := status == session.PromptSubmitted || status == session.PromptQueuedWhileBusy
		if got.Acknowledged != wantAck || !strings.EqualFold(got.Status, string(status)) {
			t.Fatalf("status=%s got=%+v", status, got)
		}
	}
}
