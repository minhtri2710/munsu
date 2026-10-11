package taskauthority

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
