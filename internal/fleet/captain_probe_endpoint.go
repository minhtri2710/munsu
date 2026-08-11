package fleet

import "strings"

type CaptainProbeResult struct {
	PaneAlive      bool
	AgentAlive     bool
	ReadyForPrompt bool
	AgentStatus    string

	// Absent is true ONLY when the endpoint pane is authoritatively confirmed
	// absent (backend ErrPaneNotFound). It is the sole relaunch authority:
	// pane-present/no-agent, Starting/Unknown/Unresponsive/StaleIdentity/
	// Unresolved, generic errors, and unproven plain Alive=false all leave
	// Absent false and fail closed — none of them authorize Launch.
	Absent bool
}

// captainAgentStatusConfirmedLive reports whether agent status evidence is a
// confirmed-live observation. Transitional or unproven statuses (Starting,
// Unknown, Unresponsive, StaleIdentity, Unresolved) fail closed even when the
// coarse pane+agent liveness bits are set. An empty status carries no
// contradiction and defers to the coarse liveness bits.
func captainAgentStatusConfirmedLive(status string) bool {
	var norm strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(status)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			norm.WriteRune(r)
		}
	}
	switch norm.String() {
	case "starting", "unknown", "unresponsive", "staleidentity", "unresolved":
		return false
	}
	return true
}

// CaptainEndpointState is the strict, typed liveness decision for one captain
// endpoint, derived from CaptainProbeResult. It is the ONE decision rule used
// by Converge, Recover, RecoverTransaction.stepRelaunch, and ProbeLiveness.
type CaptainEndpointState int

const (
	// CaptainAlive: the pane and agent are confirmed live by observation.
	CaptainAlive CaptainEndpointState = iota
	// CaptainDead: the pane is authoritatively absent (ErrPaneNotFound).
	// This is the ONLY state that authorizes relaunch.
	CaptainDead
	// CaptainUnproven: the endpoint is not confirmed alive and not
	// authoritatively absent (pane-present/no-agent, generic errors, unproven
	// plain Alive=false). Fails closed: never authorizes relaunch.
	CaptainUnproven
	// CaptainSeeded: no launch evidence exists in task meta (never launched).
	CaptainSeeded
)

type ProbeEndpoint interface {
	Probe(home string, meta map[string]string) (CaptainProbeResult, error)
}

type RecoverCapabilities struct {
	Continuity  CaptainContinuityPort
	Watcher     CaptainWatcherPort
	Integration IntegrationPort
	Launch      LaunchEndpoint
	Probe       ProbeEndpoint
	Nudge       NudgeEndpoint
}
