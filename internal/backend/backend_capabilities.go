// Capability descriptors and matrix for the five session backends (BEO-16/P1a).
//
// A descriptor records what an adapter actually provides today, plus its
// verification tier. It deliberately does not over-claim: a primitive that
// exists is not the same as canonical reservation-aware spawn being supported,
// and a native feature firstmate ships is not claimed current for munsu until
// munsu exposes it. `verified` and `experimental` are tier labels, not a
// boolean supported flag.
package backend

import "fmt"

// VerificationTier classifies how production-ready an adapter is in munsu.
type VerificationTier uint8

const (
	// TierInvalid is the zero/undefined tier.
	TierInvalid VerificationTier = iota
	// TierVerified is a production-verified session adapter (tmux, herdr).
	TierVerified
	// TierExperimental is an adapter present but not yet verified production
	// (zellij, cmux, orca).
	TierExperimental
	// TierUnsupported means the backend is not declared at all.
	TierUnsupported
)

func (t VerificationTier) String() string {
	switch t {
	case TierVerified:
		return "verified"
	case TierExperimental:
		return "experimental"
	case TierUnsupported:
		return "unsupported"
	default:
		return "invalid"
	}
}

// CapabilityAvailability states whether a specific capability is declared.
type CapabilityAvailability uint8

const (
	// CapInvalid is the zero/undefined availability.
	CapInvalid CapabilityAvailability = iota
	// CapCurrent means munsu exposes the capability today.
	CapCurrent
	// CapProposed means the capability is designed for a later stage (e.g.
	// P1b native event transport) but is not current munsu implementation.
	CapProposed
	// CapUnsupported means the capability is out of scope / not provided.
	CapUnsupported
)

func (c CapabilityAvailability) String() string {
	switch c {
	case CapCurrent:
		return "current"
	case CapProposed:
		return "proposed"
	case CapUnsupported:
		return "unsupported"
	default:
		return "invalid"
	}
}

// ProbeGranularity describes the exact resource a backend's structured probe
// proves existence/absence of. Exact authoritative dead/current is only
// meaningful at this granularity (BEO-16/P1a).
type ProbeGranularity uint8

const (
	// GranularityInvalid is the zero/undefined value.
	GranularityInvalid ProbeGranularity = iota
	// GranularityPane is exact pane/session liveness (no agent recognition).
	GranularityPane
	// GranularityPaneAgent is exact pane plus agent recognition.
	GranularityPaneAgent
	// GranularityWorkspace is workspace-level liveness only (no per-surface
	// agent semantics).
	GranularityWorkspace
	// GranularityTerminal is terminal-existence liveness only.
	GranularityTerminal
)

func (g ProbeGranularity) String() string {
	switch g {
	case GranularityPane:
		return "pane"
	case GranularityPaneAgent:
		return "pane+agent"
	case GranularityWorkspace:
		return "workspace"
	case GranularityTerminal:
		return "terminal"
	default:
		return "invalid"
	}
}

// BackendCapabilities is the static, deterministic descriptor for one session
// backend. It is a compile-time fact about the adapter surface, not a runtime
// health check.
type BackendCapabilities struct {
	// Name is the explicit backend identity (tmux, herdr, zellij, cmux, orca).
	Name string
	// Tier is the verification tier.
	Tier VerificationTier
	// Create reports the plain primitive create (NewWindow / terminal create).
	Create CapabilityAvailability
	// ReservationAwareCreate reports canonical reservation-aware find-or-create
	// (FindOrCreateWindow): the sole path usable for Soldier spawn recovery.
	ReservationAwareCreate CapabilityAvailability
	// Submit reports typed prompt / SendKeys submission.
	Submit CapabilityAvailability
	// Probe reports a structured (error-returning) endpoint probe that can
	// distinguish authoritative absence from operational failure.
	Probe CapabilityAvailability
	// ProbeGranularity describes the exact resource semantics the probe proves.
	// It prevents a coarser resource liveness (e.g. cmux workspace, orca
	// terminal) from being mistaken for exact agent-surface/incarnation proof
	// and constrains authoritative dead/current to the stated granularity.
	ProbeGranularity ProbeGranularity
	// Dispose reports teardown/close of the endpoint.
	Dispose CapabilityAvailability
	// WorktreeOwnership reports whether the backend owns the worktree. In munsu
	// worktree ownership is a separate provider (internal/backend/worktree.go),
	// never a session backend.
	WorktreeOwnership CapabilityAvailability
	// NativeBusy reports a native busy/idle/blocked activity source. For munsu
	// this is P1 proposed for herdr only; it is NOT claimed current.
	NativeBusy CapabilityAvailability
	// NativeEventWait reports a native event source for wake/reconcile hints.
	// For munsu this is a P1b stage item; it is NOT claimed current in P1a.
	NativeEventWait CapabilityAvailability
	// Secondmate reports remote second-session support. Out of scope for munsu.
	Secondmate CapabilityAvailability
}

// Capabilities returns the static capability descriptor for one named backend,
// or an error for an unknown identity.
func Capabilities(name string) (BackendCapabilities, error) {
	switch name {
	case "tmux":
		return BackendCapabilities{
			Name:                   "tmux",
			Tier:                   TierVerified,
			Create:                 CapCurrent,
			ReservationAwareCreate: CapCurrent,
			Submit:                 CapCurrent,
			Probe:                  CapCurrent,
			ProbeGranularity:       GranularityPane,
			Dispose:                CapCurrent,
			WorktreeOwnership:      CapUnsupported,
			NativeBusy:             CapUnsupported,
			NativeEventWait:        CapUnsupported,
			Secondmate:             CapUnsupported,
		}, nil
	case "herdr":
		return BackendCapabilities{
			Name:                   "herdr",
			Tier:                   TierVerified,
			Create:                 CapCurrent,
			ReservationAwareCreate: CapCurrent,
			Submit:                 CapCurrent,
			Probe:                  CapCurrent,
			ProbeGranularity:       GranularityPaneAgent,
			Dispose:                CapCurrent,
			WorktreeOwnership:      CapUnsupported,
			NativeBusy:             CapProposed,
			NativeEventWait:        CapProposed,
			Secondmate:             CapUnsupported,
		}, nil
	case "zellij":
		return BackendCapabilities{
			Name:                   "zellij",
			Tier:                   TierExperimental,
			Create:                 CapCurrent,
			ReservationAwareCreate: CapUnsupported,
			Submit:                 CapCurrent,
			Probe:                  CapCurrent,
			ProbeGranularity:       GranularityPane,
			Dispose:                CapCurrent,
			WorktreeOwnership:      CapUnsupported,
			NativeBusy:             CapUnsupported,
			NativeEventWait:        CapUnsupported,
			Secondmate:             CapUnsupported,
		}, nil
	case "cmux":
		return BackendCapabilities{
			Name:                   "cmux",
			Tier:                   TierExperimental,
			Create:                 CapCurrent,
			ReservationAwareCreate: CapUnsupported,
			Submit:                 CapCurrent,
			// cmux probe resolves at workspace granularity (no per-surface
			// liveness) and capture is unsupported; the capability is structured
			// for the workspace resource only.
			Probe:             CapCurrent,
			ProbeGranularity:  GranularityWorkspace,
			Dispose:           CapCurrent,
			WorktreeOwnership: CapUnsupported,
			NativeBusy:        CapUnsupported,
			NativeEventWait:   CapUnsupported,
			Secondmate:        CapUnsupported,
		}, nil
	case "orca":
		return BackendCapabilities{
			Name:                   "orca",
			Tier:                   TierExperimental,
			Create:                 CapCurrent,
			ReservationAwareCreate: CapUnsupported,
			Submit:                 CapCurrent,
			// orca probe resolves at terminal-existence granularity only.
			Probe:             CapCurrent,
			ProbeGranularity:  GranularityTerminal,
			Dispose:           CapCurrent,
			WorktreeOwnership: CapUnsupported,
			NativeBusy:        CapUnsupported,
			NativeEventWait:   CapUnsupported,
			Secondmate:        CapUnsupported,
		}, nil
	default:
		return BackendCapabilities{}, fmt.Errorf("unknown capability backend %q (known: tmux, herdr, zellij, cmux, orca)", name)
	}
}

// CapabilityMatrix returns the capability descriptors for all five declared
// backends in deterministic order (tmux, herdr, zellij, cmux, orca).
func CapabilityMatrix() []BackendCapabilities {
	names := []string{"tmux", "herdr", "zellij", "cmux", "orca"}
	out := make([]BackendCapabilities, 0, len(names))
	for _, n := range names {
		cap, err := Capabilities(n)
		if err != nil {
			// Static descriptors never fail for the declared five.
			continue
		}
		out = append(out, cap)
	}
	return out
}
