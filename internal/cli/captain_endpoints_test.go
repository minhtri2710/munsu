package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/fleet"
)

type captainProbeBackend struct {
	alive       bool
	paneAlive   bool
	agentAlive  bool
	agentErr    error
	prompt      backend.PromptResult
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
func (b *captainProbeBackend) AgentPrompt(string, string) backend.PromptResult {
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
	// notFoundOnAbsent makes a false probe report structured authoritative
	// absence (ErrPaneNotFound), modelling a verified structured backend.
	notFoundOnAbsent bool
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

// CheckAlive is the structured probe: a false probe reports authoritative
// absence (ErrPaneNotFound) when notFoundOnAbsent is set, else an operational
// not-confirmed reading.
func (b *ordinaryCaptainProbeBackend) CheckAlive(string) (bool, error) {
	result := b.alive
	if b.aliveCalls < len(b.aliveResults) {
		result = b.aliveResults[b.aliveCalls]
	}
	b.aliveCalls++
	if !result && b.notFoundOnAbsent {
		return false, backend.ErrPaneNotFound
	}
	return result, nil
}
func (b *ordinaryCaptainProbeBackend) Teardown(string) error { b.teardownCalls++; return b.teardownErr }

func TestSessionProbeEndpointResolutionAndOwnership(t *testing.T) {
	bk := &ordinaryCaptainProbeBackend{alive: true}
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
			ep := sessionProbeEndpoint{resolve: func(string, map[string]string) (backend.Backend, string, error) {
				return bk, tt.resolved, tt.resolveErr
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
		bk := &ordinaryCaptainProbeBackend{alive: alive}
		ep := sessionProbeEndpoint{resolve: func(string, map[string]string) (backend.Backend, string, error) { return bk, "tmux", nil }}
		got, err := ep.Probe("home", map[string]string{"window": "pane"})
		if err != nil {
			t.Fatal(err)
		}
		if got.PaneAlive != alive || got.AgentAlive != alive {
			t.Fatalf("got=%+v, alive=%v", got, alive)
		}
		// A plain Alive() bool cannot prove authoritative absence: Absent stays
		// false even when Alive=false, so the unproven reading fails closed.
		if got.Absent {
			t.Fatalf("got=%+v, Absent must be false for non-agent-aware backend", got)
		}
	}
}

func TestSessionNudgeEndpointTypedOutcomes(t *testing.T) {
	statuses := []backend.PromptStatus{backend.PromptSubmitted, backend.PromptQueuedWhileBusy, backend.PromptStalled, backend.PromptUnsupported, backend.PromptEndpointDead, backend.PromptUnsafeComposer, backend.PromptBackendFailed}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			bk := &captainProbeBackend{paneAlive: true, agentAlive: true, prompt: backend.PromptResult{Status: status}}
			resolves := 0
			ep := sessionNudgeEndpoint{resolve: func(string, map[string]string) (backend.Backend, string, error) {
				resolves++
				return bk, "herdr", nil
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
	for _, bk := range []*captainProbeBackend{{paneAlive: false}, {paneAlive: true, agentAlive: false}, {agentErr: backend.ErrPaneNotFound}} {
		ep := sessionNudgeEndpoint{resolve: func(string, map[string]string) (backend.Backend, string, error) { return bk, "herdr", nil }}
		got, err := ep.Nudge("home", map[string]string{"window": "owned:pane", "herdr_session": "owned"}, "payload")
		if err != nil || got.Status != "unavailable" || bk.promptCalls != 0 {
			t.Fatalf("got=%+v err=%v calls=%d", got, err, bk.promptCalls)
		}
	}
}

func TestSessionRetireEndpointOutcomes(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name         string
		bk           *ordinaryCaptainProbeBackend
		resolveErr   error
		meta         map[string]string
		wantErr      bool
		wantSend     int
		wantTeardown int
	}{
		{name: "resolution", bk: &ordinaryCaptainProbeBackend{}, resolveErr: boom, meta: map[string]string{"window": "pane"}, wantErr: true},
		{name: "backend mismatch", bk: &ordinaryCaptainProbeBackend{}, meta: map[string]string{"backend": "tmux", "window": "pane"}, wantErr: true},
		{name: "herdr mismatch", bk: &ordinaryCaptainProbeBackend{}, meta: map[string]string{"backend": "herdr", "window": "other:pane", "herdr_session": "owned"}, wantErr: true},
		{name: "quit failure", bk: &ordinaryCaptainProbeBackend{alive: true, sendErr: boom}, meta: map[string]string{"window": "pane"}, wantErr: true, wantSend: 1},
		{name: "graceful exit", bk: &ordinaryCaptainProbeBackend{aliveResults: []bool{true, false}, notFoundOnAbsent: true}, meta: map[string]string{"window": "pane"}, wantSend: 1},
		{name: "teardown fallback", bk: &ordinaryCaptainProbeBackend{aliveResults: []bool{true, true}}, meta: map[string]string{"window": "pane"}, wantSend: 1, wantTeardown: 1},
		{name: "dead teardown failure", bk: &ordinaryCaptainProbeBackend{teardownErr: boom}, meta: map[string]string{"window": "pane"}, wantErr: true, wantTeardown: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep := sessionRetireEndpoint{resolve: func(string, map[string]string) (backend.Backend, string, error) {
				return tt.bk, "herdr", tt.resolveErr
			}, sleep: func(time.Duration) {}}
			err := ep.Retire("home", tt.meta)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.bk.sendCalls != tt.wantSend || tt.bk.teardownCalls != tt.wantTeardown {
				t.Fatalf("send=%d teardown=%d", tt.bk.sendCalls, tt.bk.teardownCalls)
			}
		})
	}
}

type labelCaptureBackend struct{ gotLabel string }

func (c *labelCaptureBackend) NewWindow(label, name string) (string, error) {
	c.gotLabel = label
	return "win", nil
}
func (c *labelCaptureBackend) SendKeys(string, string) error       { return nil }
func (c *labelCaptureBackend) Capture(string, int) (string, error) { return "", nil }
func (c *labelCaptureBackend) Alive(string) bool                   { return false }
func (c *labelCaptureBackend) Teardown(string) error               { return nil }

func TestSessionLaunchEndpointDerivesContainerLabel(t *testing.T) {
	makeEndpoint := func(c *labelCaptureBackend) sessionLaunchEndpoint {
		return sessionLaunchEndpoint{resolve: func(string, string) (backend.Backend, string, error) {
			return c, "tmux", nil
		}}
	}

	t.Run("plain home hash fallback", func(t *testing.T) {
		c := &labelCaptureBackend{}
		ep := makeEndpoint(c)
		workingDir := t.TempDir()
		if _, err := ep.Launch("home", fleet.LaunchRequest{Backend: "tmux", WindowName: "mu-captain-x", Command: "echo", WorkingDir: workingDir}); err != nil {
			t.Fatal(err)
		}
		if want := backend.WorkspaceTag(workingDir); c.gotLabel != want {
			t.Fatalf("label=%q want %q", c.gotLabel, want)
		}
		if c.gotLabel == "" {
			t.Fatal("label must not be empty")
		}
	})

	t.Run("marked captain home readable prefix", func(t *testing.T) {
		captainHome := t.TempDir()
		os.MkdirAll(captainHome, 0755)
		os.WriteFile(filepath.Join(captainHome, ".munsu-captain-home"), []byte("munsu-v2\ncaptain-one\ntag\n"), 0600)
		c := &labelCaptureBackend{}
		ep := makeEndpoint(c)
		if _, err := ep.Launch("home", fleet.LaunchRequest{Backend: "tmux", WindowName: "mu-captain-x", Command: "echo", WorkingDir: captainHome}); err != nil {
			t.Fatal(err)
		}
		if want := backend.WorkspaceTag(captainHome); c.gotLabel != want {
			t.Fatalf("label=%q want %q", c.gotLabel, want)
		}
		if !strings.HasPrefix(c.gotLabel, "captain-captain-one-") {
			t.Fatalf("label=%q must start with captain prefix", c.gotLabel)
		}
	})
}

func TestSessionProbeEndpointAgentAwareOutcomes(t *testing.T) {
	boom := errors.New("protocol failed")
	tests := []struct {
		name       string
		backend    *captainProbeBackend
		wantPane   bool
		wantAgent  bool
		wantAbsent bool
		wantErr    error
	}{
		{name: "alive", backend: &captainProbeBackend{paneAlive: true, agentAlive: true}, wantPane: true, wantAgent: true},
		{name: "agent absent", backend: &captainProbeBackend{paneAlive: true}, wantPane: true, wantAbsent: false},
		{name: "pane absent", backend: &captainProbeBackend{agentErr: backend.ErrPaneNotFound}, wantAbsent: true},
		{name: "other error", backend: &captainProbeBackend{agentErr: boom}, wantErr: boom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep := sessionProbeEndpoint{resolve: func(string, map[string]string) (backend.Backend, string, error) { return tt.backend, "herdr", nil }}
			got, err := ep.Probe("home", map[string]string{"window": "owned:pane", "herdr_session": "owned"})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error=%v, want=%v", err, tt.wantErr)
			}
			if got.PaneAlive != tt.wantPane || got.AgentAlive != tt.wantAgent || got.Absent != tt.wantAbsent {
				t.Fatalf("got=%+v, want pane=%t agent=%t absent=%t", got, tt.wantPane, tt.wantAgent, tt.wantAbsent)
			}
		})
	}
}
