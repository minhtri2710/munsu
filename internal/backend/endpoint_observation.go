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
//	stale != dead, plain legacy-bool false != dead.
//
// Only a structured authoritative absence of the exact bound endpoint, plus
// valid generation/revision/fence checks, may qualify for Fleet recovery.
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

// ObservationSource records how an observation was produced. Legacy-bool
// readings are retained only as diagnostic pane-presence; they are never
// authoritative alive/dead and never qualify for recovery.
type ObservationSource uint8

const (
	SourceInvalid ObservationSource = iota
	SourceProbe
	SourceLegacyBool
	SourceDerived
)

func (s ObservationSource) String() string {
	switch s {
	case SourceProbe:
		return "probe"
	case SourceLegacyBool:
		return "legacy-bool"
	case SourceDerived:
		return "derived"
	default:
		return "invalid"
	}
}

func (s ObservationSource) Valid() bool {
	return s >= SourceProbe && s <= SourceDerived
}

// EndpointRef identifies one exact bound endpoint and is the identity carried
// by typed probes. Incarnation is the opaque generation-bound identity minted
// by Fleet at launch and persisted by taskauthority; it is used to reject
// stale/foreign observations (freshness).
type EndpointRef struct {
	Backend     string
	Handle      string
	Incarnation string
}

// EndpointObservation is the typed, orthogonal runtime diagnostic of one
// exact bound endpoint. Lifecycle is the only axis that may qualify for
// recovery (and only when dead with a current, exact-binding reading).
// Responsiveness, Freshness, Activity, Source, ObservedAt, Incarnation and
// Detail are diagnostic provenance/decision inputs — none of them are Task
// phase and none may mutate canonical Task lifecycle on their own.
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

// Valid reports whether every orthogonal axis holds a valid (non-zero)
// value, which every produced observation must.
func (o EndpointObservation) Valid() bool {
	return o.Lifecycle.Valid() && o.Responsiveness.Valid() &&
		o.Freshness.Valid() && o.Activity.Valid() && o.Source.Valid()
}

// EndpointObservationState is the coarse lifecycle summary of an observation,
// retained as a probe-result carrier and diagnostic summary (BEO-16 internal
// cutover). Recovery and readiness policy decisions must use the typed
// Lifecycle/Responsiveness/Freshness/Activity axes and the crossing guards
// below (dead requires an authoritative structured absence); a coarse state
// is never interpreted as a boolean "Alive" policy.
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

// State derives the coarse lifecycle summary from the orthogonal axes. It is
// diagnostic output, not policy truth: the authoritative decision input is
// the structured Live()/Absent()/Lifecycle helpers.
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

// Alive returns the coarse summary true for a confirmed-live, current, exact
// reading. It is a diagnostic helper only: "false" here means "not proven
// live", never "dead" and never a recovery trigger.
func (o EndpointObservation) Alive() bool { return o.Lifecycle == LifecycleAlive }

// Live reports a confirmed-alive, current, exact-binding reading. Only this
// is a positive readiness condition; starting/unknown/stale are not live.
func (o EndpointObservation) Live() bool {
	return o.Lifecycle == LifecycleAlive && o.Freshness == FreshnessCurrent
}

// Absent reports structured authoritative absence: dead + current + a
// trustworthy source. This is the ONLY condition that may qualify for
// recovery. Everything else fails closed.
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

// CrossCheckBinding marks an observation whose bound identity does not match
// the expected exact endpoint identity as unknown/stale/unknown: a mismatch
// (wrong backend, wrong handle, or wrong incarnation) must never remain
// authoritative. expectBackend/expectHandle/expectIncarnation are the exact
// bound identities; empty strings are not compared.
func (o EndpointObservation) CrossCheckBinding(expectBackend, expectHandle, expectIncarnation string) EndpointObservation {
	if o.Source != SourceProbe && o.Source != SourceDerived {
		return o
	}
	mismatch := ""
	if expectIncarnation != "" && o.Incarnation != expectIncarnation {
		mismatch = "incarnation"
	} else if expectHandle != "" && o.Incarnation == "" {
		// probe carried no incarnation for a bound endpoint that expects one
		mismatch = "incarnation-absent"
	}
	if mismatch == "" {
		return o
	}
	o.Lifecycle = LifecycleUnknown
	o.Freshness = FreshnessStale
	o.Activity = ActivityUnknown
	if o.Detail == "" {
		o.Detail = mismatch + " mismatch"
	} else {
		o.Detail = o.Detail + "; " + mismatch + " mismatch"
	}
	return o
}

// ObservationFromProbeError maps a probe error to the typed orthogonal
// contract. Only a structured ErrPaneNotFound (exact authoritative absence of
// the bound endpoint) becomes dead/current; every operational failure
// (timeout, malformed response, permission, missing binary, socket/CLI
// failure) becomes unknown/unresponsive/unknown and never dead.
func ObservationFromProbeError(endpoint EndpointRef, err error) EndpointObservation {
	obs := EndpointObservation{
		Lifecycle:      LifecycleUnknown,
		Responsiveness: Responsive,
		Freshness:      FreshnessCurrent,
		Activity:       ActivityUnknown,
		Source:         SourceProbe,
		Incarnation:    endpoint.Incarnation,
		ObservedAt:     observedNow(),
	}
	if err == nil {
		obs.Detail = "probe returned nil error without a structured observation"
		return obs
	}
	if errors.Is(err, ErrPaneNotFound) {
		obs.Lifecycle = LifecycleDead
		obs.Freshness = FreshnessCurrent
		obs.Detail = err.Error()
		return obs
	}
	obs.Responsiveness = Unresponsive
	obs.Freshness = FreshnessUnknown
	obs.Detail = fmt.Sprintf("probing %s/%s: %v", endpoint.Backend, endpoint.Handle, err)
	return obs
}

// ObserveBackendEndpoint produces the typed observation of one endpoint
// handle without an incarnation (incarnation unknown). Callers with the exact
// bound identity should use ObserveBoundEndpoint so the probe freshness
// cross-check can reject stale/foreign observations.
func ObserveBackendEndpoint(bk Backend, handle string) EndpointObservation {
	return ObserveBoundEndpoint(bk, handle, "")
}

// ObserveBoundEndpoint produces the typed orthogonal observation of one exact
// bound endpoint from its full identity. The candidate reflection carries the
// observed backend/handle/incarnation (when known) and is freshness
// cross-checked against the exact bound identities. An authoritative dead
// reading is only produced from a structured ErrPaneNotFound; operational
// failures become unresponsive/unknown.
//
// expectedIncarnation is the opaque value persisted by taskauthority for the
// exact binding. When empty, no cross-check on incarnation is performed
// (freshness stays as reported by the adapter).
func ObserveBoundEndpoint(bk Backend, handle, expectedIncarnation string) EndpointObservation {
	ref := EndpointRef{Handle: handle, Incarnation: expectedIncarnation}
	if bk == nil || handle == "" {
		return EndpointObservation{
			Lifecycle:      LifecycleUnknown,
			Responsiveness: ResponsivenessUnknown,
			Freshness:      FreshnessUnknown,
			Activity:       ActivityUnknown,
			Source:         SourceDerived,
			Incarnation:    expectedIncarnation,
			ObservedAt:     observedNow(),
			Detail:         "endpoint identity is incomplete",
		}
	}

	if aware, ok := bk.(AgentAwareBackend); ok {
		paneAlive, agentAlive, err := aware.CheckAgentAlive(handle)
		if err != nil {
			return ObservationFromProbeError(ref, err)
		}
		return reflectAgent(ref, paneAlive, agentAlive)
	}
	if prober, ok := bk.(endpointProber); ok {
		alive, agentAlive, err := prober.probeEndpoint(handle)
		if err != nil {
			return ObservationFromProbeError(ref, err)
		}
		return reflectAgent(ref, alive, agentAlive)
	}
	if checker, ok := bk.(endpointAliveChecker); ok {
		paneAlive, err := checker.CheckAlive(handle)
		if err != nil {
			return ObservationFromProbeError(ref, err)
		}
		if paneAlive {
			// A non-agent-aware structured backend cannot recognize an agent;
			// its best confirmed-live signal is exact pane liveness.
			return EndpointObservation{
				Lifecycle:      LifecycleAlive,
				Responsiveness: Responsive,
				Freshness:      FreshnessCurrent,
				Activity:       ActivityUnknown,
				Source:         SourceProbe,
				Incarnation:    expectedIncarnation,
				ObservedAt:     observedNow(),
			}
		}
		return EndpointObservation{
			Lifecycle:      LifecycleUnknown,
			Responsiveness: Responsive,
			Freshness:      FreshnessUnknown,
			Activity:       ActivityUnknown,
			Source:         SourceProbe,
			Incarnation:    expectedIncarnation,
			ObservedAt:     observedNow(),
			Detail:         "pane absent without authoritative absence error",
		}
	}
	// Legacy bool-only backend: retain pane presence as diagnostic only.
	// true AND false both map to lifecycle unknown — never authoritative
	// alive, and false is never dead (BEO-16 no-bool-policy cutover).
	alive := bk.Alive(handle)
	detail := "legacy bool probe (pane present)"
	if !alive {
		detail = "legacy bool probe (pane absent) — not authoritative absence"
	}
	return EndpointObservation{
		Lifecycle:      LifecycleUnknown,
		Responsiveness: ResponsivenessUnknown,
		Freshness:      FreshnessUnknown,
		Activity:       ActivityUnknown,
		Source:         SourceLegacyBool,
		Incarnation:    expectedIncarnation,
		ObservedAt:     observedNow(),
		Detail:         detail,
	}
}

func reflectAgent(ref EndpointRef, paneAlive, agentAlive bool) EndpointObservation {
	obs := EndpointObservation{
		Lifecycle:      LifecycleUnknown,
		Responsiveness: Responsive,
		Freshness:      FreshnessCurrent,
		Activity:       ActivityUnknown,
		Source:         SourceProbe,
		Incarnation:    ref.Incarnation,
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

// endpointAliveChecker is the structured probe surface implemented by tmux
// and herdr: it distinguishes authoritative absence (ErrPaneNotFound) from
// operational failure.
type endpointAliveChecker interface {
	CheckAlive(string) (bool, error)
}
