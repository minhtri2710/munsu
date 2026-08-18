package cli

import (
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

type sessionActivationTransport struct {
	resolve  func(string, string) (backend.Backend, string, error)
	identity func(string) (string, error)
}

func newSessionActivationTransport() orchestrator.ActivationTransport {
	return sessionActivationTransport{resolve: backend.Resolve, identity: fleet.ResolveGeneralHomeBackend}
}

func (t sessionActivationTransport) Attempt(home string, target orchestrator.TargetResult, payload string) orchestrator.ActivationAttempt {
	if t.identity == nil {
		return orchestrator.ActivationAttempt{SafetyError: "no backend identity resolver available"}
	}
	backendName, err := t.identity(home)
	if err != nil {
		return orchestrator.ActivationAttempt{SafetyError: err.Error()}
	}
	bk, _, err := t.resolve(home, backendName)
	if err != nil {
		return orchestrator.ActivationAttempt{SafetyError: err.Error()}
	}
	safe, verdict, err := orchestrator.IsSafeInjectTarget(bk, target.Handle)
	attempt := orchestrator.ActivationAttempt{SafetyVerdict: verdict.String()}
	if err != nil {
		attempt.SafetyError = err.Error()
		return attempt
	}
	if recognized, ok := bk.(interface {
		IsRecognizedAgent(string) (bool, string)
	}); ok {
		isAgent, status := recognized.IsRecognizedAgent(target.Handle)
		if !isAgent || !backend.AgentStatusReady(status) {
			attempt.SafetyVerdict = "pending"
			return attempt
		}
	}
	if !safe {
		if aware, ok := bk.(backend.AgentAwareBackend); ok {
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
	result := backend.SubmitPrompt(bk, target.Handle, payload)
	attempt.SubmitStatus = string(result.Status)
	attempt.SubmitDetail = result.Detail
	if result.Err != nil {
		attempt.SubmitError = result.Err.Error()
	}
	attempt.Acknowledged = result.Acknowledged()
	return attempt
}

func activationComposerSafe(cap orchestrator.PaneCapture, handle string) bool {
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
	plain := strings.TrimSpace(domain.StripANSI(composerLine))
	visible := strings.TrimSpace(domain.StripGhost(composerLine))
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
