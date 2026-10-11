package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/backend"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

type sessionMailboxSender struct {
	resolve func(string, map[string]string) (backend.Backend, string, error)
}

func newSessionMailboxSender() sessionMailboxSender {
	return sessionMailboxSender{resolve: backend.BackendForTask}
}

func (s sessionMailboxSender) backend(home string, meta map[string]string) (backend.Backend, error) {
	if home == "" || meta["window"] == "" {
		return nil, fmt.Errorf("bound sender identity is incomplete")
	}
	bk, _, err := boundBackend(home, meta, s.resolve)
	return bk, err
}

func (s sessionMailboxSender) Alive(home string, meta map[string]string) (bool, error) {
	bk, err := s.backend(home, meta)
	if err != nil {
		return false, err
	}
	result, err := probeCaptainBackend(bk, meta)
	if err != nil {
		return false, err
	}
	return result.PaneAlive && result.AgentAlive, nil
}

func (s sessionMailboxSender) Send(home string, meta map[string]string, payload string) mhome.BoundSendResult {
	bk, err := s.backend(home, meta)
	if err != nil {
		return mhome.BoundSendResult{Status: "backend-failed", Err: err}
	}
	if recognized, ok := bk.(interface {
		IsRecognizedAgent(string) (bool, string)
	}); ok {
		isAgent, status := recognized.IsRecognizedAgent(meta["window"])
		if !isAgent || !backend.AgentStatusReady(status) {
			return mhome.BoundSendResult{Status: "deferred", Detail: "agent status: " + status}
		}
	}
	result := backend.SubmitPrompt(bk, meta["window"], payload)
	return mhome.BoundSendResult{Status: string(result.Status), Detail: result.Detail, Acknowledged: result.Acknowledged(), Err: result.Err}
}
