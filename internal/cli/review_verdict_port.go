package cli

import (
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

// fleetReviewVerdictPort adapts the orchestrator ReviewVerdictPort to the
// fleet record step that `delivery record-verdict --reviewer-task` also runs.
type fleetReviewVerdictPort struct{}

func (fleetReviewVerdictPort) WorkingReviews(homeDir string) ([]orchestrator.ReviewVerdictTarget, error) {
	refs, err := fleet.WorkingReviewTasks(homeDir)
	if err != nil {
		return nil, err
	}
	targets := make([]orchestrator.ReviewVerdictTarget, len(refs))
	for i, ref := range refs {
		targets[i] = orchestrator.ReviewVerdictTarget{TaskID: ref.TaskID, Generation: ref.Generation}
	}
	return targets, nil
}

func (fleetReviewVerdictPort) ObserveReviewVerdict(homeDir, taskID string, generation uint64) (bool, []byte, error) {
	return fleet.ObserveReviewVerdictFile(homeDir, taskID, generation)
}

func (fleetReviewVerdictPort) RecordReviewVerdict(homeDir, taskID string, generation uint64, digest string) (string, error) {
	return fleet.RecordReviewVerdict(homeDir, taskID, generation, digest)
}
