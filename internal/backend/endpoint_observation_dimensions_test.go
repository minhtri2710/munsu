package backend

import (
	"errors"
	"testing"
)

// contractBoolBackend is a legacy bool-only backend: it implements Backend
// (with Alive) but none of the structured probe surfaces, so observation must
// treat it as a legacy-bool source.
type contractBoolBackend struct{ alive bool }

func (b *contractBoolBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b *contractBoolBackend) SendKeys(string, string) error            { return nil }
func (b *contractBoolBackend) Capture(string, int) (string, error)      { return "", nil }
func (b *contractBoolBackend) Alive(string) bool                        { return b.alive }
func (b *contractBoolBackend) Teardown(string) error                    { return nil }

// TestEndpointObservationOrthogonalAxes asserts the typed orthogonal contract:
// lifecycle/responsiveness/freshness/activity/source are independent dims, and
// ambiguous states are never dead and never authorize recovery (BEO-16).
func TestEndpointObservationOrthogonalAxes(t *testing.T) {
	base := func(lifecycle LifecycleState, resp Responsiveness, fresh Freshness, src ObservationSource) EndpointObservation {
		return EndpointObservation{
			Lifecycle: lifecycle, Responsiveness: resp, Freshness: fresh,
			Activity: ActivityUnknown, Source: src,
		}
	}

	live := base(LifecycleAlive, Responsive, FreshnessCurrent, SourceProbe)
	if !live.Live() || !live.Alive() {
		t.Fatalf("alive/current/response must be Live: %+v", live)
	}

	dead := base(LifecycleDead, Responsive, FreshnessCurrent, SourceProbe)
	if !dead.Absent() {
		t.Fatalf("dead/current/probe must be Absent (recovery eligible): %+v", dead)
	}

	// Invariants: unknown != dead, unresponsive != dead, starting != dead,
	// stale != dead.
	ambiguous := []EndpointObservation{
		base(LifecycleUnknown, Responsive, FreshnessCurrent, SourceProbe),                 // unknown
		base(LifecycleUnknown, Unresponsive, FreshnessUnknown, SourceProbe),               // unresponsive
		base(LifecycleStarting, Responsive, FreshnessCurrent, SourceProbe),                // starting
		base(LifecycleAlive, Responsive, FreshnessStale, SourceProbe),                     // stale (but alive)
		base(LifecycleUnknown, ResponsivenessUnknown, FreshnessUnknown, SourceLegacyBool), // legacy false
	}
	for i, obs := range ambiguous {
		if obs.Absent() {
			t.Errorf("ambiguous observation #%d must not authorize recovery: %+v", i, obs)
		}
		if obs.Lifecycle == LifecycleDead {
			t.Errorf("ambiguous observation #%d must not be dead: %+v", i, obs)
		}
	}

	// A created-but-not-live reading is not Live.
	if base(LifecycleStarting, Responsive, FreshnessCurrent, SourceProbe).Live() {
		t.Fatal("starting must not be Live")
	}
}

// TestObservationFromProbeErrorClassification asserts only structured
// ErrPaneNotFound becomes dead/current; every operational failure is
// unknown/unresponsive and never dead.
func TestObservationFromProbeErrorClassification(t *testing.T) {
	ref := EndpointRef{Backend: "tmux", Handle: "s:p", Incarnation: "inc-1"}

	dead := ObservationFromProbeError(ref, ErrPaneNotFound)
	if !dead.Absent() || dead.Lifecycle != LifecycleDead || dead.Freshness != FreshnessCurrent {
		t.Fatalf("ErrPaneNotFound must map to dead/current: %+v", dead)
	}

	for _, opErr := range []error{
		errors.New("timeout"),
		errors.New("permission denied"),
		errors.New("malformed response"),
		errors.New("binary not found"),
		errors.New("socket failure"),
	} {
		obs := ObservationFromProbeError(ref, opErr)
		if obs.Lifecycle == LifecycleDead || obs.Absent() {
			t.Fatalf("operational error %q must never be dead/absent: %+v", opErr, obs)
		}
		if obs.Responsiveness != Unresponsive {
			t.Errorf("operational error %q: responsiveness = %v, want unresponsive", opErr, obs.Responsiveness)
		}
	}

	nilObs := ObservationFromProbeError(ref, nil)
	if nilObs.Lifecycle == LifecycleDead || nilObs.Absent() {
		t.Fatalf("nil-error observation must not be dead: %+v", nilObs)
	}
}

// TestObservationIncarinationMismatchIsStale asserts a probe carrying a
// different/wrong incarnation than the exact bound identity is classified
// unknown/stale and never remains authoritative.
func TestObservationIncarinationMismatchIsStale(t *testing.T) {
	obs := EndpointObservation{
		Lifecycle: LifecycleAlive, Responsiveness: Responsive, Freshness: FreshnessCurrent,
		Activity: ActivityUnknown, Source: SourceProbe, Incarnation: "inc-foreign",
	}
	// Expected bound incarnation is "inc-exact" — mismatch.
	got := obs.CrossCheckBinding("backend", "handle", "inc-exact")
	if got.Lifecycle != LifecycleUnknown || got.Freshness != FreshnessStale {
		t.Fatalf("incarnation mismatch must downgrade to unknown/stale: %+v", got)
	}
	if got.Absent() || got.Live() {
		t.Fatal("stale mismatch must not be recovered or live")
	}

	// Correct incarnation is untouched.
	match := obs.CrossCheckBinding("backend", "handle", "inc-foreign")
	if !match.Live() || match.Freshness != FreshnessCurrent {
		t.Fatalf("matching incarnation must stay live/current: %+v", match)
	}
}

// TestObserveBoundEndpointCrossChecksIncarination asserts the probe function
// propagates the expected incarnation into the observation (so callers can
// cross-check freshness downstream).
func TestObserveBoundEndpointCrossChecksIncarination(t *testing.T) {
	// A legacy bool backend: both true and false map to unknown, never dead.
	bk := &contractBoolBackend{alive: false}
	obs := ObserveBoundEndpoint(bk, "pane-1", "inc-x")
	if obs.Source != SourceLegacyBool || obs.Lifecycle != LifecycleUnknown {
		t.Fatalf("legacy-bool false must be unknown/legacy-bool: %+v", obs)
	}
	if obs.Absent() || obs.Live() {
		t.Fatal("legacy-bool false must not authorize recovery or be live")
	}
	if obs.Incarnation != "inc-x" {
		t.Fatalf("expected incarnation must propagate: %q", obs.Incarnation)
	}
}
