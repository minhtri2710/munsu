package cli

import "github.com/minhtri2710/munsu/internal/taskauthority"

type BacklogReadinessRow struct {
	TaskID          string   `json:"task_id"`
	Generation      string   `json:"generation,omitempty"`
	Ready           bool     `json:"ready"`
	BlockingReasons []string `json:"blocking_reasons,omitempty"`
}

func backlogReadinessRow(readiness taskauthority.Readiness) BacklogReadinessRow {
	reasons := make([]string, len(readiness.BlockingReasons))
	for i, reason := range readiness.BlockingReasons {
		reasons[i] = string(reason)
	}
	generation := ""
	if readiness.Generation != 0 {
		generation = readiness.Generation.String()
	}
	return BacklogReadinessRow{
		TaskID:          readiness.TaskID,
		Generation:      generation,
		Ready:           readiness.Ready,
		BlockingReasons: reasons,
	}
}
