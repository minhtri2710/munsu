package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
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

func PrepareForcedRetirementEvidence(homeDir, taskID string) ([]string, error) {
	backupDir := filepath.Join(homeDir, "state", ".backup", taskID)
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return nil, err
	}
	stateDir := filepath.Join(homeDir, "state")
	if src, err := os.ReadFile(filepath.Join(stateDir, taskID+".status")); err == nil {
		if err := os.WriteFile(filepath.Join(backupDir, taskID+".status"), src, 0600); err != nil {
			return nil, err
		}
	}
	if entries, err := os.ReadDir(ReceiptDir(homeDir)); err == nil {
		prefix := taskID + "."
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
	return []string{"evidence preserved to state/.backup/" + taskID + " (--force)"}, nil
}

func FinalizeRetirementJournals(homeDir, taskID string) ([]string, error) {
	var steps []string
	statusPath := filepath.Join(homeDir, "state", taskID+".status")
	for _, activity := range domain.OpenActivities(statusPath) {
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
