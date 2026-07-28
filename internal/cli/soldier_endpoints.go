package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/mailbox"
)

type recognizedAgentBackend interface {
	IsRecognizedAgent(string) (bool, string)
}

type sessionSoldierEndpoints struct {
	resolve func(string, map[string]string) (backend.Backend, string, error)
}

func newSessionSoldierEndpoints() sessionSoldierEndpoints {
	return sessionSoldierEndpoints{resolve: backend.BackendForTask}
}

func (s sessionSoldierEndpoints) backend(home string, meta map[string]string) (backend.Backend, error) {
	if home == "" || meta["window"] == "" {
		return nil, fmt.Errorf("soldier endpoint identity is incomplete")
	}
	bk, name, err := s.resolve(home, meta)
	if err != nil {
		return nil, err
	}
	if expected := meta["backend"]; expected != "" && name != expected {
		return nil, fmt.Errorf("bound backend resolved as %q", name)
	}
	if name == "herdr" && meta["herdr_session"] != "" {
		sessionID, _ := backend.ParseWindow(meta["window"])
		if sessionID != "" && sessionID != meta["herdr_session"] {
			return nil, fmt.Errorf("herdr session ownership mismatch")
		}
	}
	return bk, nil
}

func (s sessionSoldierEndpoints) Alive(home string, meta map[string]string) (bool, error) {
	bk, err := s.backend(home, meta)
	if err != nil {
		return false, err
	}
	return bk.Alive(meta["window"]), nil
}

func (s sessionSoldierEndpoints) Busy(home string, meta map[string]string) (bool, error) {
	bk, err := s.backend(home, meta)
	if err != nil {
		return false, err
	}
	if checker, ok := bk.(backend.BusyChecker); ok {
		return checker.AgentBusy(meta["window"])
	}
	if herdr, ok := bk.(recognizedAgentBackend); ok {
		recognized, status := herdr.IsRecognizedAgent(meta["window"])
		if !recognized {
			if !bk.Alive(meta["window"]) {
				return false, fmt.Errorf("endpoint not alive")
			}
			return false, fmt.Errorf("endpoint status unknown: not a recognized agent")
		}
		switch status {
		case "working":
			return true, nil
		case "idle", "ready", "review-ready":
			return false, nil
		default:
			return false, fmt.Errorf("endpoint status unknown: %q", status)
		}
	}
	return false, nil
}

func (s sessionSoldierEndpoints) Send(home string, meta map[string]string, payload string) mailbox.BoundSendResult {
	bk, err := s.backend(home, meta)
	if err != nil {
		return mailbox.BoundSendResult{Status: "backend-failed", Err: err}
	}
	result := backend.SubmitPrompt(bk, meta["window"], payload)
	return mailbox.BoundSendResult{Status: string(result.Status), Detail: result.Detail, Acknowledged: result.Acknowledged(), Err: result.Err}
}
