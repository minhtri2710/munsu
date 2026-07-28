package cli

import "github.com/minhtri2710/munsu/internal/fleet"

type fleetRetirementPort struct{}

func (fleetRetirementPort) RecoverPendingRetirements(homeDir string) (int, []error) {
	return fleet.RecoverAllPendingRetirements(homeDir)
}

func (fleetRetirementPort) RetireMergedPoll(homeDir, taskID, checkPath string) error {
	return fleet.RetireMergedPoll(homeDir, taskID, checkPath)
}
