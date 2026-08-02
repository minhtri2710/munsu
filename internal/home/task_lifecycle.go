package home

import (
	"errors"
)

type ReadinessReason string

const (
	ReadinessMissingOwner ReadinessReason = "missing-owner"
	ReadinessQueued       ReadinessReason = "queued"
	ReadinessBlocked      ReadinessReason = "blocked"
	ReadinessInFlight     ReadinessReason = "in-flight"
	ReadinessTerminal     ReadinessReason = "terminal"
	ReadinessDispatchHold ReadinessReason = "dispatch-hold"
)

type TaskReadiness struct {
	TaskID          string
	Generation      string
	Ready           bool
	BlockingReasons []ReadinessReason
}

// QueryTaskReadiness is the legacy v1 readiness evaluation retained only as
// the Phase 6 handoff compatibility adapter's snapshot read
// (EvaluateDispatchWithDependencies gathers its interpretation readiness
// through this query); the CLI and fleet readiness surface uses the canonical
// Authority.Readiness. It is removed when the handoff saga cuts over to two
// Authorities (Task 6.x).
func QueryTaskReadiness(homeDir, taskID string) (TaskReadiness, error) {
	if err := validateTaskID(taskID); err != nil {
		return TaskReadiness{}, err
	}
	agg, ok, err := ReadCurrentTaskAggregate(homeDir, taskID)
	if err != nil {
		return TaskReadiness{}, err
	}
	if !ok {
		return TaskReadiness{TaskID: taskID, BlockingReasons: []ReadinessReason{"not-found"}}, nil
	}
	result := TaskReadiness{TaskID: taskID, Generation: agg.Generation}
	if err := checkDispatchHoldUnlocked(homeDir, DispatchActionStart, taskID, agg.Project, agg.Generation, ""); err != nil {
		if errors.Is(err, ErrDispatchHeld) {
			result.BlockingReasons = append(result.BlockingReasons, ReadinessDispatchHold)
		}
	}
	if agg.Owner == "" {
		result.BlockingReasons = append(result.BlockingReasons, ReadinessMissingOwner)
	}
	switch agg.State {
	case "queued", "":
		if len(result.BlockingReasons) == 0 {
			result.Ready = true
		}
	case "blocked":
		result.BlockingReasons = append(result.BlockingReasons, ReadinessBlocked)
	case "working", "in-flight":
		result.BlockingReasons = append(result.BlockingReasons, ReadinessInFlight)
	case "done", "resolved", "retired":
		result.BlockingReasons = append(result.BlockingReasons, ReadinessTerminal)
	default:
		result.BlockingReasons = append(result.BlockingReasons, ReadinessTerminal)
	}
	return result, nil
}
