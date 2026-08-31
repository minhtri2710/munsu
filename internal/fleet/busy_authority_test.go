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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ReadBusy(tc.obs)
			if got != tc.want {
				t.Fatalf("ReadBusy(%s) = %s, want %s", tc.obs, got, tc.want)
			}
			if !got.Valid() {
				t.Fatalf("ReadBusy(%s) = %s is not a valid reading", tc.obs, got)
			}
		})
	}
}

// TestReadBusy_UnknownIsNeverIdleOrDead pins P1a: unknown != idle != dead.
func TestReadBusy_UnknownIsNeverIdleOrDead(t *testing.T) {
	for _, obs := range []EndpointStatus{
		liveObs(ActivityUnknown),
		liveObs(ActivityInvalid),
		{Lifecycle: LifecycleUnknown, Freshness: FreshnessUnknown, Activity: ActivityUnknown, Source: SourceProbe},
		{Lifecycle: LifecycleUnknown, Freshness: FreshnessStale, Activity: ActivityUnknown, Source: SourceDerived},
	} {
		got := ReadBusy(obs)
		if got == BusyReadingIdle {
			t.Fatalf("ReadBusy(%s) folded unknown into idle", obs)
		}
		if got == BusyReadingDead {
			t.Fatalf("ReadBusy(%s) folded unknown into dead", obs)
		}
		if got != BusyReadingUnknown {
			t.Fatalf("ReadBusy(%s) = %s, want unknown", obs, got)
		}
	}
}

// TestReadBusy_BlockedIsDistinctAttentionState pins P1a: blocked is never
// folded into idle or busy and is not a lifecycle conclusion.
func TestReadBusy_BlockedIsDistinctAttentionState(t *testing.T) {
	got := ReadBusy(liveObs(ActivityBlocked))
	if got == BusyReadingIdle || got == BusyReadingHeld {
		t.Fatalf("blocked folded into %s", got)
	}
	if got == BusyReadingDead || got == BusyReadingUnknown {
		t.Fatalf("blocked read as lifecycle/unknown conclusion %s", got)
	}
	if got != BusyReadingBlocked {
		t.Fatalf("ReadBusy(blocked) = %s, want blocked", got)
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

// TestReadBusy_EventSourceIsHintOnly pins P1a: a SourceEvent observation
// contributes a busy/idle hint but never a Live()/Absent()-derived answer,
// even when its axes claim dead+current.
func TestReadBusy_EventSourceIsHintOnly(t *testing.T) {
	cases := []struct {
		name string
		obs  EndpointStatus
		want BusyReading
	}{
		{"event busy hint", EndpointStatus{Lifecycle: LifecycleUnknown, Freshness: FreshnessUnknown, Activity: ActivityBusy, Source: SourceEvent}, BusyReadingHeld},
		{"event idle hint", EndpointStatus{Lifecycle: LifecycleUnknown, Freshness: FreshnessUnknown, Activity: ActivityIdle, Source: SourceEvent}, BusyReadingIdle},
		{"event blocked hint", EndpointStatus{Lifecycle: LifecycleUnknown, Freshness: FreshnessUnknown, Activity: ActivityBlocked, Source: SourceEvent}, BusyReadingBlocked},
		{"event unknown hint", EndpointStatus{Lifecycle: LifecycleUnknown, Freshness: FreshnessUnknown, Activity: ActivityUnknown, Source: SourceEvent}, BusyReadingUnknown},
		{"event claiming dead+current stays a busy hint", EndpointStatus{Lifecycle: LifecycleDead, Responsiveness: Responsive, Freshness: FreshnessCurrent, Activity: ActivityBusy, Source: SourceEvent}, BusyReadingHeld},
		{"event claiming dead+current with unknown activity", EndpointStatus{Lifecycle: LifecycleDead, Responsiveness: Responsive, Freshness: FreshnessCurrent, Activity: ActivityUnknown, Source: SourceEvent}, BusyReadingUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.obs.Absent() || tc.obs.Live() {
				t.Fatalf("event fixture must never be Live()/Absent(): %s", tc.obs)
			}
			got := ReadBusy(tc.obs)
			if got == BusyReadingDead {
				t.Fatalf("ReadBusy(%s) concluded dead from an event source", tc.obs)
			}
			if got != tc.want {
				t.Fatalf("ReadBusy(%s) = %s, want %s", tc.obs, got, tc.want)
			}
		})
	}
}

// TestBusyReading_StringAndValid enters every String() arm and both Valid()
// outcomes, including the invalid zero value and an out-of-range value.
func TestBusyReading_StringAndValid(t *testing.T) {
	cases := []struct {
		r     BusyReading
		str   string
		valid bool
	}{
		{BusyReadingInvalid, "invalid", false},
		{BusyReadingHeld, "held", true},
		{BusyReadingIdle, "idle", true},
		{BusyReadingUnknown, "unknown", true},
		{BusyReadingBlocked, "blocked", true},
		{BusyReadingDead, "dead", true},
		{BusyReading(200), "invalid", false},
	}
	for _, tc := range cases {
		if got := tc.r.String(); got != tc.str {
			t.Errorf("BusyReading(%d).String() = %q, want %q", uint8(tc.r), got, tc.str)
		}
		if got := tc.r.Valid(); got != tc.valid {
			t.Errorf("BusyReading(%d).Valid() = %v, want %v", uint8(tc.r), got, tc.valid)
		}
	}
}
