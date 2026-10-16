package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/backend"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

type sessionSoldierEndpoints struct {
	resolve boundResolver
}

func newSessionSoldierEndpoints() sessionSoldierEndpoints {
	return sessionSoldierEndpoints{resolve: backend.BackendForTask}
}

func (s sessionSoldierEndpoints) backend(home string, ep taskauthority.EndpointBinding) (backend.Backend, error) {
	if home == "" || ep.Handle == "" {
		return nil, fmt.Errorf("soldier endpoint identity is incomplete")
	}
	bk, _, err := boundBackend(home, bindingRef(ep), s.resolve)
	return bk, err
}

func (s sessionSoldierEndpoints) Busy(home string, ep taskauthority.EndpointBinding, harness string) (bool, error) {
	bk, err := s.backend(home, ep)
	if err != nil {
		return false, err
	}
	if _, ok := bk.(backend.AgentActivityReader); ok {
		observation := backend.ObserveEndpoint(bk, ep.Handle, harnessProcessMatcher(harness))
		if observation.Lifecycle != backend.LifecycleAlive {
			return false, fmt.Errorf("endpoint not alive: %s", observation.Detail)
		}
		switch observation.Activity {
		case backend.ActivityBusy:
			return true, nil
		case backend.ActivityIdle:
			return false, nil
		default:
			return false, fmt.Errorf("endpoint status unknown: %q", observation.Activity.String())
		}
	}
	return false, nil
}

func (s sessionSoldierEndpoints) Send(home string, ep taskauthority.EndpointBinding, payload string) mhome.BoundSendResult {
	bk, err := s.backend(home, ep)
	if err != nil {
		return mhome.BoundSendResult{Status: "backend-failed", Err: err}
	}
	result := backend.SubmitPrompt(bk, ep.Handle, payload)
	return mhome.BoundSendResult{Status: string(result.Status), Detail: result.Detail, Acknowledged: result.Acknowledged(), Err: result.Err}
}
