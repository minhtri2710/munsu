// Package session provides typed prompt submission distinct from raw SendKeys.
package session

import (
	"fmt"
	"strings"
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
	var b strings.Builder
	b.WriteString(string(r.Status))
	if r.Detail != "" {
		b.WriteString(": ")
		b.WriteString(r.Detail)
	}
	if r.Err != nil {
		if b.Len() > 0 {
			b.WriteString(": ")
		}
		b.WriteString(r.Err.Error())
	}
	return b.String()
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

// IsPromptText returns true when the text is a prompt/message meant for
// the agent, as opposed to a raw terminal operation. Only /quit, launch
// scripts, and literal control keys are raw terminal operations.
// Agent-interpreted slash commands (/re-read-agents, etc.) are prompts.
func IsPromptText(text string) bool {
	text = strings.TrimSpace(text)

	// /quit terminates the agent — always raw.
	if text == "/quit" {
		return false
	}

	// Launch scripts (multi-line or shell command chains) are raw.
	if strings.Contains(text, "\n") || strings.Contains(text, "&&") || strings.Contains(text, "||") {
		return false
	}

	// Literal control key names are raw.
	controlKeys := []string{"Enter", "Escape", "Tab", "Up", "Down", "Left", "Right",
		"Backspace", "Delete", "Home", "End", "PageUp", "PageDown",
		"C-c", "C-d", "C-z", "C-l", "C-u"}
	for _, k := range controlKeys {
		if text == k {
			return false
		}
	}

	// Everything else (including agent slash commands like /re-read-agents)
	// is a prompt.
	return true
}

// DispatchPrompt routes a text submission to the correct method based on
// the Backend's capabilities and the nature of the text.
//
// Rules:
//   - Raw terminal commands (shell commands, /quit, control keys) always
//     use SendKeys.
//   - If the backend implements PromptSubmitter and the target is a
//     recognized agent, AgentPrompt is used.
//   - If the target does not support prompts but the backend declares
//     LegacyPrompt, fallback is allowed only for policy-safe targets.
//   - Fallback is NEVER allowed for stalled, protocol mismatch, or
//     selected-backend failure conditions — those return errors.
func DispatchPrompt(bk Backend, windowID, text string) PromptResult {
	// Raw commands always use SendKeys.
	if !IsPromptText(text) {
		if err := bk.SendKeys(windowID, text); err != nil {
			return PromptResult{
				Status: PromptBackendFailed,
				Detail: fmt.Sprintf("send-keys failed"),
				Err:    err,
			}
		}
		return PromptResult{
			Status: PromptSubmitted,
			Detail: "send-keys (raw command)",
			Legacy: true,
		}
	}

	// Try typed prompt submission.
	if submitter, ok := bk.(PromptSubmitter); ok {
		result := submitter.AgentPrompt(windowID, text)
		return result
	}

	// No typed prompt support: fall back to legacy SendKeys if declared.
	// This is the "explicitly unsupported capability" case.
	if legacy, ok := bk.(LegacyPrompt); ok {
		return legacy.LegacyPrompt(windowID, text)
	}

	// Backend doesn't support prompt submission at all.
	return PromptResult{
		Status: PromptUnsupported,
		Detail: fmt.Sprintf("backend %T does not support prompt submission", bk),
	}
}
