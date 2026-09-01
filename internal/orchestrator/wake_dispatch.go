// Package orchestrator provides the high DispatchWake interface and workflow
// for dispatching durable Wakes to backend agent targets.
package orchestrator

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/backend"
)

// DispatchWakeOutcome enumerates the typed outcomes from a Wake dispatch attempt.
type DispatchWakeOutcome string

const (
	WakeSkipped   DispatchWakeOutcome = "skipped"
	WakeSubmitted DispatchWakeOutcome = "submitted"
	WakeDeferred  DispatchWakeOutcome = "deferred"
)

// DispatchWakeResult carries the typed outcome of a Wake dispatch attempt.
type DispatchWakeResult struct {
	Outcome DispatchWakeOutcome
	Reason  string
	Detail  string
}

// SkippedResult returns a Skipped outcome with reason and detail.
func SkippedResult(reason, detail string) DispatchWakeResult {
	return DispatchWakeResult{Outcome: WakeSkipped, Reason: reason, Detail: detail}
}

// SubmittedResult returns a Submitted outcome with reason and detail.
func SubmittedResult(reason, detail string) DispatchWakeResult {
	return DispatchWakeResult{Outcome: WakeSubmitted, Reason: reason, Detail: detail}
}

// DeferredResult returns a Deferred outcome with a specific reason and detail.
// The reason identifies the failure class (backend-error, stalled, endpoint-dead,
// unsupported, rejected, backend-failed).
func DeferredResult(reason, detail string) DispatchWakeResult {
	return DispatchWakeResult{Outcome: WakeDeferred, Reason: reason, Detail: detail}
}

// ProbePort is the Backend-facing probe adapter interface.
// Implementations wrap backend.Backend and return a typed backend.EndpointObservation.
type ProbePort interface {
	Probe(window string) (backend.EndpointObservation, error)
}

// SubmitResult carries the typed outcome of a prompt submission attempt.
// Status preserves the backend's typed result (stalled, endpoint-dead, etc.).
type SubmitResult struct {
	Acknowledged bool
	Status       string
	Detail       string
	Err          error
}

// SubmitPort is the Backend-facing prompt submission adapter interface.
// Implementations wrap backend.SubmitPrompt.
type SubmitPort interface {
	Submit(window, prompt string) SubmitResult
}

// BusyPort reads the busy authority's reading for an observation. The reading
// is the pinned fleet.BusyReading string: "held"/"idle"/"unknown"/"blocked"/"dead".
type BusyPort interface {
	Read(obs backend.EndpointObservation) string
}

// DispatchWakeRequest carries all inputs and adapter ports for a Wake dispatch.
type DispatchWakeRequest struct {
	HomeDir string
	Mode    WakeDeliveryMode
	Target  TargetResult

	// Backend-facing ports injected via the request struct.
	// Must be non-nil for herdr delivery.
	Probe  ProbePort
	Submit SubmitPort
	Busy   BusyPort
}

// DispatchWake owns the complete Wake dispatch workflow.
//
// Flow:
//  1. Delivery-mode gate: native/manual -> Skipped without claiming
//  2. Target identity gate: missing/incomplete target -> Skipped
//  3. Ownership validation: invalid target -> fail-closed error
//  4. Endpoint probe gate: typed observation gates — alive proceeds to the
//     busy gate; starting/unresponsive/dead defer; unknown/stale-identity/
//     unresolved skip safely without claiming (NOT collapsed to dead)
//  5. Busy gate (alive only): idle proceeds; held/blocked hold; unknown is
//     refused (never dispatched as idle); dead skips; unrecognized fails closed
//  6. Wake claim: max 1 Wake, after all gates pass
//  7. Empty queue after claim -> Skipped
//  8. Prompt construction with exact claim_id, event_id, payload, resolve instruction
//  9. Prompt submission -> Submitted or Deferred
//
// Errors only for invalid invariants, unsafe ownership, corrupt state.
func DispatchWake(req DispatchWakeRequest) (DispatchWakeResult, error) {
	// Step 1: Delivery-mode gate
	if req.Mode != WakeDeliveryHerdr {
		return SkippedResult("delivery-mode", "mode "+string(req.Mode)+" is not herdr"), nil
	}

	// Step 2: Target identity gate
	if req.Target.Handle == "" {
		return SkippedResult("missing-target", "target handle is empty"), nil
	}
	if req.Target.Session == "" {
		return SkippedResult("missing-target", "target session is empty"), nil
	}

	// Step 3: Ownership validation (fail-closed)
	if err := ValidateTargetOwnership(&req.Target); err != nil {
		return DispatchWakeResult{}, fmt.Errorf("invalid target ownership: %w", err)
	}

	// Step 4: Endpoint probe gate — typed observation gates
	if req.Probe == nil {
		return DispatchWakeResult{}, fmt.Errorf("probe port is nil")
	}
	obs, err := req.Probe.Probe(req.Target.Handle)
	if err != nil {
		return SkippedResult("probe-error", err.Error()), nil
	}
	switch obs.State() {
	case backend.EndpointAlive:
		if req.Busy == nil {
			return DispatchWakeResult{}, fmt.Errorf("busy port is nil")
		}
		switch req.Busy.Read(obs) {
		case "idle":
			// Proceed to claim
		case "held":
			return SkippedResult("endpoint-busy", "endpoint has an active turn"), nil
		case "blocked":
			return SkippedResult("endpoint-blocked", "endpoint is blocked awaiting input"), nil
		case "unknown":
			return SkippedResult("activity-unknown", "endpoint activity is unknown; not dispatched as idle"), nil
		case "dead":
			return SkippedResult("endpoint-absent", "endpoint activity reads absent"), nil
		default:
			return SkippedResult("invalid-busy-reading", "unrecognized busy reading"), nil
		}
	case backend.EndpointStarting:
		return SkippedResult("target-unready", "endpoint is starting"), nil
	case backend.EndpointUnresponsive:
		return SkippedResult("target-unready", "endpoint is unresponsive"), nil
	case backend.EndpointDead:
		return SkippedResult("endpoint-dead", obs.Detail), nil
	case backend.EndpointUnknown:
		// NOT collapsed to dead — skip safely without claiming
		return SkippedResult("endpoint-unknown", obs.Detail), nil
	case backend.EndpointStaleIdentity:
		// NOT collapsed to dead — skip safely without claiming
		return SkippedResult("stale-identity", obs.Detail), nil
	case backend.EndpointUnresolved:
		// NOT collapsed to dead — skip safely without claiming
		return SkippedResult("endpoint-unresolved", obs.Detail), nil
	default:
		return SkippedResult("invalid-observation", "unrecognized endpoint observation state"), nil
	}

	// Step 5: Claim Wake (max 1, after all gates pass)
	claim, err := ClaimWakes(req.HomeDir, "munsu:herdr", 60, 1)
	if err != nil {
		return SkippedResult("claim-error", err.Error()), nil
	}
	if claim == nil || len(claim.Wakes) == 0 {
		return SkippedResult("empty-queue", "no wakes to dispatch"), nil
	}

	// Step 6: Build the exact prompt
	wake := claim.Wakes[0]
	eventID := wake.Epoch + ":" + wake.Seq
	prompt := fmt.Sprintf(
		"[mu-system:wake]\nkey: %s\nclaim_id: %s\nevent_id: %s\n\n%s\n\nReview this durable wake, then run:\nmunsu wake resolve --claim-id %q --event-id %q --summary %q",
		eventID, claim.LeaseID, eventID, wake.Payload, claim.LeaseID, eventID, "<non-empty summary>",
	)

	// Step 7: Submit prompt
	if req.Submit == nil {
		return DispatchWakeResult{}, fmt.Errorf("submit port is nil")
	}
	result := req.Submit.Submit(req.Target.Handle, prompt)
	if result.Err != nil {
		return DeferredResult("backend-error", result.Err.Error()), nil
	}
	if !result.Acknowledged {
		return DeferredResult(result.Status, result.Detail), nil
	}

	return SubmittedResult("acknowledged", "event="+eventID+" claim="+claim.LeaseID), nil
}
