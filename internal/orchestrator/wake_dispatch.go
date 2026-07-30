// Package orchestrator provides the high DispatchWake interface and workflow
// for dispatching durable Wakes to backend agent targets.
package orchestrator

import "fmt"

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

// DeferredResult returns a Deferred outcome (claim taken, submit unacknowledged).
func DeferredResult(detail string) DispatchWakeResult {
	return DispatchWakeResult{Outcome: WakeDeferred, Reason: "submit-not-acknowledged", Detail: detail}
}

// ProbeResult carries the probe outcome from a backend port.
type ProbeResult struct {
	PaneAlive      bool
	AgentAlive     bool
	ReadyForPrompt bool
}

// ProbePort is the Backend-facing probe adapter interface.
// Implementations wrap backend.Backend and backend.AgentAwareBackend.
type ProbePort interface {
	Probe(window string) (ProbeResult, error)
}

// SubmitPort is the Backend-facing prompt submission adapter interface.
// Implementations wrap backend.SubmitPrompt.
type SubmitPort interface {
	Submit(window, prompt string) (acknowledged bool, detail string, err error)
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
}

// DispatchWake owns the complete Wake dispatch workflow.
//
// Flow:
//  1. Delivery-mode gate: native/manual -> Skipped without claiming
//  2. Target identity gate: missing/incomplete target -> Skipped
//  3. Ownership validation: invalid target -> fail-closed error
//  4. Backend probe gate: probe failure or unready target -> Skipped
//  5. Wake claim: max 1 Wake, after all gates pass
//  6. Empty queue after claim -> Skipped
//  7. Prompt construction with exact claim_id, event_id, payload, resolve instruction
//  8. Prompt submission -> Submitted or Deferred
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

	// Step 4: Backend probe gate
	if req.Probe == nil {
		return DispatchWakeResult{}, fmt.Errorf("probe port is nil")
	}
	probeResult, err := req.Probe.Probe(req.Target.Handle)
	if err != nil {
		return SkippedResult("probe-error", err.Error()), nil
	}
	if !probeResult.PaneAlive {
		return SkippedResult("target-unready", "pane is not alive"), nil
	}
	if !probeResult.AgentAlive {
		return SkippedResult("target-unready", "agent is not alive"), nil
	}
	if !probeResult.ReadyForPrompt {
		return SkippedResult("target-unready", "target is not ready for prompt"), nil
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
	acknowledged, detail, err := req.Submit.Submit(req.Target.Handle, prompt)
	if err != nil {
		return DeferredResult("backend error: " + err.Error()), nil
	}
	if !acknowledged {
		return DeferredResult("submit not acknowledged: " + detail), nil
	}

	return SubmittedResult("acknowledged", "event="+eventID+" claim="+claim.LeaseID), nil
}
