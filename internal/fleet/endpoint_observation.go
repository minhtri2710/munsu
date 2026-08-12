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

	SourceInvalid    = backend.SourceInvalid
	SourceProbe      = backend.SourceProbe
	SourceLegacyBool = backend.SourceLegacyBool
	SourceDerived    = backend.SourceDerived
)
