package fleet

import "testing"

// authorizedAbsent builds a Fleet-authorized Absent() observation: dead,
// current, trusted source. Only Fleet can produce this shape (authorizeAbsence).
func authorizedAbsent(activity Activity) EndpointStatus {
	return EndpointStatus{
		Lifecycle:      LifecycleDead,
		Responsiveness: Responsive,
		Freshness:      FreshnessCurrent,
		Activity:       activity,
		Source:         SourceProbe,
		Incarnation:    "inc-1",
	}
}

// liveObs builds a Fleet-authorized Live() observation carrying the given
// Activity axis.
func liveObs(activity Activity) EndpointStatus {
	return EndpointStatus{
		Lifecycle:      LifecycleAlive,
		Responsiveness: Responsive,
		Freshness:      FreshnessCurrent,
		Activity:       activity,
		Source:         SourceProbe,
		Incarnation:    "inc-1",
	}
}

// TestReadBusy_DerivesFromActivityAxis pins the busy authority: each Activity
// arm maps to its own answer, unknown/invalid never become idle, blocked is
// neither idle nor busy, and only a Fleet-authorized Absent() yields dead.
func TestReadBusy_DerivesFromActivityAxis(t *testing.T) {
	cases := []struct {
		name string
		obs  EndpointStatus
		want BusyReading
	}{
		{"busy live -> held", liveObs(ActivityBusy), BusyReadingHeld},
		{"idle live -> idle", liveObs(ActivityIdle), BusyReadingIdle},
		{"unknown live -> unknown", liveObs(ActivityUnknown), BusyReadingUnknown},
		{"blocked live -> blocked", liveObs(ActivityBlocked), BusyReadingBlocked},
		{"invalid activity -> unknown", liveObs(ActivityInvalid), BusyReadingUnknown},
		{"out-of-range activity -> unknown", liveObs(Activity(200)), BusyReadingUnknown},
		{"lifecycle unknown with unknown activity", EndpointStatus{Lifecycle: LifecycleUnknown, Freshness: FreshnessUnknown, Activity: ActivityUnknown, Source: SourceProbe}, BusyReadingUnknown},
		{"stale derived unknown", EndpointStatus{Lifecycle: LifecycleUnknown, Freshness: FreshnessStale, Activity: ActivityUnknown, Source: SourceDerived}, BusyReadingUnknown},
		{"authorized absent overrides busy", authorizedAbsent(ActivityBusy), BusyReadingDead},
		{"authorized absent overrides idle", authorizedAbsent(ActivityIdle), BusyReadingDead},
		{"authorized absent overrides blocked", authorizedAbsent(ActivityBlocked), BusyReadingDead},
		{"authorized absent with unknown activity", authorizedAbsent(ActivityUnknown), BusyReadingDead},
		{"authorized absent via derived source", func() EndpointStatus {
			o := authorizedAbsent(ActivityBusy)
			o.Source = SourceDerived
			return o
		}(), BusyReadingDead},
		{"zero observation -> unknown", EndpointStatus{}, BusyReadingUnknown},
		// A live-looking event observation yields only the Activity-derived
		// hint: ReadBusy never consults lifecycle liveness, so the answer is
		// neither dead nor a live-derived conclusion.
		{"live-looking event busy -> held (hint only)", EndpointStatus{Lifecycle: LifecycleAlive, Responsiveness: Responsive, Freshness: FreshnessCurrent, Activity: ActivityBusy, Source: SourceEvent}, BusyReadingHeld},
		{"live-looking event idle -> idle (hint only)", EndpointStatus{Lifecycle: LifecycleAlive, Responsiveness: Responsive, Freshness: FreshnessCurrent, Activity: ActivityIdle, Source: SourceEvent}, BusyReadingIdle},
		{"event busy hint without liveness", EndpointStatus{Lifecycle: LifecycleUnknown, Freshness: FreshnessUnknown, Activity: ActivityBusy, Source: SourceEvent}, BusyReadingHeld},
		{"event idle hint without liveness", EndpointStatus{Lifecycle: LifecycleUnknown, Freshness: FreshnessUnknown, Activity: ActivityIdle, Source: SourceEvent}, BusyReadingIdle},
		{"event blocked hint", EndpointStatus{Lifecycle: LifecycleUnknown, Freshness: FreshnessUnknown, Activity: ActivityBlocked, Source: SourceEvent}, BusyReadingBlocked},
		{"event unknown hint", EndpointStatus{Lifecycle: LifecycleUnknown, Freshness: FreshnessUnknown, Activity: ActivityUnknown, Source: SourceEvent}, BusyReadingUnknown},
		{"event claims dead and current but stays hint", EndpointStatus{Lifecycle: LifecycleDead, Responsiveness: Responsive, Freshness: FreshnessCurrent, Activity: ActivityBusy, Source: SourceEvent}, BusyReadingHeld},
		{"event claims dead and current with unknown activity", EndpointStatus{Lifecycle: LifecycleDead, Responsiveness: Responsive, Freshness: FreshnessCurrent, Activity: ActivityUnknown, Source: SourceEvent}, BusyReadingUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ReadBusy(tc.obs)
			if got != tc.want {
				t.Fatalf("ReadBusy(%s) = %s, want %s", tc.obs, got, tc.want)
			}
		})
	}
}

// TestReadBusy_AdapterProbeAloneNeverDead pins P1a: an adapter's raw dead
// probe is not Fleet-authorized (freshness unknown) and never reads dead; the
// Activity axis still answers.
func TestReadBusy_AdapterProbeAloneNeverDead(t *testing.T) {
	cases := []struct {
		name string
		obs  EndpointStatus
		want BusyReading
	}{
		{"raw dead probe, busy", EndpointStatus{Lifecycle: LifecycleDead, Responsiveness: Responsive, Freshness: FreshnessUnknown, Activity: ActivityBusy, Source: SourceProbe}, BusyReadingHeld},
		{"raw dead probe, idle", EndpointStatus{Lifecycle: LifecycleDead, Responsiveness: Responsive, Freshness: FreshnessUnknown, Activity: ActivityIdle, Source: SourceProbe}, BusyReadingIdle},
		{"raw dead probe, unknown", EndpointStatus{Lifecycle: LifecycleDead, Responsiveness: Responsive, Freshness: FreshnessUnknown, Activity: ActivityUnknown, Source: SourceProbe}, BusyReadingUnknown},
		{"stale dead reading, busy", EndpointStatus{Lifecycle: LifecycleDead, Responsiveness: Responsive, Freshness: FreshnessStale, Activity: ActivityBusy, Source: SourceProbe}, BusyReadingHeld},
		{"dead lifecycle without freshness", EndpointStatus{Lifecycle: LifecycleDead, Activity: ActivityBusy, Source: SourceDerived}, BusyReadingHeld},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.obs.Absent() {
				t.Fatalf("fixture %s must not be Absent()", tc.obs)
			}
			got := ReadBusy(tc.obs)
			if got == BusyReadingDead {
				t.Fatalf("ReadBusy(%s) concluded dead from an unauthorized probe", tc.obs)
			}
			if got != tc.want {
				t.Fatalf("ReadBusy(%s) = %s, want %s", tc.obs, got, tc.want)
			}
		})
	}
}

// TestReadBusy_ConsumesFleetAuthorization drives the real upstream
// authorization: the same raw dead probe reads dead only after
// authorizeAbsence promotes it under a complete current proof, and reads
// unknown (never idle, never dead) when the proof is incomplete and the
// observation is demoted.
func TestReadBusy_ConsumesFleetAuthorization(t *testing.T) {
	raw := EndpointStatus{
		Lifecycle:      LifecycleDead,
		Responsiveness: Responsive,
		Freshness:      FreshnessUnknown,
		Activity:       ActivityBusy,
		Source:         SourceProbe,
	}
	if got := ReadBusy(raw); got != BusyReadingHeld {
		t.Fatalf("raw probe = %s, want held before authorization", got)
	}

	complete := exactEndpointProof{
		backend: "tmux", handle: "w1:p1", incarnation: "inc-1",
		leaseID: "lease-1", fenceToken: "fence-1", generation: 1, revision: 1,
	}
	authorized := authorizeAbsence(raw, complete)
	if !authorized.Absent() {
		t.Fatalf("authorizeAbsence did not conclude Absent(): %s", authorized)
	}
	if got := ReadBusy(authorized); got != BusyReadingDead {
		t.Fatalf("authorized absent = %s, want dead", got)
	}

	demoted := authorizeAbsence(raw, exactEndpointProof{backend: "tmux", handle: "w1:p1"})
	if demoted.Absent() {
		t.Fatalf("incomplete proof must not authorize absence: %s", demoted)
	}
	if got := ReadBusy(demoted); got != BusyReadingUnknown {
		t.Fatalf("demoted observation = %s, want unknown", got)
	}

	// Positive authorization does not change the Activity-derived answer.
	rawAlive := EndpointStatus{
		Lifecycle:      LifecycleAlive,
		Responsiveness: Responsive,
		Freshness:      FreshnessUnknown,
		Activity:       ActivityIdle,
		Source:         SourceProbe,
	}
	complete.acquired = true
	live := authorizeLive(rawAlive, complete)
	if !live.Live() {
		t.Fatalf("authorizeLive did not conclude Live(): %s", live)
	}
	if got := ReadBusy(live); got != BusyReadingIdle {
		t.Fatalf("authorized live idle = %s, want idle", got)
	}
}

// TestBusyReading_String enters every String() arm, including the zero value
// and an out-of-range value.
func TestBusyReading_String(t *testing.T) {
	cases := []struct {
		r   BusyReading
		str string
	}{
		{BusyReading(0), "invalid"},
		{BusyReadingHeld, "held"},
		{BusyReadingIdle, "idle"},
		{BusyReadingUnknown, "unknown"},
		{BusyReadingBlocked, "blocked"},
		{BusyReadingDead, "dead"},
		{BusyReading(200), "invalid"},
	}
	for _, tc := range cases {
		if got := tc.r.String(); got != tc.str {
			t.Errorf("BusyReading(%d).String() = %q, want %q", uint8(tc.r), got, tc.str)
		}
	}
}
