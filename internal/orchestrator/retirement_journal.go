package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// VerifyRetirementContinuity refuses a teardown while a report the task's
// reporter wrote with munsu report is not settled by its parent: retiring the
// task would end it before the parent took its report. A line the dispatcher
// wrote holds nothing, and neither does a scout's done report: settling it only
// completes the scout, which retiring it supersedes. An unreadable report state
// refuses.
func VerifyRetirementContinuity(homeDir, taskID, kind string) error {
	unsettled, err := home.UnsettledReports(homeDir, taskID)
	if err != nil {
		return fmt.Errorf("reading task %s reports: %w", taskID, err)
	}
	if kind == taskauthority.KindScout {
		unsettled = slices.DeleteFunc(unsettled, func(line string) bool { return domain.LineVerb(line) == "done" })
	}
	if len(unsettled) > 0 {
		return fmt.Errorf("task %s has %d report(s) its parent has not settled: settle them with munsu wake claim and munsu wake resolve, or retire anyway with munsu teardown --force %s", taskID, len(unsettled), taskID)
	}
	return nil
}

// PrepareForcedRetirementEvidence preserves forced-retirement evidence under
// state/.backup/<durable-stem>/<durable-stem>.status; the logical task ID is
// used only to derive the durable stem.
func PrepareForcedRetirementEvidence(homeDir, taskID string) ([]string, error) {
	stem, err := home.DurableKey(taskID)
	if err != nil {
		return nil, err
	}
	backupDir := filepath.Join(homeDir, "state", ".backup", stem)
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return nil, err
	}
	statusPath, err := home.StatusFilePath(homeDir, taskID)
	if err != nil {
		return nil, err
	}
	if src, err := home.ReadRegularFile(statusPath); err == nil {
		if err := os.WriteFile(filepath.Join(backupDir, filepath.Base(statusPath)), src, 0600); err != nil {
			return nil, err
		}
	}
	return []string{"evidence preserved to state/.backup/" + stem + " (--force)"}, nil
}

func FinalizeRetirementJournals(homeDir, taskID string) ([]string, error) {
	var steps []string
	statusPath, err := home.StatusFilePath(homeDir, taskID)
	if err != nil {
		return steps, err
	}
	for _, activity := range home.OpenActivities(statusPath) {
		line := fmt.Sprintf("resolved [key=%s]: soldier torn down", activity.Key)
		if err := home.AppendStatus(homeDir, taskID, line); err != nil {
			return steps, err
		}
		if _, err := Append(homeDir, "task.status", taskID, activity.Key, line); err != nil {
			return steps, err
		}
		steps = append(steps, fmt.Sprintf("closed keyed phase [key=%s]", activity.Key))
	}
	return steps, nil
}
