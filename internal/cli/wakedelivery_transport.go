package cli

import (
	"strings"

	"github.com/minhtri2710/munsu/internal/afk"
	"github.com/minhtri2710/munsu/internal/composer"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/wakedelivery"
)

type sessionActivationTransport struct {
	resolve func(string, string) (session.Backend, string, error)
}

func newSessionActivationTransport() wakedelivery.ActivationTransport {
	return sessionActivationTransport{resolve: session.Resolve}
}

func (t sessionActivationTransport) Attempt(home string, target afk.TargetResult, payload string) wakedelivery.ActivationAttempt {
	bk, _, err := t.resolve(home, "")
	if err != nil {
		return wakedelivery.ActivationAttempt{SafetyError: err.Error()}
	}
	safe, verdict, err := afk.IsSafeInjectTarget(bk, target.Handle)
	attempt := wakedelivery.ActivationAttempt{SafetyVerdict: verdict.String()}
	if err != nil {
		attempt.SafetyError = err.Error()
		return attempt
	}
	if !safe {
		if aware, ok := bk.(session.AgentAwareBackend); ok {
			alive, agentAlive, agentErr := aware.CheckAgentAlive(target.Handle)
			if agentErr == nil && alive && agentAlive && (verdict.String() == "unknown" || verdict.String() == "pending") && activationComposerSafe(bk, target.Handle) {
				safe = true
				attempt.SafetyVerdict = "empty"
				attempt.CaptureContent = "(agent-override: bare prompt glyph)"
			}
		}
	}
	if !safe {
		return attempt
	}
	result := session.SubmitPrompt(bk, target.Handle, payload)
	attempt.SubmitStatus = string(result.Status)
	attempt.SubmitDetail = result.Detail
	if result.Err != nil {
		attempt.SubmitError = result.Err.Error()
	}
	attempt.Acknowledged = result.Acknowledged()
	return attempt
}

func activationComposerSafe(cap afk.PaneCapture, handle string) bool {
	output, err := cap.Capture(handle, 4)
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimRight(output, "\n\r"), "\n")
	composerLine := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			composerLine = lines[i]
			break
		}
	}
	plain := strings.TrimSpace(composer.StripANSI(composerLine))
	visible := strings.TrimSpace(composer.StripGhost(composerLine))
	for _, busy := range []string{"Working", "Thinking", "Running", "Processing"} {
		if strings.Contains(plain, busy) {
			return false
		}
	}
	for _, value := range []string{plain, visible} {
		if value == "" || value == "❯" || value == "›" || value == ">" || value == "o" {
			return true
		}
	}
	return false
}
