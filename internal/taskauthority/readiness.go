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
	// ReadinessReservedForTransfer reports a queued task whose current
	// generation is actively reserved for transfer: unrelated dispatch/
	// readiness activity is fenced and the task is not ready.
	ReadinessReservedForTransfer ReadinessReason = "reserved-for-transfer"
	// ReadinessNotCurrent reports a task generation that is superseded or not
	// the current/active generation: it is not ready and not current Task
	// truth.
	ReadinessNotCurrent ReadinessReason = "not-current"
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

// evaluateReadiness evaluates one aggregate's readiness against the given
// committed holds. A non-current/superseded generation is never ready; an
// actively reserved-for-transfer generation is never ready (unrelated dispatch
// activity is fenced). Dispatch Holds are independent controls and cannot
// override either reason.
func evaluateReadiness(holds []DispatchHold, agg Aggregate) Readiness {
	result := Readiness{TaskID: agg.TaskID, Generation: agg.Generation}
	if !agg.Current {
		result.BlockingReasons = append(result.BlockingReasons, ReadinessNotCurrent)
		return result
	}
	if activeReservation(agg.Transfer) {
		result.BlockingReasons = append(result.BlockingReasons, ReadinessReservedForTransfer)
		return result
	}
	if holdsBlockStart(holds, agg) {
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
	return result
}

// activeReservation reports whether the transfer state is an active source
// reservation (set by ReserveTransfer and not yet committed) that fences the
// task's unrelated dispatch/readiness activity.
func activeReservation(ts *TransferState) bool {
	return ts != nil && !ts.Transferred && ts.DestinationHome != ""
}

// holdsBlockStart reports whether any committed start hold matches the task.
func holdsBlockStart(holds []DispatchHold, agg Aggregate) bool {
	return holdsBlockAction(holds, DispatchActionStart, agg)
}
