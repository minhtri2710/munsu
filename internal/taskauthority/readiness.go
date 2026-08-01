package taskauthority

import (
	"strings"
)

// ReadinessReason is a typed reason why a task is not ready.
type ReadinessReason string

const (
	ReadinessNotFound     ReadinessReason = "not-found"
	ReadinessMissingOwner ReadinessReason = "missing-owner"
	ReadinessBlocked      ReadinessReason = "blocked"
	ReadinessInFlight     ReadinessReason = "in-flight"
	ReadinessTerminal     ReadinessReason = "terminal"
	ReadinessDispatchHold ReadinessReason = "dispatch-hold"
)

// Readiness is the canonical readiness evaluation of one task. Watcher health
// and degraded supervision are deliberately absent: they are checked by
// fleet/CLI orchestration outside Task Authority.
type Readiness struct {
	TaskID          string
	Generation      Generation
	Ready           bool
	BlockingReasons []ReadinessReason
}

// Readiness evaluates the current authoritative task state against the start
// action's durable dispatch control.
func (a *Authority) Readiness(taskID string) (Readiness, error) {
	if err := validateTaskID(taskID); err != nil {
		return Readiness{}, err
	}
	v, err := a.store.View()
	if err != nil {
		return Readiness{}, err
	}
	agg, ok := v.Current(taskID)
	if !ok {
		return Readiness{TaskID: taskID, BlockingReasons: []ReadinessReason{ReadinessNotFound}}, nil
	}
	result := Readiness{TaskID: taskID, Generation: agg.Generation}
	if holdsBlockStart(v, agg) {
		result.BlockingReasons = append(result.BlockingReasons, ReadinessDispatchHold)
	}
	if strings.TrimSpace(agg.Definition.Owner) == "" {
		result.BlockingReasons = append(result.BlockingReasons, ReadinessMissingOwner)
	}
	switch agg.Phase {
	case PhaseQueued:
		if len(result.BlockingReasons) == 0 {
			result.Ready = true
		}
	case PhaseBlocked:
		result.BlockingReasons = append(result.BlockingReasons, ReadinessBlocked)
	case PhaseWorking:
		result.BlockingReasons = append(result.BlockingReasons, ReadinessInFlight)
	default: // done, resolved, retired
		result.BlockingReasons = append(result.BlockingReasons, ReadinessTerminal)
	}
	return result, nil
}

// holdsBlockStart reports whether any committed start hold matches the task.
func holdsBlockStart(v View, agg Aggregate) bool {
	for _, hold := range v.Holds {
		if hold.Matches(DispatchActionStart, agg.TaskID, agg.Definition.Project, agg.Generation.String(), agg.Definition.ParentTaskID) {
			return true
		}
	}
	return false
}
