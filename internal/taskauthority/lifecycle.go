package taskauthority

import (
	"strings"
)

// CreateRequest creates one new task at Generation 1, Revision 1, phase
// queued. No ExpectedGeneration applies: the task must not exist.
type CreateRequest struct {
	OperationID  string
	Actor        Actor
	TaskID       string
	Owner        string
	Description  string
	Kind         string
	Project      string
	ParentTaskID string
	Reason       string
}

// Create is the named semantic operation that creates one queued Task
// Generation. Repeating the same Operation ID replays; creating an existing
// task conflicts.
func (a *Authority) Create(req CreateRequest) (Result, error) {
	if err := validateTaskID(req.TaskID); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(req.Owner) == "" {
		return Result{}, validationError("create requires an owner")
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID       string `json:"task_id"`
		Owner        string `json:"owner"`
		Description  string `json:"description"`
		Kind         string `json:"kind"`
		Project      string `json:"project"`
		ParentTaskID string `json:"parent_task_id"`
		Reason       string `json:"reason"`
	}{req.TaskID, req.Owner, req.Description, req.Kind, req.Project, req.ParentTaskID, req.Reason})
	if err != nil {
		return Result{}, err
	}
	receipt, err := a.store.Update(op, func(tx *Tx) error {
		if _, ok := tx.Current(req.TaskID); ok {
			return conflictError(ErrConflict, "task %s already exists", req.TaskID)
		}
		agg, err := NewAggregate(req.TaskID, req.Owner, req.Description, req.Kind, req.Project, req.ParentTaskID)
		if err != nil {
			return err
		}
		if err := tx.PutAggregate(agg); err != nil {
			return err
		}
		return tx.AppendAudit(a.audit(op, agg.TaskID, agg.Generation, req.Reason, "", agg.Phase))
	})
	if err != nil {
		return Result{}, err
	}
	return resultFromReceipt(receipt)
}

// StartRequest starts a queued task into working.
type StartRequest struct {
	OperationID        string
	Actor              Actor
	TaskID             string
	ExpectedGeneration Generation
	Reason             string
}

// Start is the named semantic operation that transitions a queued task to
// working, checking applicable Dispatch Holds inside the same Store
// transaction so a concurrent hold creation cannot interleave.
func (a *Authority) Start(req StartRequest) (Result, error) {
	return a.phaseTransition(req.OperationID, req.Actor, req.TaskID, req.ExpectedGeneration, PhaseWorking, "", req.Reason, func(tx *Tx, cur Aggregate) error {
		if cur.Phase != PhaseQueued {
			return preconditionError("start requires queued task")
		}
		return checkHolds(tx, DispatchActionStart, cur)
	})
}

// BlockRequest blocks a queued or working task.
type BlockRequest struct {
	OperationID        string
	Actor              Actor
	TaskID             string
	ExpectedGeneration Generation
	Detail             string
	Reason             string
}

// Block is the named semantic operation that transitions a queued or working
// task into blocked.
func (a *Authority) Block(req BlockRequest) (Result, error) {
	return a.phaseTransition(req.OperationID, req.Actor, req.TaskID, req.ExpectedGeneration, PhaseBlocked, req.Detail, req.Reason, func(tx *Tx, cur Aggregate) error {
		if cur.Phase != PhaseQueued && cur.Phase != PhaseWorking {
			return preconditionError("block requires queued or working task")
		}
		return nil
	})
}

// UnblockRequest unblocks a blocked task back to queued.
type UnblockRequest struct {
	OperationID        string
	Actor              Actor
	TaskID             string
	ExpectedGeneration Generation
	Reason             string
}

// Unblock is the named semantic operation that returns a blocked task to
// queued.
func (a *Authority) Unblock(req UnblockRequest) (Result, error) {
	return a.phaseTransition(req.OperationID, req.Actor, req.TaskID, req.ExpectedGeneration, PhaseQueued, "", req.Reason, func(tx *Tx, cur Aggregate) error {
		if cur.Phase != PhaseBlocked {
			return preconditionError("unblock requires blocked task")
		}
		return nil
	})
}

// CompleteRequest completes a non-terminal task into a terminal phase.
type CompleteRequest struct {
	OperationID        string
	Actor              Actor
	TaskID             string
	ExpectedGeneration Generation
	To                 Phase
	Reason             string
}

// Complete is the named semantic operation that transitions a non-terminal
// task into a terminal phase (done or resolved). Delivery-completion rules
// bind provider evidence in a later slice; this operation only owns the
// lifecycle transition.
func (a *Authority) Complete(req CompleteRequest) (Result, error) {
	if req.To != PhaseDone && req.To != PhaseResolved {
		return Result{}, validationError("complete target %q is not a terminal phase", req.To)
	}
	return a.phaseTransition(req.OperationID, req.Actor, req.TaskID, req.ExpectedGeneration, req.To, "", req.Reason, func(tx *Tx, cur Aggregate) error {
		if cur.Phase.terminal() {
			return preconditionError("complete requires a non-terminal task")
		}
		return nil
	})
}

// ReopenRequest reopens a terminal task into a new Generation.
type ReopenRequest struct {
	OperationID        string
	Actor              Actor
	TaskID             string
	ExpectedGeneration Generation
	Reason             string
}

// Reopen is the named semantic operation that creates the next Task
// Generation at Revision 1 and preserves the prior Generation as a historical
// record. Bindings are not carried into the new Generation.
func (a *Authority) Reopen(req ReopenRequest) (Result, error) {
	if err := req.ExpectedGeneration.Validate(); err != nil {
		return Result{}, err
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID             string `json:"task_id"`
		ExpectedGeneration uint64 `json:"expected_generation"`
		Reason             string `json:"reason"`
	}{req.TaskID, uint64(req.ExpectedGeneration), req.Reason})
	if err != nil {
		return Result{}, err
	}
	receipt, err := a.store.Update(op, func(tx *Tx) error {
		cur, ok := tx.Current(req.TaskID)
		if !ok {
			return conflictError(ErrNotFound, "task %s not found", req.TaskID)
		}
		if cur.Generation != req.ExpectedGeneration {
			return conflictError(ErrConflict, "task %s is at generation %s, expected %s", req.TaskID, cur.Generation, req.ExpectedGeneration)
		}
		if !cur.Phase.terminal() {
			return preconditionError("reopen requires terminal task")
		}
		next, err := cur.Generation.Next()
		if err != nil {
			return err
		}
		historical := cur.clone()
		historical.Current = false
		historical.Revision++
		if err := tx.PutAggregate(historical); err != nil {
			return err
		}
		newGen := Aggregate{
			SchemaVersion: taskAuthoritySchema,
			TaskID:        cur.TaskID,
			Generation:    next,
			Revision:      FirstRevision,
			Current:       true,
			Definition:    cur.Definition,
			Phase:         PhaseQueued,
		}
		if err := tx.PutAggregate(newGen); err != nil {
			return err
		}
		return tx.AppendAudit(a.audit(op, cur.TaskID, next, req.Reason, cur.Phase, PhaseQueued))
	})
	if err != nil {
		return Result{}, err
	}
	return resultFromReceipt(receipt)
}

// phaseTransition is the shared envelope for single-phase lifecycle
// operations: generation fence, precondition evaluation, Revision advance,
// and typed audit commit inside one Store transaction.
func (a *Authority) phaseTransition(operationID string, actor Actor, taskID string, expected Generation, after Phase, detail, reason string, precondition func(tx *Tx, cur Aggregate) error) (Result, error) {
	if err := expected.Validate(); err != nil {
		return Result{}, err
	}
	op, err := a.operation(operationID, actor, phaseRequest{
		TaskID:   taskID,
		Expected: uint64(expected),
		After:    after,
		Detail:   detail,
		Reason:   reason,
	})
	if err != nil {
		return Result{}, err
	}
	receipt, err := a.store.Update(op, func(tx *Tx) error {
		cur, ok := tx.Current(taskID)
		if !ok {
			return conflictError(ErrNotFound, "task %s not found", taskID)
		}
		if cur.Generation != expected {
			return conflictError(ErrConflict, "task %s is at generation %s, expected %s", taskID, cur.Generation, expected)
		}
		if err := precondition(tx, cur); err != nil {
			return err
		}
		updated := cur.clone()
		updated.Phase = after
		updated.PhaseDetail = detail
		updated.Revision++
		if err := tx.PutAggregate(updated); err != nil {
			return err
		}
		return tx.AppendAudit(a.audit(op, cur.TaskID, cur.Generation, reason, cur.Phase, after))
	})
	if err != nil {
		return Result{}, err
	}
	return resultFromReceipt(receipt)
}

// phaseRequest is the digest carrier for single-phase transitions.
type phaseRequest struct {
	TaskID   string `json:"task_id"`
	Expected uint64 `json:"expected_generation"`
	After    Phase  `json:"after"`
	Detail   string `json:"detail,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// operation builds a validated Operation with the computed intent digest.
func (a *Authority) operation(id string, actor Actor, payload any) (Operation, error) {
	digest, err := requestDigest(struct {
		Actor   Actor `json:"actor"`
		Payload any   `json:"payload"`
	}{actor, payload})
	if err != nil {
		return Operation{}, err
	}
	op := Operation{ID: id, Digest: digest, Actor: actor}
	if err := op.Validate(); err != nil {
		return Operation{}, err
	}
	return op, nil
}

// checkHolds evaluates durable dispatch control against the task's current
// authoritative state inside the transaction.
func checkHolds(tx *Tx, action DispatchAction, agg Aggregate) error {
	for _, hold := range tx.Holds() {
		if hold.Matches(action, agg.TaskID, agg.Definition.Project, agg.Generation.String(), agg.Definition.ParentTaskID) {
			return conflictError(ErrDispatchHeld, "%s: %s (%s)", ErrDispatchHeld, hold.ID, hold.Reason)
		}
	}
	return nil
}

func preconditionError(format string, args ...any) error {
	return conflictError(ErrPrecondition, format, args...)
}
