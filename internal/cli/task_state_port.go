package cli

import (
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/soldierstate"
)

type runtimeTaskStatePort struct{}

func (runtimeTaskStatePort) ReadTaskState(homeDir, taskID string) (*orchestrator.ObservedTaskState, error) {
	state, err := soldierstate.ReadWithProbe(homeDir, taskID, runtimeTaskEndpointProbe())
	if err != nil {
		return nil, err
	}
	return &orchestrator.ObservedTaskState{NoMistakesRunStep: state.NoMistakesRunStep}, nil
}
