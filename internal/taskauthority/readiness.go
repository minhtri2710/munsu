package taskauthority

// ReadinessReason is a typed reason why a task is not ready.
type ReadinessReason string

const ReadinessMissingOwner ReadinessReason = "missing-owner"

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
