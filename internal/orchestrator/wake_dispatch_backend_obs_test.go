package orchestrator

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/testutil"
)

// observeProbePort mirrors the production cli.probeAdapter: it returns the
// canonical backend.EndpointObservation produced by backend.ObserveEndpoint.
// This is the contract the intent requires — wake dispatch must consume the
// canonical backend observation directly, with no orchestrator duplicate type
// in the data path.
type observeProbePort struct {
	bk backend.Backend
}

func (p *observeProbePort) Probe(window string) (backend.EndpointObservation, error) {
	return backend.ObserveEndpoint(p.bk, window), nil
}

// fakeAgentAwareBackend is a minimal AgentAwareBackend whose CheckAgentAlive
// returns a fixed (paneAlive, agentAlive, err) so ObserveEndpoint derives a
// known coarse state, exactly as a real herdr/tmux backend would.
type fakeAgentAwareBackend struct {
	paneAlive  bool
	agentAlive bool
	err        error
}

func (b *fakeAgentAwareBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b *fakeAgentAwareBackend) SendKeys(string, string) error            { return nil }
func (b *fakeAgentAwareBackend) Capture(string, int) (string, error)      { return "", nil }
func (b *fakeAgentAwareBackend) Alive(string) bool                        { return true }
func (b *fakeAgentAwareBackend) Teardown(string) error                    { return nil }
func (b *fakeAgentAwareBackend) CheckAgentAlive(string) (bool, bool, error) {
	return b.paneAlive, b.agentAlive, b.err
}

// TestDispatchWake_ConsumesCanonicalBackendObservation drives the full
// production data path: a real backend.Backend -> backend.ObserveEndpoint ->
// DispatchWake. It asserts that each coarse observation state maps to the
// same per-state dispatch decision as before the refactor, and that every
// non-alive state leaves the durable Wake unclaimed (Recovery/claim is only
// reached on a confirmed-alive observation).
func TestDispatchWake_ConsumesCanonicalBackendObservation(t *testing.T) {
	cases := []struct {
		name         string
		bk           backend.Backend
		wantSubmitted bool
		wantReason   string
	}{
		{
			name:         "alive-proceeds-and-claims",
			bk:           &fakeAgentAwareBackend{paneAlive: true, agentAlive: true},
			wantSubmitted: true,
		},
		{
			name:         "starting-skipped",
			bk:           &fakeAgentAwareBackend{paneAlive: true, agentAlive: false},
			wantSubmitted: false,
			wantReason:   "target-unready",
		},
		{
			name:         "dead-skipped",
			bk:           &fakeAgentAwareBackend{paneAlive: false, agentAlive: false, err: backend.ErrPaneNotFound},
			wantSubmitted: false,
			wantReason:   "endpoint-dead",
		},
		{
			name:         "unresponsive-skipped",
			bk:           &fakeAgentAwareBackend{paneAlive: false, agentAlive: false, err: errors.New("timeout")},
			wantSubmitted: false,
			wantReason:   "target-unready",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := testutil.TempHome(t)
			// Enqueue a durable Wake so the claim step is reachable on a live endpoint.
			if err := EnqueueWake(home, "signal", "task-1", "payload"); err != nil {
				t.Fatalf("EnqueueWake: %v", err)
			}

			req := DispatchWakeRequest{
				HomeDir: home,
				Mode:    WakeDeliveryHerdr,
				Target:  TargetResult{Source: RuntimeSource, Handle: "default:w1:p1", Session: "default"},
				// Real production adapter: the canonical backend observation flows in.
				Probe:  &observeProbePort{bk: tc.bk},
				Submit: &mockSubmitPort{acknowledged: true},
			}

			result, err := DispatchWake(req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantSubmitted {
				if result.Outcome != WakeSubmitted {
					t.Fatalf("alive endpoint: expected Submitted, got outcome=%q reason=%q", result.Outcome, result.Reason)
				}
				return
			}

			if result.Outcome != WakeSkipped {
				t.Fatalf("expected Skipped, got outcome=%q reason=%q", result.Outcome, result.Reason)
			}
			if result.Reason != tc.wantReason {
				t.Errorf("expected reason %q, got %q", tc.wantReason, result.Reason)
			}

			// Non-alive states must NOT claim the durable Wake.
			claim, err := ClaimWakes(home, "munsu:herdr", 60, 10)
			if err != nil {
				t.Fatalf("ClaimWakes: %v", err)
			}
			if claim == nil || len(claim.Wakes) == 0 {
				t.Fatalf("state %q must NOT trigger wake claim (wake was already consumed)", tc.wantReason)
			}
		})
	}
}
