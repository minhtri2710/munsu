package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/home"
)

func VerifyRetirementContinuity(homeDir, taskID string) error {
	if HasPendingReport(homeDir, taskID) || HasAnyOpenReport(homeDir, taskID) {
		return fmt.Errorf("uplink report not acknowledged: Processing Ack is still pending for task %s", taskID)
	}
	open, err := IsTaskReportRelayOpen(homeDir, taskID)
	if err != nil {
		return fmt.Errorf("reading task obligations: %w", err)
	}
	if !open {
		return nil
	}
	hasMaterial, err := MaterialReportExists(homeDir, taskID)
	if err != nil {
		return fmt.Errorf("checking material report: %w", err)
	}
	if hasMaterial {
		return fmt.Errorf("terminal report-relay not acknowledged for task %s", taskID)
	}
	return nil
}

// PrepareForcedRetirementEvidence preserves forced-retirement evidence under
// state/.backup/<durable-stem>/<durable-stem>.status (and matching receipt
// basenames); the logical task ID is used only to derive the durable stem.
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
	if src, err := os.ReadFile(statusPath); err == nil {
		if err := os.WriteFile(filepath.Join(backupDir, filepath.Base(statusPath)), src, 0600); err != nil {
			return nil, err
		}
	}
	if entries, err := os.ReadDir(ReceiptDir(homeDir)); err == nil {
		prefix := stem + "."
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), prefix) {
				continue
			}
			src, err := os.ReadFile(filepath.Join(ReceiptDir(homeDir), entry.Name()))
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(backupDir, entry.Name()), src, 0600); err != nil {
				return nil, err
			}
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
		if err := AppendWithID(homeDir, SyntheticEventID(), "task.status", taskID, activity.Key, line); err != nil {
			return steps, err
		}
		steps = append(steps, fmt.Sprintf("closed keyed phase [key=%s]", activity.Key))
	}
	if err := ClearTaskCompleted(homeDir, taskID); err != nil {
		return steps, err
	}
	steps = append(steps, "task obligations cleared")
	return steps, nil
}
