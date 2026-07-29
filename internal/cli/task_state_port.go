package cli

import (
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

type runtimeTaskStatePort struct{}

func (runtimeTaskStatePort) ReadTaskState(homeDir, taskID string) (*orchestrator.ObservedTaskState, error) {
	state, err := fleet.ReadWithProbe(homeDir, taskID, runtimeTaskEndpointProbe())
	if err != nil {
		return nil, err
	}
	return &orchestrator.ObservedTaskState{NoMistakesRunStep: state.NoMistakesRunStep}, nil
}
