package backend

import (
	"errors"
	"testing"
)

// TestEndpointObservationOrthogonalAxes asserts the typed orthogonal contract:
// lifecycle/responsiveness/freshness/activity/source are independent dims, and
// ambiguous states are never dead and never authorize recovery on their own.
func TestEndpointObservationOrthogonalAxes(t *testing.T) {
	base := func(lifecycle LifecycleState, resp Responsiveness, fresh Freshness, src ObservationSource) EndpointObservation {
		return EndpointObservation{
			Lifecycle: lifecycle, Responsiveness: resp, Freshness: fresh,
			Activity: ActivityUnknown, Source: src,
		}
	}

	// Live/Absent require FreshnessCurrent, which only Fleet authorization sets.
	live := base(LifecycleAlive, Responsive, FreshnessCurrent, SourceProbe)
	if !live.Live() || !live.Alive() {
		t.Fatalf("alive/current/probe must be Live: %+v", live)
	}

	dead := base(LifecycleDead, Responsive, FreshnessCurrent, SourceProbe)
	if !dead.Absent() {
		t.Fatalf("dead/current/probe must be Absent (recovery eligible): %+v", dead)
	}

	// A raw adapter probe is always FreshnessUnknown: even a dead lifecycle can
	// NOT be Absent() and an alive lifecycle can NOT be Live() on its own.
	rawAlive := base(LifecycleAlive, Responsive, FreshnessUnknown, SourceProbe)
	if rawAlive.Live() || rawAlive.Absent() {
		t.Fatalf("raw adapter observation must not be Live/Absent: %+v", rawAlive)
	}
	rawDead := base(LifecycleDead, Responsive, FreshnessUnknown, SourceProbe)
	if rawDead.Absent() || rawDead.Live() {
		t.Fatalf("raw dead observation must not be Absent without Fleet authorization: %+v", rawDead)
	}

	// Ambiguous states are never dead/absent.
	ambiguous := []EndpointObservation{
		base(LifecycleUnknown, Responsive, FreshnessCurrent, SourceProbe),
		base(LifecycleUnknown, Unresponsive, FreshnessUnknown, SourceProbe),
		base(LifecycleStarting, Responsive, FreshnessCurrent, SourceProbe),
		base(LifecycleAlive, Responsive, FreshnessStale, SourceProbe),
		base(LifecycleUnknown, ResponsivenessUnknown, FreshnessUnknown, SourceDerived),
	}
	for i, obs := range ambiguous {
		if obs.Absent() {
			t.Errorf("ambiguous observation #%d must not authorize recovery: %+v", i, obs)
		}
		if obs.Lifecycle == LifecycleDead {
			t.Errorf("ambiguous observation #%d must not be dead: %+v", i, obs)
		}
	}
	if base(LifecycleStarting, Responsive, FreshnessCurrent, SourceProbe).Live() {
		t.Fatal("starting must not be Live")
	}
}

// TestObservationFromProbeErrorClassification asserts only a structured
// ErrPaneNotFound is a dead lifecycle; every operational failure is
// unresponsive/unknown, and — because an adapter cannot attest the launch
// incarnation — even ErrPaneNotFound observations are FreshnessUnknown and not
// Absent() on their own.
func TestObservationFromProbeErrorClassification(t *testing.T) {
	dead := ObservationFromProbeError(ErrPaneNotFound)
	if dead.Lifecycle != LifecycleDead || dead.Responsiveness != Responsive {
		t.Fatalf("ErrPaneNotFound must map to dead lifecycle: %+v", dead)
	}
	if dead.Freshness != FreshnessUnknown {
		t.Fatalf("adapter probe must not fabricate FreshnessCurrent: %+v", dead)
	}
	if dead.Absent() || dead.Live() {
		t.Fatalf("ErrPaneNotFound observation must not be Absent without Fleet authorization: %+v", dead)
	}

	for _, opErr := range []error{
		errors.New("timeout"),
		errors.New("permission denied"),
		errors.New("malformed response"),
		errors.New("binary not found"),
		errors.New("no server running"),
		errors.New("socket failure"),
	} {
		obs := ObservationFromProbeError(opErr)
		if obs.Lifecycle == LifecycleDead || obs.Absent() {
			t.Fatalf("operational error %q must never be dead/absent: %+v", opErr, obs)
		}
		if obs.Responsiveness != Unresponsive {
			t.Errorf("operational error %q: responsiveness = %v, want unresponsive", opErr, obs.Responsiveness)
		}
	}

	nilObs := ObservationFromProbeError(nil)
	if nilObs.Lifecycle == LifecycleDead || nilObs.Absent() {
		t.Fatalf("nil-error observation must not be dead: %+v", nilObs)
	}
	if nilObs.Incarnation != "" {
		t.Fatalf("adapter observation must not fabricate an incarnation: %+v", nilObs)
	}
}

// TestObserveEndpointReturnsRawFreshnessUnknown asserts ObserveEndpoint never
// fabricates freshness or incarnation for a structured probe and is never
// Absent/Live on its own.
func TestObserveEndpointReturnsRawFreshnessUnknown(t *testing.T) {
	// An agent-aware backend (pane + agent alive) still yields a raw unknown
	// freshness observation that is not Live until Fleet authorizes it.
	bk := &contractAgentBackendRaw{alive: true, agentAlive: true}
	obs := ObserveEndpoint(bk, "pane-1")
	if obs.Lifecycle != LifecycleAlive {
		t.Fatalf("agent-aware alive lifecycle = %v, want alive", obs.Lifecycle)
	}
	if obs.Freshness != FreshnessUnknown || obs.Incarnation != "" {
		t.Fatalf("raw observation must be fresh-unknown with empty incarnation: %+v", obs)
	}
	if obs.Live() || obs.Absent() {
		t.Fatalf("raw observation must not be Live/Absent: %+v", obs)
	}

	// A non-agent-aware structured backend reports pane presence only.
	sb := &contractEndChecker{alive: true}
	sob := ObserveEndpoint(sb, "pane-1")
	if sob.Lifecycle != LifecycleAlive || sob.Freshness != FreshnessUnknown {
		t.Fatalf("checker alive observation = %+v", sob)
	}
	if sob.Absent() || sob.Live() {
		t.Fatalf("checker observation must not be Live/Absent: %+v", sob)
	}

	// A backend with no structured probe is unknown (a probe was attempted but
	// no authoritative surface exists), and still never Live/Absent.
	var nb Backend = &contractNoProbe{alive: true}
	nob := ObserveEndpoint(nb, "pane-1")
	if nob.Lifecycle != LifecycleUnknown || nob.Absent() || nob.Live() {
		t.Fatalf("no-probe backend observation = %+v", nob)
	}
}

// contractAgentBackendRaw is an agent-aware structured backend fixture.
type contractAgentBackendRaw struct{ alive, agentAlive bool }

func (b *contractAgentBackendRaw) NewWindow(string, string) (string, error) { return "", nil }
func (b *contractAgentBackendRaw) SendKeys(string, string) error            { return nil }
func (b *contractAgentBackendRaw) Capture(string, int) (string, error)      { return "", nil }
func (b *contractAgentBackendRaw) Teardown(string) error                    { return nil }
func (b *contractAgentBackendRaw) CheckAgentAlive(string) (bool, bool, error) {
	return b.alive, b.agentAlive, nil
}

// contractEndChecker is a non-agent-aware structured backend fixture.
type contractEndChecker struct{ alive bool }

func (b *contractEndChecker) NewWindow(string, string) (string, error) { return "", nil }
func (b *contractEndChecker) SendKeys(string, string) error            { return nil }
func (b *contractEndChecker) Capture(string, int) (string, error)      { return "", nil }
func (b *contractEndChecker) Teardown(string) error                    { return nil }
func (b *contractEndChecker) CheckAlive(string) (bool, error)          { return b.alive, nil }

// contractNoProbe implements Backend with no structured probe surface.
type contractNoProbe struct{ alive bool }

func (b *contractNoProbe) NewWindow(string, string) (string, error) { return "", nil }
func (b *contractNoProbe) SendKeys(string, string) error            { return nil }
func (b *contractNoProbe) Capture(string, int) (string, error)      { return "", nil }
func (b *contractNoProbe) Teardown(string) error                    { return nil }
