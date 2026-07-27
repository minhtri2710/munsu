package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/mailbox"
	"github.com/minhtri2710/munsu/internal/session"
)

type sessionMailboxSender struct {
	resolve func(string, map[string]string) (session.Backend, string, error)
}

func newSessionMailboxSender() mailbox.BoundSender {
	return sessionMailboxSender{resolve: session.BackendForTask}
}

func (s sessionMailboxSender) backend(home string, meta map[string]string) (session.Backend, error) {
	if home == "" || meta["backend"] == "" || meta["window"] == "" {
		return nil, fmt.Errorf("bound sender identity is incomplete")
	}
	bk, name, err := s.resolve(home, meta)
	if err != nil {
		return nil, err
	}
	if name != meta["backend"] {
		return nil, fmt.Errorf("bound backend resolved as %q", name)
	}
	if meta["backend"] == "herdr" && meta["herdr_session"] != "" {
		hs, _ := session.ParseWindow(meta["window"])
		if hs != "" && hs != meta["herdr_session"] {
			return nil, fmt.Errorf("herdr session ownership mismatch")
		}
	}
	return bk, nil
}

func (s sessionMailboxSender) Alive(home string, meta map[string]string) (bool, error) {
	bk, err := s.backend(home, meta)
	if err != nil {
		return false, err
	}
	return bk.Alive(meta["window"]), nil
}

func (s sessionMailboxSender) Send(home string, meta map[string]string, payload string) mailbox.BoundSendResult {
	bk, err := s.backend(home, meta)
	if err != nil {
		return mailbox.BoundSendResult{Status: "backend-failed", Err: err}
	}
	result := session.SubmitPrompt(bk, meta["window"], payload)
	return mailbox.BoundSendResult{Status: string(result.Status), Detail: result.Detail, Acknowledged: result.Acknowledged(), Err: result.Err}
}
