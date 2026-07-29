package cli

import "github.com/minhtri2710/munsu/internal/orchestrator"

type orchestratorRetirementJournals struct{}

func (orchestratorRetirementJournals) VerifyRetirementContinuity(homeDir, taskID string) error {
	return orchestrator.VerifyRetirementContinuity(homeDir, taskID)
}

func (orchestratorRetirementJournals) PrepareForcedRetirementEvidence(homeDir, taskID string) ([]string, error) {
	return orchestrator.PrepareForcedRetirementEvidence(homeDir, taskID)
}

func (orchestratorRetirementJournals) FinalizeRetirementJournals(homeDir, taskID string) ([]string, error) {
	return orchestrator.FinalizeRetirementJournals(homeDir, taskID)
}
