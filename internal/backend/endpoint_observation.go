// Package backend provides terminal/session resolution, session adapters
// (tmux, herdr, zellij, cmux, orca), worktree pool management, and home tag
// helpers.
//
// Endpoint observation is the typed, orthogonal runtime diagnostic of one
// exact bound endpoint (BEO-16/P1a). It is NOT Task lifecycle truth: the
// canonical Task phase lives in taskauthority, and observation is only a
// recovery/readiness input. The orthogonal dimensions keep a diagnostic
// reading from being mistaken for an authoritative Task or endpoint decision:
//
//	unknown != idle, unknown != dead, unresponsive != dead, starting != dead,
//	stale != dead.
//
// A backend adapter reports only what it directly observes: lifecycle and
// responsiveness from its structured probe, an empty Incarnation, and
// FreshnessUnknown — an adapter cannot attest the opaque launch incarnation.
// Freshness current-ness and authoritative absence are concluded ONLY by Fleet
// after matching the observation against the exact canonical binding/generation/
// revision/fence (see fleet.authorizeObservation). An adapter probe therefore
// never yields an Absent()/Live() reading on its own.
package backend

import (
	"errors"
	"fmt"
	"time"
)

// LifecycleState is the authoritative endpoint-lifecycle axis. These are the
// only values that encode whether an endpoint may be considered for recovery.
type LifecycleState uint8

const (
	LifecycleInvalid LifecycleState = iota
	LifecycleStarting
	LifecycleAlive
	LifecycleDead
	LifecycleUnknown
)

func (s LifecycleState) String() string {
	switch s {
	case LifecycleStarting:
		return "starting"
	case LifecycleAlive:
		return "alive"
	case LifecycleDead:
		return "dead"
	case LifecycleUnknown:
		return "unknown"
	default:
		return "invalid"
	}
}

func (s LifecycleState) Valid() bool {
	return s >= LifecycleStarting && s <= LifecycleUnknown
}

// Responsiveness describes the transport/command health of an observation.
// It is strictly diagnostic: an unresponsive reading is never dead.
type Responsiveness uint8

const (
	ResponsivenessInvalid Responsiveness = iota
	Responsive
	Unresponsive
	ResponsivenessUnknown
)

func (r Responsiveness) String() string {
	switch r {
	case Responsive:
		return "responsive"
	case Unresponsive:
		return "unresponsive"
	case ResponsivenessUnknown:
		return "unknown"
	default:
		return "invalid"
	}
}

func (r Responsiveness) Valid() bool {
	return r >= Responsive && r <= ResponsivenessUnknown
}

// Freshness describes how confidently an observation matches the exact bound
// incarnation/cursor of the endpoint. Stale observations are diagnostic only
// and must never be used to dispose, teardown, relaunch, or replace.
//
// Backend adapters always report FreshnessUnknown: only Fleet may conclude
// current after matching the observation against the exact canonical binding.
type Freshness uint8

const (
	FreshnessInvalid Freshness = iota
	FreshnessCurrent
	FreshnessStale
	FreshnessUnknown
)

func (f Freshness) String() string {
	switch f {
	case FreshnessCurrent:
		return "current"
	case FreshnessStale:
		return "stale"
	case FreshnessUnknown:
		return "unknown"
	default:
		return "invalid"
	}
}

func (f Freshness) Valid() bool {
	return f >= FreshnessCurrent && f <= FreshnessUnknown
}

// Activity is an attention/activity signal, never a Task phase and never an
// endpoint death. `blocked` is a diagnostic attention state only.
type Activity uint8

const (
	ActivityInvalid Activity = iota
	ActivityBusy
	ActivityIdle
	ActivityBlocked
	ActivityUnknown
)

func (a Activity) String() string {
	switch a {
	case ActivityBusy:
		return "busy"
	case ActivityIdle:
		return "idle"
	case ActivityBlocked:
		return "blocked"
	case ActivityUnknown:
		return "unknown"
	default:
		return "invalid"
	}
}

func (a Activity) Valid() bool {
	return a >= ActivityBusy && a <= ActivityUnknown
}

// ObservationSource records how an observation was produced. Only a structured
// probe (or a Fleet-derived conclusion) is a trustworthy decision input.
type ObservationSource uint8

const (
	SourceInvalid ObservationSource = iota
	SourceProbe
	SourceDerived
)

func (s ObservationSource) String() string {
	switch s {
	case SourceProbe:
		return "probe"
	case SourceDerived:
		return "derived"
	default:
		return "invalid"
	}
}

func (s ObservationSource) Valid() bool {
	return s >= SourceProbe && s <= SourceDerived
}

// EndpointRef identifies one exact bound endpoint for a typed probe. Incarnation
// is the opaque launch-operation provenance token minted and persisted by Fleet
// (BEO-16/P1a). An adapter carries the requested reference but never attests the
// incarnation itself; the field is populated by Fleet's freshness authorization.
type EndpointRef struct {
	Backend     string
	Handle      string
	Incarnation string
}

// EndpointObservation is the typed, orthogonal runtime diagnostic of one exact
// bound endpoint. Only a Fleet-authorized observation (freshness current plus a
// lifecycle reading) may qualify for recovery/readiness; an adapter probe on its
// own is fresh unknown and never Live()/Absent().
type EndpointObservation struct {
	Lifecycle      LifecycleState
	Responsiveness Responsiveness
	Freshness      Freshness
	Activity       Activity
	Source         ObservationSource
	ObservedAt     time.Time
	Incarnation    string
	Detail         string
}

// Valid reports whether every orthogonal axis holds a valid (non-zero) value.
func (o EndpointObservation) Valid() bool {
	return o.Lifecycle.Valid() && o.Responsiveness.Valid() &&
		o.Freshness.Valid() && o.Activity.Valid() && o.Source.Valid()
}

// EndpointObservationState is the coarse lifecycle summary of an observation,
// retained as a diagnostic summary. Policy decisions use the typed axes and the
// Live()/Absent() guards; a coarse state is never a boolean "Alive" policy.
type EndpointObservationState uint8

const (
	EndpointObservationInvalid EndpointObservationState = iota
	EndpointAlive
	EndpointStarting
	EndpointUnresponsive
	EndpointDead
	EndpointUnknown
	EndpointStaleIdentity
	EndpointUnresolved
)

func (s EndpointObservationState) String() string {
	switch s {
	case EndpointAlive:
		return "alive"
	case EndpointStarting:
		return "starting"
	case EndpointUnresponsive:
		return "unresponsive"
	case EndpointDead:
		return "dead"
	case EndpointUnknown:
		return "unknown"
	case EndpointStaleIdentity:
		return "stale-identity"
	case EndpointUnresolved:
		return "unresolved"
	default:
		return "invalid"
	}
}

func (s EndpointObservationState) Valid() bool {
	return s >= EndpointAlive && s <= EndpointUnresolved
}

// State derives the coarse lifecycle summary from the orthogonal axes.
func (o EndpointObservation) State() EndpointObservationState {
	switch o.Lifecycle {
	case LifecycleAlive:
		return EndpointAlive
	case LifecycleStarting:
		return EndpointStarting
	case LifecycleDead:
		return EndpointDead
	default:
		if o.Freshness == FreshnessStale {
			return EndpointStaleIdentity
		}
		if o.Responsiveness == Unresponsive {
			return EndpointUnresponsive
		}
		if o.Source == SourceDerived {
			return EndpointUnresolved
		}
		return EndpointUnknown
	}
}

// Alive returns the coarse summary true for a confirmed-live reading. It is a
// diagnostic helper only: "false" here means "not proven live", never "dead".
func (o EndpointObservation) Alive() bool { return o.Lifecycle == LifecycleAlive }

// Live reports a confirmed-alive, current, exact-binding reading and is the
// only positive readiness condition. It is true only after Fleet authorized
// freshness as current against the exact canonical binding.
func (o EndpointObservation) Live() bool {
	return o.Lifecycle == LifecycleAlive && o.Freshness == FreshnessCurrent
}

// Absent reports structured authoritative absence: dead + current + a trusted
// source. This is the ONLY condition that may qualify for recovery, and only
// Fleet can authorize freshness current; an adapter probe alone is never absent.
func (o EndpointObservation) Absent() bool {
	return o.Lifecycle == LifecycleDead && o.Freshness == FreshnessCurrent &&
		(o.Source == SourceProbe || o.Source == SourceDerived)
}

// String renders a compact diagnostic summary.
func (o EndpointObservation) String() string {
	return fmt.Sprintf("lifecycle=%s responsiveness=%s freshness=%s activity=%s source=%s incarnation=%q detail=%q",
		o.Lifecycle, o.Responsiveness, o.Freshness, o.Activity, o.Source, o.Incarnation, o.Detail)
}

func observedNow() time.Time { return time.Now().UTC() }

// ObservationFromProbeError maps a probe error to the typed contract. A
// structured ErrPaneNotFound is evidence of endpoint absence (lifecycle dead)
// but — because an adapter cannot attest the opaque launch incarnation — the
// observation is still fresh unknown and is NOT Absent() on its own. Every
// operational failure (timeout, malformed response, permission, missing binary,
// server/socket failure) is unresponsive/unknown and never dead.
func ObservationFromProbeError(err error) EndpointObservation {
	obs := EndpointObservation{
		Lifecycle:      LifecycleUnknown,
		Responsiveness: Responsive,
		Freshness:      FreshnessUnknown,
		Activity:       ActivityUnknown,
		Source:         SourceProbe,
		ObservedAt:     observedNow(),
	}
	if err == nil {
		obs.Detail = "probe returned nil error without a structured observation"
		return obs
	}
	if errors.Is(err, ErrPaneNotFound) {
		obs.Lifecycle = LifecycleDead
		obs.Responsiveness = Responsive
		obs.Detail = err.Error()
		return obs
	}
	obs.Responsiveness = Unresponsive
	obs.Detail = err.Error()
	return obs
}

// ObserveEndpoint produces the raw typed observation of one endpoint from its
// handle. It never fabricates freshness or incarnation: the adapter reports
// lifecycle/responsiveness only, FreshnessUnknown, and empty Incarnation. It
// never returns an Absent() (recovery-eligible) observation because it cannot
// attest the exact bound incarnation/generation/fence. Callers keep the raw
// observation as diagnostic input and pass it to Fleet's freshness
// authorization (authorizeObservation in internal/fleet) for a policy decision.
func ObserveEndpoint(bk Backend, handle string) EndpointObservation {
	ref := EndpointRef{Handle: handle}
	obs := EndpointObservation{
		Lifecycle:      LifecycleUnknown,
		Responsiveness: ResponsivenessUnknown,
		Freshness:      FreshnessUnknown,
		Activity:       ActivityUnknown,
		Source:         SourceProbe,
		ObservedAt:     observedNow(),
	}
	if bk == nil || handle == "" {
		obs.Source = SourceDerived
		obs.Detail = "endpoint identity is incomplete"
		return obs
	}

	if aware, ok := bk.(AgentAwareBackend); ok {
		paneAlive, agentAlive, err := aware.CheckAgentAlive(handle)
		if err != nil {
			return reflectError(ref, err)
		}
		return reflectLiveness(ref, paneAlive, agentAlive)
	}
	if prober, ok := bk.(endpointProber); ok {
		alive, agentAlive, err := prober.probeEndpoint(handle)
		if err != nil {
			return reflectError(ref, err)
		}
		return reflectLiveness(ref, alive, agentAlive)
	}
	if checker, ok := bk.(endpointAliveChecker); ok {
		paneAlive, err := checker.CheckAlive(handle)
		if err != nil {
			return reflectError(ref, err)
		}
		if paneAlive {
			// A non-agent-aware structured backend cannot recognize an agent;
			// its best confirmed signal is pane liveness (freshness still unknown
			// until Fleet authorizes it).
			obs.Lifecycle = LifecycleAlive
			obs.Responsiveness = Responsive
			return obs
		}
		obs.Responsiveness = Responsive
		obs.Detail = "pane absent without authoritative absence error"
		return obs
	}
	// No structured probe surface: the endpoint liveness is unknown (the backend
	// exists but provides no authoritative probe). This is not an unresolved
	// backend identity.
	obs.Responsiveness = ResponsivenessUnknown
	obs.Detail = "backend has no structured probe surface"
	return obs
}

func reflectError(ref EndpointRef, err error) EndpointObservation {
	obs := ObservationFromProbeError(err)
	obs.ObservedAt = observedNow()
	return obs
}

func reflectLiveness(ref EndpointRef, paneAlive, agentAlive bool) EndpointObservation {
	obs := EndpointObservation{
		Lifecycle:      LifecycleUnknown,
		Responsiveness: Responsive,
		Freshness:      FreshnessUnknown,
		Activity:       ActivityUnknown,
		Source:         SourceProbe,
		ObservedAt:     observedNow(),
	}
	switch {
	case paneAlive && agentAlive:
		obs.Lifecycle = LifecycleAlive
	case paneAlive:
		obs.Lifecycle = LifecycleStarting
		obs.Detail = "pane exists but no recognized live agent"
	default:
		obs.Lifecycle = LifecycleUnknown
		obs.Detail = "probe returned pane-absent without authoritative error"
	}
	return obs
}

// endpointProber is the optional structured probe surface a session adapter
// may implement. It returns (alive, agentAlive, nil) for confirmed states and
// (_, _, ErrPaneNotFound) for structured authoritative absence; any
// operational failure returns the operational error (never ErrPaneNotFound).
type endpointProber interface {
	probeEndpoint(handle string) (alive bool, agentAlive bool, err error)
}

// endpointAliveChecker is the structured probe surface implemented by the five
// session adapters: it distinguishes authoritative absence (ErrPaneNotFound)
// from operational failure.
type endpointAliveChecker interface {
	CheckAlive(string) (bool, error)
}
