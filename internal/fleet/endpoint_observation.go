package fleet

import "github.com/minhtri2710/munsu/internal/backend"

type EndpointObservationState = backend.EndpointObservationState
type EndpointStatus = backend.EndpointObservation

// Typed orthogonal observation axes (BEO-16/P1a). Fleet policy uses these
// typed Lifecycle/Responsiveness/Freshness/Activity/Source dimensions for
// recovery and readiness decisions, never a boolean "Alive" policy.
type LifecycleState = backend.LifecycleState
type Responsiveness = backend.Responsiveness
type Freshness = backend.Freshness
type Activity = backend.Activity
type ObservationSource = backend.ObservationSource

const (
	EndpointObservationInvalid = backend.EndpointObservationInvalid
	EndpointAlive              = backend.EndpointAlive
	EndpointStarting           = backend.EndpointStarting
	EndpointUnresponsive       = backend.EndpointUnresponsive
	EndpointDead               = backend.EndpointDead
	EndpointUnknown            = backend.EndpointUnknown
	EndpointStaleIdentity      = backend.EndpointStaleIdentity
	EndpointUnresolved         = backend.EndpointUnresolved

	LifecycleInvalid  = backend.LifecycleInvalid
	LifecycleStarting = backend.LifecycleStarting
	LifecycleAlive    = backend.LifecycleAlive
	LifecycleDead     = backend.LifecycleDead
	LifecycleUnknown  = backend.LifecycleUnknown

	ResponsivenessInvalid = backend.ResponsivenessInvalid
	Responsive            = backend.Responsive
	Unresponsive          = backend.Unresponsive
	ResponsivenessUnknown = backend.ResponsivenessUnknown

	FreshnessInvalid = backend.FreshnessInvalid
	FreshnessCurrent = backend.FreshnessCurrent
	FreshnessStale   = backend.FreshnessStale
	FreshnessUnknown = backend.FreshnessUnknown

	ActivityInvalid = backend.ActivityInvalid
	ActivityBusy    = backend.ActivityBusy
	ActivityIdle    = backend.ActivityIdle
	ActivityBlocked = backend.ActivityBlocked
	ActivityUnknown = backend.ActivityUnknown

	SourceInvalid = backend.SourceInvalid
	SourceProbe   = backend.SourceProbe
	SourceDerived = backend.SourceDerived
	SourceEvent   = backend.SourceEvent
)

// exactEndpointProof carries the exact canonical identities Fleet must match
// before an adapter observation may be concluded fresh/current (BEO-16/P1a).
// backend/handle identify the exact bound endpoint; incarnation is the opaque
// launch-operation provenance token; leaseID/fenceToken are the canonical
// launch reservation fences; generation/revision are the exact aggregate
// preconditions revalidated at authorization time.
type exactEndpointProof struct {
	backend     string
	handle      string
	incarnation string
	leaseID     string
	fenceToken  string
	generation  uint64
	revision    uint64
	// acquired marks an explicitly recorded acquisition receipt tying the
	// exact handle to the incarnation: the CreatedEndpoint returned by the
	// backend create (fresh in-process acquisition), the durable
	// AcquiredEndpoint, or the canonical EndpointBinding evidence. P1a
	// adapters cannot attest incarnation, so positive liveness is NEVER
	// concluded without it; a probe of an expected handle with no acquisition
	// record (e.g. a mutable .meta projection) fails closed.
	acquired bool
}

// authorized reports whether the proof is complete enough to conclude
// current. Every identity field must be non-empty; an incomplete proof fails
// closed. generation/revision are required separately by current().
func (p exactEndpointProof) authorized() bool {
	return p.backend != "" && p.handle != "" && p.incarnation != "" && p.leaseID != "" && p.fenceToken != ""
}

// current reports whether the proof carries the revalidated current
// generation/revision of the canonical aggregate. A caller that cannot
// revalidate against task authority (no canonical access) fails closed.
func (p exactEndpointProof) current() bool { return p.generation != 0 && p.revision != 0 }

// demoteObservation fails an observation closed to unknown/stale: it never
// grants Absent()/Live() and never carries an incarnation.
func demoteObservation(raw EndpointStatus, reason string) EndpointStatus {
	raw.Lifecycle = LifecycleUnknown
	raw.Freshness = FreshnessStale
	raw.Activity = ActivityUnknown
	raw.Incarnation = ""
	raw.Detail = reason
	return raw
}

// authorizeAbsence is Fleet's NEGATIVE authorization authority (BEO-16/P1a).
// Only a narrowly classified exact structured target absence — the exact
// bound handle probed dead with a trusted probe/derived source, against a
// complete canonical proof revalidated under current generation/revision —
// may conclude Absent(). Anything else fails closed to unknown/stale and is
// never Absent()/Live(). Absence does not require an acquisition receipt: a
// dead reading of the exact bound handle is absent regardless of which
// incarnation previously owned it.
func authorizeAbsence(raw EndpointStatus, proof exactEndpointProof) EndpointStatus {
	if !proof.authorized() || !proof.current() {
		return demoteObservation(raw, "cannot authorize absence: incomplete or stale canonical proof (backend/handle/incarnation/lease/fence/current-generation-revision required)")
	}
	if raw.Lifecycle != LifecycleDead || (raw.Source != SourceProbe && raw.Source != SourceDerived) {
		return demoteObservation(raw, "observation is not a narrow exact structured target absence")
	}
	raw.Freshness = FreshnessCurrent
	raw.Incarnation = proof.incarnation
	return raw
}

// authorizeLive is Fleet's POSITIVE authorization authority (BEO-16/P1a). Raw
// probe liveness is promoted to Live() ONLY for a trusted probe/derived source
// and with explicit acquisition/creation evidence tied to the incarnation
// (proof.acquired): P1a adapters cannot attest incarnation, so a probe of an
// expected handle with no acquisition record is never promoted — a reused handle
// cannot become Live() merely because it matches the expected strings (ABA
// fail-closed). An event-derived SourceEvent reading is rejected outright because
// event hints are never lifecycle truth, symmetric with authorizeAbsence. The
// proof must also be complete and revalidated under current generation/revision.
func authorizeLive(raw EndpointStatus, proof exactEndpointProof) EndpointStatus {
	if !proof.authorized() || !proof.current() {
		return demoteObservation(raw, "cannot authorize liveness: incomplete or stale canonical proof (backend/handle/incarnation/lease/fence/current-generation-revision required)")
	}
	if !proof.acquired {
		return demoteObservation(raw, "cannot authorize liveness: no explicit acquisition evidence tied to the incarnation")
	}
	if raw.Lifecycle != LifecycleAlive || raw.Responsiveness != Responsive {
		return demoteObservation(raw, "raw probe is not alive/responsive")
	}
	if raw.Source != SourceProbe && raw.Source != SourceDerived {
		return demoteObservation(raw, "raw liveness is not from a trusted probe/derived source")
	}
	raw.Freshness = FreshnessCurrent
	raw.Incarnation = proof.incarnation
	return raw
}
