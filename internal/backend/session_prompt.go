// Package session provides typed prompt submission distinct from raw SendKeys.

package backend

import (
	"fmt"
)

// PromptStatus enumerates typed outcomes from AgentPrompt submission.
type PromptStatus string

const (
	// PromptSubmitted means the agent accepted the prompt and a turn start
	// (status working or blocked) was observed after the submit. A transport
	// acknowledgment alone never yields this status.
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
// was confirmed delivered to the agent: an observed turn start (submitted) or
// a prompt queued behind an already-working agent's turn (queued-while-busy).
// All other statuses mean the submission either did not happen or did not
// reach the agent.
func (s PromptStatus) Acknowledged() bool {
	return s == PromptSubmitted || s == PromptQueuedWhileBusy
}

// PromptResult carries the outcome of an AgentPrompt submission.
type PromptResult struct {
	Status PromptStatus `json:"status"`
	Detail string       `json:"detail,omitempty"`
	Err    error        `json:"-"` // non-nil for backend-failed
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
	// underlying transport's response. Unsupported targets (alive
	// non-agent panes) should return PromptUnsupported.
	AgentPrompt(windowID, text string) PromptResult
}

// SubmitPrompt submits a prompt to the target agent through the backend's
// typed AgentPrompt. No content heuristics are applied — the caller is
// responsible for ensuring the text is a prompt (not a raw shell command).
// A backend without PromptSubmitter, or a target AgentPrompt reports as
// unsupported (an alive non-agent pane), yields PromptUnsupported; nothing
// is ever typed into the pane as raw keys.
func SubmitPrompt(bk Backend, windowID, text string) PromptResult {
	if submitter, ok := bk.(PromptSubmitter); ok {
		return submitter.AgentPrompt(windowID, text)
	}
	return PromptResult{
		Status: PromptUnsupported,
		Detail: fmt.Sprintf("backend %T does not support prompt submission", bk),
	}
}
