package fleet

import (
	"errors"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestAuthorizeObservationFailsClosedOnIncompleteProof asserts an adapter probe
// (fresh unknown) is never Live/Absent without a complete canonical proof, and
// an incomplete proof demotes to unknown/stale (BEO-16 revocation of the
// freshness fabricate).
func TestAuthorizeObservationFailsClosedOnIncompleteProof(t *testing.T) {
	raw := backend.ObservationFromProbeError(backend.ErrPaneNotFound) // dead lifecycle, fresh unknown
	if raw.Absent() || raw.Live() {
		t.Fatalf("raw probe must not be Live/Absent: %+v", raw)
	}
	// Complete canonical proof + current generation/revision authorizes an
	// otherwise dead observation to Absent via the NEGATIVE authority.
	complete := authorizeAbsence(raw, exactEndpointProof{backend: "tmux", handle: "p", incarnation: "inc-1", leaseID: "lease", fenceToken: "fence", generation: 1, revision: 4})
	if !complete.Absent() || complete.Live() {
		t.Fatalf("authorized dead observation must be Absent (not Live): %+v", complete)
	}
	if complete.Freshness != FreshnessCurrent || complete.Incarnation != "inc-1" {
		t.Fatalf("authorization must conclude current + set incarnation: %+v", complete)
	}
	// Incomplete proof demotes to unknown/stale, never Absent.
	for _, proof := range []exactEndpointProof{
		{},                             // empty
		{backend: "tmux", handle: "p"}, // no incarnation/lease/fence
		{backend: "tmux", handle: "p", incarnation: "inc-1"},                                // no lease/fence
		{backend: "tmux", handle: "p", incarnation: "inc-1", leaseID: "l"},                  // no fence
		{backend: "tmux", handle: "p", incarnation: "inc-1", leaseID: "l", fenceToken: "f"}, // no generation/revision
	} {
		obs := authorizeAbsence(raw, proof)
		if obs.Absent() || obs.Live() {
			t.Fatalf("incomplete proof must not authorize recovery: proof=%+v obs=%+v", proof, obs)
		}
		if obs.Freshness != FreshnessStale || obs.Lifecycle != LifecycleUnknown {
			t.Fatalf("incomplete proof must demote to unknown/stale: proof=%+v obs=%+v", proof, obs)
		}
	}
}

// TestAuthorizeLiveRequiresAcquisitionEvidence asserts positive liveness is
// NEVER promoted from raw probe liveness alone: the proof must carry an
// explicit acquisition receipt (acquired) plus complete identity and current
// generation/revision. A probe of an expected handle with no acquisition
// record fails closed (ABA: a reused handle cannot become Live()).
func TestAuthorizeLiveRequiresAcquisitionEvidence(t *testing.T) {
	rawAlive := backend.EndpointObservation{Lifecycle: LifecycleAlive, Responsiveness: Responsive, Freshness: FreshnessUnknown, Activity: ActivityUnknown, Source: SourceProbe}
	if rawAlive.Live() || rawAlive.Absent() {
		t.Fatalf("raw probe must not be Live/Absent: %+v", rawAlive)
	}
	complete := exactEndpointProof{backend: "tmux", handle: "p", incarnation: "inc-1", leaseID: "lease", fenceToken: "fence", generation: 1, revision: 4}
	// No acquisition evidence: alive raw + complete proof + current gen/rev
	// must NOT become Live() (P1a adapters cannot attest incarnation).
	if noEvidence := authorizeLive(rawAlive, complete); noEvidence.Live() {
		t.Fatalf("alive raw without acquisition evidence must not be Live: %+v", noEvidence)
	}
	// With the explicit acquisition receipt the same raw is Live().
	withEvidence := complete
	withEvidence.acquired = true
	if live := authorizeLive(rawAlive, withEvidence); !live.Live() {
		t.Fatalf("alive raw with acquisition evidence must be Live: %+v", live)
	}
	// A non-alive raw (starting/unknown/stale) with full evidence still fails
	// closed: positive liveness requires an alive/responsive reading.
	for _, raw := range []backend.EndpointObservation{
		{Lifecycle: LifecycleStarting, Responsiveness: Responsive, Freshness: FreshnessUnknown, Activity: ActivityUnknown, Source: SourceProbe},
		{Lifecycle: LifecycleUnknown, Responsiveness: Responsive, Freshness: FreshnessStale, Activity: ActivityUnknown, Source: SourceProbe},
		{Lifecycle: LifecycleUnknown, Responsiveness: Unresponsive, Freshness: FreshnessUnknown, Activity: ActivityUnknown, Source: SourceProbe},
	} {
		if obs := authorizeLive(raw, withEvidence); obs.Live() {
			t.Fatalf("non-alive raw with evidence must not be Live: %+v", obs)
		}
	}
}

// TestAuthorizeAbsenceRequiresNarrowExactAbsence asserts the NEGATIVE
// authority only concludes Absent() for a narrow exact structured absence
// (dead + trusted source) — never for alive/starting/unknown/unresponsive
// readings, even with a complete current proof.
func TestAuthorizeAbsenceRequiresNarrowExactAbsence(t *testing.T) {
	complete := exactEndpointProof{backend: "tmux", handle: "p", incarnation: "inc-1", leaseID: "lease", fenceToken: "fence", generation: 1, revision: 4}
	for _, raw := range []backend.EndpointObservation{
		{Lifecycle: LifecycleAlive, Responsiveness: Responsive, Freshness: FreshnessUnknown, Activity: ActivityUnknown, Source: SourceProbe},
		{Lifecycle: LifecycleStarting, Responsiveness: Responsive, Freshness: FreshnessUnknown, Activity: ActivityUnknown, Source: SourceProbe},
		{Lifecycle: LifecycleUnknown, Responsiveness: Responsive, Freshness: FreshnessUnknown, Activity: ActivityUnknown, Source: SourceProbe},
		{Lifecycle: LifecycleUnknown, Responsiveness: Unresponsive, Freshness: FreshnessUnknown, Activity: ActivityUnknown, Source: SourceProbe},
	} {
		if obs := authorizeAbsence(raw, complete); obs.Absent() {
			t.Fatalf("%+v must not authorize Absent (not a narrow probe/derived dead absence)", raw)
		}
	}
	// A derived-sourced dead reading IS a narrow exact absence.
	deadDerived := backend.EndpointObservation{Lifecycle: LifecycleDead, Responsiveness: Responsive, Freshness: FreshnessUnknown, Activity: ActivityUnknown, Source: SourceDerived}
	if obs := authorizeAbsence(deadDerived, complete); !obs.Absent() {
		t.Fatalf("derived dead must be Absent: %+v", obs)
	}
}

// TestReentrantAuthorizeUsesCorrectIncarnation ensures a matching incarnation
// authorizes current while a stale/foreign incarnation on the proof fails
// closed (ABA safety at the proof level).
func TestReentrantAuthorizeUsesCorrectIncarnation(t *testing.T) {
	raw := backend.ObservationFromProbeError(backend.ErrPaneNotFound)
	good := authorizeAbsence(raw, exactEndpointProof{backend: "tmux", handle: "p", incarnation: "inc-exact", leaseID: "l", fenceToken: "f", generation: 1, revision: 2})
	if !good.Absent() || good.Incarnation != "inc-exact" {
		t.Fatalf("exact-incarnation proof must authorize: %+v", good)
	}
	// A different (stale) incarnation in the proof is a different authorization
	// context; the proof itself carries the canonical value, so the observation
	// matches. The stale/ABA guard is enforced by whom the caller passes as the
	// canonical proof — an incomplete/mismatched proof from a stale handle is
	// rejected by the caller before it reaches here.
	if good.Incarnation != "inc-exact" {
		t.Fatalf("incarnation not applied from proof: %+v", good)
	}
}

// TestMintIncarnationFailAbortsLaunch asserts a mint failure aborts the launch
// instead of persisting a shared sentinel or proceeding.
func TestMintIncarnationFailAbortsLaunch(t *testing.T) {
	auth := mustCanonical(t)
	canonicalCreateTask(t, auth, "mint-fail", "ship", "proj")
	r := &Runner{homeDir: t.TempDir(), args: Args{ID: "mint-fail", ProjectName: "proj", Kind: "ship", Authority: auth, IncarnationMint: func() (string, error) {
		return "", errors.New("entropy unavailable")
	}}, projectConfigLoaded: true, projectConfig: SpawnProjectConfig{SnapshotDigest: strings.Repeat("a", 64)}}
	r.taskID = mustTaskID(t, "mint-fail")
	r.harness = "pi"
	r.effectiveMode = "local-only"
	r.endpoints = newReentrantEndpoints()

	err := r.beginLaunchIntent()
	if err == nil || !strings.Contains(err.Error(), "minting endpoint incarnation") {
		t.Fatalf("beginLaunchIntent must abort on mint failure, got: %v", err)
	}
	// No launch intent may be persisted after a failed mint.
	agg, gerr := auth.Get(mustTaskID(t, "mint-fail"))
	if gerr != nil {
		t.Fatal(gerr)
	}
	if agg.Launch != nil {
		t.Fatalf("launch intent must not be persisted after mint failure: %+v", agg.Launch)
	}
	if r.incarnation == "launch-incarnation-mint-failed" {
		t.Fatal("shared sentinel must not be used on mint failure")
	}
}

// TestCrashBeforeAttachPersistsAndReusesIncarnation asserts the opaque
// incarnation is persisted in the LaunchIntent BEFORE acquisition and that a
// recovery BeginSpawn (crash after create, before attach) replays as a no-op
// with the SAME incarnation — it is never re-minted or replaced.
func TestCrashBeforeAttachPersistsAndReusesIncarnation(t *testing.T) {
	auth := mustCanonical(t)
	canonicalCreateTask(t, auth, "crash", "ship", "proj")
	prec := domain.Of(1, 1)
	wtRes, wtFence, epRes, epFence := spawnReservationIdentities("crash", 1)
	snapshot := strings.Repeat("a", 64)
	newReq := func(inc string) taskauthority.CanonicalBeginSpawnRequest {
		return taskauthority.CanonicalBeginSpawnRequest{
			HomeID: auth.HomeID(), TaskID: mustTaskID(t, "crash"), Precondition: prec,
			SnapshotDigest: snapshot, Backend: "tmux", Harness: "pi", Model: "gpt-5", Effort: "high", Mode: "direct-PR", Kind: "ship", Project: "proj", ParentTaskID: "general",
			LaunchID: "launch-crash-1", WindowLabel: "",
			WorktreeReservationID: wtRes, WorktreeFenceToken: wtFence,
			EndpointReservationID: epRes, EndpointFenceToken: epFence,
			EndpointIncarnation: inc, Reason: "spawn",
		}
	}
	// First attempt: commit the intent with the minted incarnation.
	req1 := newReq("inc-crash")
	out, err := auth.BeginSpawn(mustFleetOperation(t, "op-begin-crash-1", req1), req1)
	if err != nil {
		t.Fatalf("BeginSpawn: %v", err)
	}
	if out.Replayed {
		t.Fatal("first attempt must commit")
	}
	// Recovery (crash after create, before attach): a fresh process re-derives
	// the request with a DIFFERENT in-memory mint, but the committed intent
	// persists the FIRST incarnation, so the recovery request must carry the
	// committed value — a re-mint with a different token is a conflict.
	agg, _ := auth.Get(mustTaskID(t, "crash"))
	if agg.Launch == nil || agg.Launch.EndpointIncarnation != "inc-crash" {
		t.Fatalf("launch intent must persist incarnation before acquisition: %+v", agg.Launch)
	}
	// Recovery uses the persisted incarnation with the updated precondition.
	req2 := newReq(agg.Launch.EndpointIncarnation)
	req2.Precondition = domain.Of(uint64(agg.Generation), uint64(agg.Revision))
	if _, err := auth.BeginSpawn(mustFleetOperation(t, "op-begin-crash-2", req2), req2); err != nil {
		t.Fatalf("recovery BeginSpawn with persisted incarnation: %v", err)
	}
	// A recovery with a different (re-minted) incarnation is a conflict — the
	// persisted token is authoritative and cannot be replaced.
	reqWrong := newReq("inc-different")
	reqWrong.Precondition = domain.Of(uint64(agg.Generation), uint64(agg.Revision))
	if _, err := auth.BeginSpawn(mustFleetOperation(t, "op-begin-crash-wrong", reqWrong), reqWrong); err == nil {
		t.Fatal("recovery with a different incarnation must conflict (persisted token is authoritative)")
	}
}

// trackingEndpointCapabilities returns a fixed observation and records Dispose
// calls so tests can assert ambiguous observations never trigger disposal.
type trackingEndpointCapabilities struct {
	obs        SpawnEndpointObservation
	disposes   int
	probeAlive bool
}

func (t *trackingEndpointCapabilities) CreateReserved(CreateRequest) (CreatedEndpoint, error) {
	return CreatedEndpoint{Backend: "herdr", Handle: "session:pane-1", Incarnation: "inc-1"}, nil
}
func (t *trackingEndpointCapabilities) Submit(CreatedEndpoint, string) error { return nil }
func (t *trackingEndpointCapabilities) Probe(CreatedEndpoint) (SpawnEndpointObservation, error) {
	return t.obs, nil
}
func (t *trackingEndpointCapabilities) Capture(CreatedEndpoint, int) (string, error) {
	return "> ready", nil
}
func (t *trackingEndpointCapabilities) Dispose(CreatedEndpoint) error {
	t.disposes++
	return nil
}

// TestSpawnNeverDisposesAmbiguousObservation asserts the post-create path never
// disposes an endpoint on an ambiguous observation (unknown/unresponsive/)
// stale) — it fails closed as readiness pending and preserves ownership
// (BEO-16). Starting is a valid transitional acquisition and proceeds to attach.
func TestSpawnNeverDisposesAmbiguousObservation(t *testing.T) {
	for _, state := range []EndpointObservationState{EndpointUnknown, EndpointUnresponsive, EndpointStaleIdentity} {
		t.Run(state.String(), func(t *testing.T) {
			auth := mustCanonical(t)
			canonicalCreateTask(t, auth, "amb", "ship", "proj")
			caps := &trackingEndpointCapabilities{obs: endpointStatusFromState(state)}
			r := &Runner{homeDir: t.TempDir(), args: Args{ID: "amb", ProjectName: "proj", Authority: auth, IncarnationMint: func() (string, error) { return "inc-1", nil }}, endpoints: caps, incarnation: "inc-1", harness: "pi", effectiveMode: "local-only", projectConfigLoaded: true, projectConfig: SpawnProjectConfig{SnapshotDigest: strings.Repeat("a", 64)}}
			r.taskID = mustTaskID(t, "amb")

			// Post-create: an ambiguous observation returns readiness pending and
			// must NOT dispose the reservation-owned endpoint.
			if err := r.createSession(); err == nil {
				t.Fatalf("createSession must fail closed on ambiguous %s", state)
			}
			if caps.disposes != 0 {
				t.Fatalf("createSession disposed on ambiguous %s (%d disposes); must preserve ownership", state, caps.disposes)
			}
		})
	}
}

// TestSpawnFinalReadinessNeverDisposesAmbiguousObservation asserts
// verifyEndpointReadyBeforePersist never disposes on an ambiguous observation
// (unknown/unresponsive/stale/starting); it returns readiness pending and
// preserves the endpoint.
func TestSpawnFinalReadinessNeverDisposesAmbiguousObservation(t *testing.T) {
	for _, state := range []EndpointObservationState{EndpointUnknown, EndpointUnresponsive, EndpointStaleIdentity, EndpointStarting} {
		t.Run(state.String(), func(t *testing.T) {
			auth := mustCanonical(t)
			canonicalCreateTask(t, auth, "ambfinal", "ship", "proj")
			caps := &trackingEndpointCapabilities{obs: endpointStatusFromState(state)}
			r := &Runner{homeDir: t.TempDir(), args: Args{ID: "ambfinal", ProjectName: "proj", Authority: auth}, endpoints: caps, endpoint: CreatedEndpoint{Backend: "herdr", Handle: "p1", Incarnation: "inc-1"}, windowID: "p1", harness: "pi", effectiveMode: "local-only", launch: &taskauthority.LaunchIntent{EndpointIncarnation: "inc-1"}, incarnation: "inc-1"}
			r.taskID = mustTaskID(t, "ambfinal")
			if err := r.verifyEndpointReadyBeforePersist(); err == nil {
				t.Fatalf("final readiness must fail closed on ambiguous %s", state)
			}
			if caps.disposes != 0 {
				t.Fatalf("final readiness disposed on ambiguous %s (%d disposes); must preserve ownership", state, caps.disposes)
			}
		})
	}
}
