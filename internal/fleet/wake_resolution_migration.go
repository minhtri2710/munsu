package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

type WakeResolutionFleetPlan struct {
	ParentHome string                           `json:"parent_home"`
	Outcomes   []WakeResolutionFleetHomeOutcome `json:"outcomes"`
}

type WakeResolutionFleetHomeOutcome struct {
	Home         string                                `json:"home"`
	Status       string                                `json:"status"`
	SourceDigest string                                `json:"source_digest,omitempty"`
	RecordCount  int                                   `json:"record_count,omitempty"`
	Command      string                                `json:"command,omitempty"`
	Error        string                                `json:"error,omitempty"`
	Plan         *mhome.WakeResolutionMigrationPlan    `json:"plan,omitempty"`
	Receipt      *mhome.WakeResolutionMigrationReceipt `json:"-"`
}

func WriteWakeResolutionFleetPlan(path string, plan WakeResolutionFleetPlan) error {
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) != string(data) {
			return errFleetPlanConflict(path)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-fleet-plan-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func errFleetPlanConflict(path string) error {
	return &fleetPlanConflictError{path: path}
}

type fleetPlanConflictError struct{ path string }

func (e *fleetPlanConflictError) Error() string {
	return "wake resolution fleet plan conflict at " + e.path
}

func ReadWakeResolutionFleetPlan(path string) (WakeResolutionFleetPlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WakeResolutionFleetPlan{}, err
	}
	var plan WakeResolutionFleetPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return WakeResolutionFleetPlan{}, err
	}
	return plan, nil
}

func PlanFleetWakeResolutionMigration(parentHome string) WakeResolutionFleetPlan {
	plan := WakeResolutionFleetPlan{ParentHome: parentHome}
	plan.Outcomes = append(plan.Outcomes, planWakeResolutionHome(parentHome))
	entries, err := ParseRegistry(CaptainRegistryPath(parentHome))
	if err != nil {
		plan.Outcomes = append(plan.Outcomes, WakeResolutionFleetHomeOutcome{Home: parentHome, Status: "error", Error: err.Error()})
		return plan
	}
	for _, entry := range entries {
		if entry.Home == "" {
			plan.Outcomes = append(plan.Outcomes, WakeResolutionFleetHomeOutcome{Status: "error", Error: "captain registry entry missing home"})
			continue
		}
		plan.Outcomes = append(plan.Outcomes, planWakeResolutionHome(entry.Home))
	}
	return plan
}

func planWakeResolutionHome(homeDir string) WakeResolutionFleetHomeOutcome {
	homePlan, err := mhome.PlanWakeResolutionMigration(homeDir)
	if err != nil {
		status := "error"
		if strings.Contains(err.Error(), "legacy wake resolution state not found") {
			status = "skipped"
		}
		return WakeResolutionFleetHomeOutcome{Home: homeDir, Status: status, Command: mhome.WakeResolutionMigrationCommand(homeDir), Error: err.Error()}
	}
	return WakeResolutionFleetHomeOutcome{Home: homeDir, Status: "planned", SourceDigest: homePlan.SourceDigest, RecordCount: homePlan.RecordCount, Command: mhome.WakeResolutionMigrationCommand(homeDir), Plan: homePlan}
}

func ApplyFleetWakeResolutionMigration(plan WakeResolutionFleetPlan) WakeResolutionFleetPlan {
	result := WakeResolutionFleetPlan{ParentHome: plan.ParentHome}
	for _, outcome := range plan.Outcomes {
		if outcome.Plan == nil {
			if outcome.Status == "error" || outcome.Status == "skipped" {
				result.Outcomes = append(result.Outcomes, outcome)
			} else {
				outcome.Status = "error"
				outcome.Error = "missing single-home migration plan"
				result.Outcomes = append(result.Outcomes, outcome)
			}
			continue
		}
		receipt, err := mhome.ApplyWakeResolutionMigration(outcome.Plan)
		if err != nil {
			outcome.Status = "error"
			outcome.Error = err.Error()
			result.Outcomes = append(result.Outcomes, outcome)
			continue
		}
		outcome.Status = "applied"
		outcome.Receipt = receipt
		outcome.SourceDigest = receipt.SourceDigest
		outcome.RecordCount = receipt.RecordCount
		result.Outcomes = append(result.Outcomes, outcome)
	}
	return result
}
