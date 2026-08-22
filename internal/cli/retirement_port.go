package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// fleetRetirementPort adapts the orchestrator RetirementPort to the fleet
// implementation. The merged delivery_state transition routes through the
// composed canonical Task Authority: the compose function builds the
// canonical authority over the exact home the watcher is servicing, mirroring
// the delivery command composition root.
type fleetRetirementPort struct {
	compose func(homeDir string) (*taskauthority.Canonical, error)
}

func (p fleetRetirementPort) RecoverPendingRetirements(homeDir string) (int, []error) {
	auth, err := p.compose(homeDir)
	if err != nil {
		return 0, []error{fmt.Errorf("composing task authority for retirement recovery: %w", err)}
	}
	return fleet.RecoverAllPendingRetirements(homeDir, auth)
}

// fleetCheckValidationPort adapts the orchestrator CheckValidationPort to the
// fleet validator that owns the rule. It composes nothing: validating a check
// artifact is a question about a file, not about task authority, which is why
// it is not a method on fleetRetirementPort.
type fleetCheckValidationPort struct{}

func (fleetCheckValidationPort) ValidateCheck(path string) error {
	return fleet.ValidateCheckWithLstat(path)
}

func (p fleetRetirementPort) RetireMergedPoll(homeDir, taskID, checkPath string) error {
	auth, err := p.compose(homeDir)
	if err != nil {
		return fmt.Errorf("composing task authority for merged poll retirement: %w", err)
	}
	return fleet.RetireMergedPoll(homeDir, taskID, checkPath, auth)
}
