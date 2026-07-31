package cli

import "github.com/minhtri2710/munsu/internal/home"

type BacklogReadinessRow struct {
	TaskID          string   `json:"task_id"`
	Generation      string   `json:"generation,omitempty"`
	Ready           bool     `json:"ready"`
	BlockingReasons []string `json:"blocking_reasons,omitempty"`
}

func backlogReadinessRow(readiness home.TaskReadiness) BacklogReadinessRow {
	reasons := make([]string, len(readiness.BlockingReasons))
	for i, reason := range readiness.BlockingReasons {
		reasons[i] = string(reason)
	}
	return BacklogReadinessRow{
		TaskID:          readiness.TaskID,
		Generation:      readiness.Generation,
		Ready:           readiness.Ready,
		BlockingReasons: reasons,
	}
}
