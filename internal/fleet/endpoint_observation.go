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
)

// exactEndpointProof carries the exact canonical identities Fleet must match
// before an adapter observation may be concluded fresh/current (BEO-16/P1a).
// backend/handle identify the exact bound endpoint; incarnation is the opaque
// launch-operation provenance token; leaseID/fenceToken are the canonical
// launch reservation fences; generation/revision are the exact aggregate
// preconditions.
type exactEndpointProof struct {
	backend     string
	handle      string
	incarnation string
	leaseID     string
	fenceToken  string
	generation  uint64
	revision    uint64
}

// authorized reports whether the proof is complete enough to conclude current.
// Every field must be non-empty (except generation/revision which are matched
// by the canonical precondition and are deliberately not required here to keep
// the proof purely about endpoint identity). An incomplete proof fails closed.
func (p exactEndpointProof) authorized() bool {
	return p.backend != "" && p.handle != "" && p.incarnation != "" && p.leaseID != "" && p.fenceToken != ""
}

// authorizeObservation is Fleet's sole freshness authority (BEO-16/P1a). An
// adapter probe reports FreshnessUnknown and an empty Incarnation — an adapter
// cannot attest the opaque launch incarnation. Only Fleet, by matching the
// observation to a complete exactEndpointProof (the exact bound
// backend/handle plus the canonical incarnation/lease/fence), may conclude
// FreshnessCurrent. On an incomplete proof the observation is demoted to
// LifecycleUnknown/FreshnessStale, carries no incarnation, and is never
// Live()/Absent().
//
// The caller probes the exact bound handle of the canonical binding, so the
// backend/handle affinity is assured before authorization; the proof's presence
// and completeness is what grants freshness.
func authorizeObservation(raw EndpointStatus, proof exactEndpointProof) EndpointStatus {
	if !proof.authorized() {
		raw.Lifecycle = LifecycleUnknown
		raw.Freshness = FreshnessStale
		raw.Activity = ActivityUnknown
		raw.Incarnation = ""
		raw.Detail = "cannot authorize freshness: incomplete canonical proof (backend/handle/incarnation/lease/fence required)"
		return raw
	}
	raw.Freshness = FreshnessCurrent
	raw.Incarnation = proof.incarnation
	return raw
}
