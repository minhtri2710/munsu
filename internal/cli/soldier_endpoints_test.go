package cli

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
)

type soldierEndpointBackend struct {
	alive      bool
	recognized bool
	status     string
	prompt     backend.PromptResult
}

func (b *soldierEndpointBackend) NewWindow(string, string) (string, error)          { return "", nil }
func (b *soldierEndpointBackend) FindOrCreateWindow(string, string) (string, error) { return "", nil }
func (b *soldierEndpointBackend) SendKeys(string, string) error                     { return nil }
func (b *soldierEndpointBackend) Capture(string, int) (string, error)               { return "", nil }
func (b *soldierEndpointBackend) Alive(string) bool                                 { return b.alive }
func (b *soldierEndpointBackend) Teardown(string) error                             { return nil }
func (b *soldierEndpointBackend) CheckAgentAlive(string) (bool, bool, error) {
	return b.alive, b.alive && b.recognized, nil
}
func (b *soldierEndpointBackend) IsRecognizedAgent(string) (bool, string) {
	return b.recognized, b.status
}
func (b *soldierEndpointBackend) AgentPrompt(string, string) backend.PromptResult { return b.prompt }

type recognizedOnlyBackend struct{ base *soldierEndpointBackend }

func (b recognizedOnlyBackend) NewWindow(a, c string) (string, error)             { return b.base.NewWindow(a, c) }
func (b recognizedOnlyBackend) FindOrCreateWindow(string, string) (string, error) { return "", nil }
func (b recognizedOnlyBackend) SendKeys(a, c string) error                        { return b.base.SendKeys(a, c) }
func (b recognizedOnlyBackend) Capture(a string, c int) (string, error)           { return b.base.Capture(a, c) }
func (b recognizedOnlyBackend) Alive(a string) bool                               { return b.base.Alive(a) }
func (b recognizedOnlyBackend) Teardown(a string) error                           { return b.base.Teardown(a) }
func (b recognizedOnlyBackend) IsRecognizedAgent(a string) (bool, string) {
	return b.base.IsRecognizedAgent(a)
}
func (b recognizedOnlyBackend) CheckAgentAlive(a string) (bool, bool, error) {
	return b.base.CheckAgentAlive(a)
}

func TestSoldierEndpointsBusyRejectsUnknownRecognizedAgentStatus(t *testing.T) {
	bk := recognizedOnlyBackend{base: &soldierEndpointBackend{alive: true, recognized: true, status: "mystery"}}
	endpoint := sessionSoldierEndpoints{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return bk, "custom", nil
	}}
	busy, err := endpoint.Busy("home", map[string]string{"backend": "custom", "window": "pane"})
	if busy {
		t.Fatal("Busy() = true, want false for unknown status")
	}
	if err == nil || err.Error() != `endpoint status unknown: "unknown"` {
		t.Fatalf("Busy() error = %v, want exact unknown-activity refusal", err)
	}
}

func TestSoldierEndpointsRecognizedAgentOutcomes(t *testing.T) {
	for _, tt := range []struct {
		status                           string
		alive, recognized, busy, wantErr bool
		wantErrPrefix                    string
	}{
		{"working", true, true, true, false, ""},
		{"idle", true, true, false, false, ""},
		{"done", true, true, false, false, ""},
		{" Done ", true, true, false, false, ""},
		{"blocked", true, true, false, true, `endpoint status unknown: "blocked"`},
		{"", true, false, false, true, "endpoint not alive:"},
		{"", false, false, false, true, "endpoint not alive:"},
	} {
		bk := recognizedOnlyBackend{base: &soldierEndpointBackend{alive: tt.alive, recognized: tt.recognized, status: tt.status}}
		endpoint := sessionSoldierEndpoints{resolve: func(string, map[string]string) (backend.Backend, string, error) { return bk, "custom", nil }}
		got, err := endpoint.Busy("home", map[string]string{"backend": "custom", "window": "pane"})
		if got != tt.busy || (err != nil) != tt.wantErr {
			t.Fatalf("%+v: got=%v err=%v", tt, got, err)
		}
		if tt.wantErrPrefix != "" && (err == nil || !strings.HasPrefix(err.Error(), tt.wantErrPrefix)) {
			t.Fatalf("%+v: error = %v, want prefix %q", tt, err, tt.wantErrPrefix)
		}
	}
}

func TestSoldierEndpointsMapsPromptOutcomes(t *testing.T) {
	for _, status := range []backend.PromptStatus{backend.PromptSubmitted, backend.PromptQueuedWhileBusy, backend.PromptStalled, backend.PromptEndpointDead, backend.PromptBackendFailed, backend.PromptUnsupported} {
		bk := &soldierEndpointBackend{prompt: backend.PromptResult{Status: status}}
		endpoint := sessionSoldierEndpoints{resolve: func(string, map[string]string) (backend.Backend, string, error) { return bk, "tmux", nil }}
		got := endpoint.Send("home", map[string]string{"backend": "tmux", "window": "pane"}, "payload")
		wantAck := status == backend.PromptSubmitted || status == backend.PromptQueuedWhileBusy
		if got.Acknowledged != wantAck || !strings.EqualFold(got.Status, string(status)) {
			t.Fatalf("status=%s got=%+v", status, got)
		}
	}
}
