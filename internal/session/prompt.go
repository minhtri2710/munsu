// Package session provides typed prompt submission distinct from raw SendKeys.
package session

import (
	"fmt"
)

// PromptStatus enumerates typed outcomes from AgentPrompt submission.
type PromptStatus string

const (
	// PromptSubmitted means the agent accepted the prompt and a state change
	// was observed. The text was submitted and the agent is processing it
	// (or has already finished).
	PromptSubmitted PromptStatus = "submitted"

	// PromptQueuedWhileBusy means the agent was already working and accepted
	// the prompt into its queue. The submission succeeded but processing
	// starts after the current turn completes.
	PromptQueuedWhileBusy PromptStatus = "queued-while-busy"

	// PromptStalled means the prompt text was written to the composer/input
	// but the agent did not start processing within the stall-detection
	// window. The text remains pending; outbox/envelope must NOT be removed.
	PromptStalled PromptStatus = "stalled"

	// PromptUnsupported means the backend or target does not support typed
	// prompt submission (e.g., protocol version too low, target is a raw
	// terminal without an agent).
	PromptUnsupported PromptStatus = "unsupported"

	// PromptEndpointDead means the target pane or agent was confirmed dead
	// or not found during the prompt attempt.
	PromptEndpointDead PromptStatus = "endpoint-dead"

	// PromptUnsafeComposer means the target composer/input contained
	// pending text before submission, making injection unsafe.
	PromptUnsafeComposer PromptStatus = "unsafe-composer"

	// PromptBackendFailed means the backend CLI invocation failed, returned
	// an unrecognized response, or encountered an unexpected error. The
	// submission state is unknown.
	PromptBackendFailed PromptStatus = "backend-failed"
)

// Acknowledged returns true when the prompt status means the submission
// was confirmed delivered to the agent. Only submitted and queued-while-busy
// count as acknowledged. All other statuses mean the submission either did
// not happen or did not reach the agent.
func (s PromptStatus) Acknowledged() bool {
	return s == PromptSubmitted || s == PromptQueuedWhileBusy
}

// PromptResult carries the outcome of an AgentPrompt submission.
type PromptResult struct {
	Status PromptStatus `json:"status"`
	Detail string       `json:"detail,omitempty"`
	Err    error        `json:"-"`                // non-nil for backend-failed
	Legacy bool         `json:"legacy,omitempty"` // true when fallback was used
}

// String returns a human-readable description of the result.
func (r PromptResult) String() string {
	s := string(r.Status)
	if r.Detail != "" {
		s += ": " + r.Detail
	}
	if r.Err != nil {
		if r.Detail != "" {
			s += ": "
		}
		s += r.Err.Error()
	}
	return s
}

// Acknowledged returns true when the submission was confirmed delivered.
func (r PromptResult) Acknowledged() bool {
	return r.Status.Acknowledged()
}

// PromptSubmitter is an optional interface that Backend implementations
// can implement to provide typed prompt submission distinct from raw
// terminal SendKeys.
type PromptSubmitter interface {
	// AgentPrompt submits a prompt to the target agent and returns a
	// typed result. The backend must distinguish between submitted,
	// queued-while-busy, stalled, and endpoint-dead based on the
	// underlying transport's response. Unsupported targets (non-agent
	// panes, protocol-16 servers) should return PromptUnsupported.
	AgentPrompt(windowID, text string) PromptResult
}

// LegacyPrompt is an optional interface that Backend implementations
// can implement to explicitly declare support for legacy SendKeys-based
// prompt submission when typed prompt is unavailable.
type LegacyPrompt interface {
	// LegacyPrompt sends text via SendKeys and returns a PromptResult.
	// This is used only when the target does NOT support PromptSubmitter
	// (e.g., protocol < 17) but the backend can still send via raw keys.
	// The result may have limited diagnostic power compared to AgentPrompt.
	LegacyPrompt(windowID, text string) PromptResult
}

// SubmitPrompt submits a prompt to the target agent using typed prompt
// submission when available. No content heuristics are applied — the caller
// is responsible for ensuring the text is a prompt (not a raw shell command).
//
// Dispatch rules:
//   - If the backend implements PromptSubmitter, AgentPrompt is used.
//   - If AgentPrompt returns PromptUnsupported (e.g., protocol < 17,
//     alive non-agent pane) AND the backend declares LegacyPrompt,
//     the legacy SendKeys fallback is invoked.
//   - AgentPrompt results other than Unsupported (stalled, endpoint-dead,
//     backend-failed) are NEVER subject to fallback.
//   - If the backend has neither PromptSubmitter nor LegacyPrompt, the
//     result is PromptUnsupported.
func SubmitPrompt(bk Backend, windowID, text string) PromptResult {
	// Try typed prompt submission.
	if submitter, ok := bk.(PromptSubmitter); ok {
		result := submitter.AgentPrompt(windowID, text)
		// Only fall back to legacy if typed prompt returned Unsupported
		// (e.g., protocol < 17, alive non-agent pane).
		if result.Status == PromptUnsupported {
			if legacy, ok := bk.(LegacyPrompt); ok {
				return legacy.LegacyPrompt(windowID, text)
			}
		}
		// For all other statuses (stalled, endpoint-dead, backend-failed),
		// return as-is — NEVER fall back.
		return result
	}

	// No typed prompt support: fall back to legacy SendKeys if declared.
	if legacy, ok := bk.(LegacyPrompt); ok {
		return legacy.LegacyPrompt(windowID, text)
	}

	// Backend doesn't support prompt submission at all.
	return PromptResult{
		Status: PromptUnsupported,
		Detail: fmt.Sprintf("backend %T does not support prompt submission", bk),
	}
}
