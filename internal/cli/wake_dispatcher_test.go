package cli

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

// --- probeAdapter tests ---

// agentAwareBackend implements AgentAwareBackend to exercise the full
// ObserveBackendEndpoint type-switching path in probeAdapter.Probe.
type agentAwareBackend struct {
	checkAlive   func(string) (bool, bool, error)
}

func (b *agentAwareBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b *agentAwareBackend) SendKeys(string, string) error            { return nil }
func (b *agentAwareBackend) Capture(string, int) (string, error)      { return "", nil }
func (b *agentAwareBackend) Alive(string) bool                        { return true }
func (b *agentAwareBackend) Teardown(string) error                    { return nil }
func (b *agentAwareBackend) CheckAgentAlive(s string) (bool, bool, error) {
	return b.checkAlive(s)
}

// plainBackend implements only the basic Backend interface (no AgentAwareBackend)
// to test the fallback path in ObserveBackendEndpoint.
type plainBackend struct {
	alive bool
}

func (b *plainBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b *plainBackend) SendKeys(string, string) error            { return nil }
func (b *plainBackend) Capture(string, int) (string, error)      { return "", nil }
func (b *plainBackend) Alive(string) bool                        { return b.alive }
func (b *plainBackend) Teardown(string) error                    { return nil }

func TestProbeAdapter_AgentAwareBackendProducesTypedStates(t *testing.T) {
	tests := []struct {
		name        string
		checkAlive  func(string) (bool, bool, error)
		wantState   orchestrator.EndpointObservationState
		wantAlive   bool // alive() should match
	}{
		{
			name:      "alive",
			checkAlive: func(string) (bool, bool, error) { return true, true, nil },
			wantState: orchestrator.EndpointAlive,
			wantAlive: true,
		},
		{
			name:      "starting",
			checkAlive: func(string) (bool, bool, error) { return true, false, nil },
			wantState: orchestrator.EndpointStarting,
			wantAlive: false,
		},
		{
			name:      "dead",
			checkAlive: func(string) (bool, bool, error) { return false, false, backend.ErrPaneNotFound },
			wantState: orchestrator.EndpointDead,
			wantAlive: false,
		},
		{
			name:      "unresponsive",
			checkAlive: func(string) (bool, bool, error) { return false, false, errors.New("timeout") },
			wantState: orchestrator.EndpointUnresponsive,
			wantAlive: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bk := &agentAwareBackend{checkAlive: tt.checkAlive}
			adapter := &probeAdapter{bk: bk}
			got, err := adapter.Probe("pane-1")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.State != tt.wantState {
				t.Errorf("state = %v, want %v", got.State, tt.wantState)
			}
		})
	}
}

func TestProbeAdapter_PlainBackendProducesUnknown(t *testing.T) {
	// A plain backend (no AgentAwareBackend, no endpointAliveChecker) that
	// returns false from Alive() produces EndpointUnknown.
	bk := &plainBackend{alive: false}
	adapter := &probeAdapter{bk: bk}
	got, err := adapter.Probe("pane-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.State != orchestrator.EndpointUnknown {
		t.Errorf("state = %v, want unknown", got.State)
	}
}

// --- submitAdapter tests ---

type testSubmitBackend struct {
	result backend.PromptResult
}

func (b *testSubmitBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b *testSubmitBackend) SendKeys(string, string) error            { return nil }
func (b *testSubmitBackend) Capture(string, int) (string, error)      { return "", nil }
func (b *testSubmitBackend) Alive(string) bool                        { return true }
func (b *testSubmitBackend) Teardown(string) error                    { return nil }
func (b *testSubmitBackend) AgentPrompt(string, string) backend.PromptResult {
	return b.result
}

func TestSubmitAdapter_ConvertsPromptResult(t *testing.T) {
	tests := []struct {
		name   string
		result backend.PromptResult
		want   orchestrator.SubmitResult
	}{
		{
			name:   "submitted",
			result: backend.PromptResult{Status: backend.PromptSubmitted, Detail: "ok"},
			want:   orchestrator.SubmitResult{Acknowledged: true, Status: "submitted", Detail: "ok"},
		},
		{
			name:   "queued-while-busy",
			result: backend.PromptResult{Status: backend.PromptQueuedWhileBusy, Detail: "agent busy"},
			want:   orchestrator.SubmitResult{Acknowledged: true, Status: "queued-while-busy", Detail: "agent busy"},
		},
		{
			name:   "stalled",
			result: backend.PromptResult{Status: backend.PromptStalled, Detail: "no response"},
			want:   orchestrator.SubmitResult{Acknowledged: false, Status: "stalled", Detail: "no response"},
		},
		{
			name:   "endpoint-dead",
			result: backend.PromptResult{Status: backend.PromptEndpointDead, Detail: "pane gone"},
			want:   orchestrator.SubmitResult{Acknowledged: false, Status: "endpoint-dead", Detail: "pane gone"},
		},
		{
			name:   "backend-failed",
			result: backend.PromptResult{Status: backend.PromptBackendFailed, Detail: "cli error", Err: errors.New("exit 1")},
			want:   orchestrator.SubmitResult{Acknowledged: false, Status: "backend-failed", Detail: "cli error", Err: errors.New("exit 1")},
		},
		{
			name:   "unsupported",
			result: backend.PromptResult{Status: backend.PromptUnsupported, Detail: "no agent"},
			want:   orchestrator.SubmitResult{Acknowledged: false, Status: "unsupported", Detail: "no agent"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := &submitAdapter{bk: &testSubmitBackend{result: tt.result}}
			got := adapter.Submit("pane-1", "hello")
			if got.Acknowledged != tt.want.Acknowledged {
				t.Errorf("Acknowledged = %v, want %v", got.Acknowledged, tt.want.Acknowledged)
			}
			if got.Status != tt.want.Status {
				t.Errorf("Status = %q, want %q", got.Status, tt.want.Status)
			}
			if got.Detail != tt.want.Detail {
				t.Errorf("Detail = %q, want %q", got.Detail, tt.want.Detail)
			}
			if (got.Err != nil) != (tt.want.Err != nil) {
				t.Errorf("Err presence = %v, want %v", got.Err != nil, tt.want.Err != nil)
			}
		})
	}
}