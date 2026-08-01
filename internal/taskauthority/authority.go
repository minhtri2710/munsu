package taskauthority

import (
	"time"
)

// Authority is the concrete deep module that owns task lifecycle, readiness,
// and durable dispatch control. It is constructed over a Store and is not an
// interface that callers replace; the Store is the only implementation seam.
type Authority struct {
	store Store
	now   func() time.Time
}

// New constructs an Authority over the given Store.
func New(store Store) *Authority {
	return &Authority{store: store, now: time.Now}
}

// Result is the caller-visible outcome of one named lifecycle operation.
type Result struct {
	TaskID     string
	Generation Generation
	Revision   Revision
	Phase      Phase
	Reopened   bool
}

// Get returns the current authoritative Aggregate for the task.
func (a *Authority) Get(taskID string) (Aggregate, error) {
	if err := validateTaskID(taskID); err != nil {
		return Aggregate{}, err
	}
	v, err := a.store.View()
	if err != nil {
		return Aggregate{}, err
	}
	agg, ok := v.Current(taskID)
	if !ok {
		return Aggregate{}, conflictError(ErrNotFound, "task %s not found", taskID)
	}
	return agg, nil
}

// List returns the current authoritative Aggregates sorted by task ID.
func (a *Authority) List() ([]Aggregate, error) {
	v, err := a.store.View()
	if err != nil {
		return nil, err
	}
	ids := v.sortedTaskIDs()
	out := make([]Aggregate, 0, len(ids))
	for _, id := range ids {
		agg, _ := v.Current(id)
		out = append(out, agg)
	}
	return out, nil
}

// resultFromView reads back the current aggregate of a task after an
// operation; it also serves replayed operations whose callback did not run.
func (a *Authority) resultFromView(taskID string) (Result, error) {
	agg, err := a.Get(taskID)
	if err != nil {
		return Result{}, err
	}
	return Result{
		TaskID:     agg.TaskID,
		Generation: agg.Generation,
		Revision:   agg.Revision,
		Phase:      agg.Phase,
	}, nil
}

// audit builds a lifecycle audit event for one authoritative mutation.
func (a *Authority) audit(op Operation, taskID string, generation Generation, reason string, before, after Phase) AuditEvent {
	return AuditEvent{
		OperationID: op.ID,
		Actor:       op.Actor,
		Kind:        AuditLifecycle,
		TaskID:      taskID,
		Generation:  generation,
		Reason:      reason,
		Before:      before,
		After:       after,
		At:          a.now().UnixNano(),
	}
}

// dispatchAudit builds a dispatch-control audit event.
func (a *Authority) dispatchAudit(op Operation, reason string) AuditEvent {
	return AuditEvent{
		OperationID: op.ID,
		Actor:       op.Actor,
		Kind:        AuditDispatch,
		Reason:      reason,
		At:          a.now().UnixNano(),
	}
}
