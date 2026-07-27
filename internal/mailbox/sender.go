package mailbox

import (
	"fmt"
	"github.com/minhtri2710/munsu/internal/session"
)

type BoundSendResult struct {
	Status       string
	Detail       string
	Acknowledged bool
	Err          error
}
type BoundSender interface {
	Alive(homeDir string, meta map[string]string) (bool, error)
	Send(homeDir string, meta map[string]string, payload string) BoundSendResult
}
type sessionBoundSender struct {
	resolve func(string, map[string]string) (session.Backend, string, error)
}

func (s sessionBoundSender) backend(home string, meta map[string]string) (session.Backend, error) {
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
func (s sessionBoundSender) Alive(home string, meta map[string]string) (bool, error) {
	bk, err := s.backend(home, meta)
	if err != nil {
		return false, err
	}
	return bk.Alive(meta["window"]), nil
}
func (s sessionBoundSender) Send(home string, meta map[string]string, payload string) BoundSendResult {
	bk, err := s.backend(home, meta)
	if err != nil {
		return BoundSendResult{Status: "backend-failed", Err: err}
	}
	result := session.SubmitPrompt(bk, meta["window"], payload)
	return BoundSendResult{Status: string(result.Status), Detail: result.Detail, Acknowledged: result.Acknowledged(), Err: result.Err}
}

var defaultBoundSender BoundSender = sessionBoundSender{resolve: session.BackendForTask}
