package taskauthority

import (
	"encoding/json"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
)

// CanonicalBindWorktreeRequest binds one generation-scoped worktree lease to
// a task. The request is the typed intent for the operation digest.
type CanonicalBindWorktreeRequest struct {
	HomeID       domain.HomeID
	TaskID       domain.TaskID
	Precondition domain.Precondition
	Binding      WorktreeBinding
	Reason       string
}

func (r CanonicalBindWorktreeRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID     string          `json:"home_id"`
		TaskID     string          `json:"task_id"`
		Generation uint64          `json:"generation"`
		Revision   uint64          `json:"revision"`
		Binding    WorktreeBinding `json:"binding"`
		Reason     string          `json:"reason,omitempty"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.Binding, r.Reason})
}

// BindWorktree is the canonical operation that binds one generation-scoped
// worktree lease to a task. The mutation is generation-bound and fenced: the
// request carries the expected Generation/Revision precondition, and the
// committed worktree binding is stored on the task's current Aggregate.
// Binding an already-bound generation fails closed with a typed conflict, and
// a stale precondition fails closed with a typed domain.Conflict. Repeating
// the same Operation ID with the same digest replays the durable prior
// outcome; reusing the Operation ID with a different intent conflicts.
func (c *Canonical) BindWorktree(op domain.Operation, req CanonicalBindWorktreeRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateWorktreeBinding(req.Binding); err != nil {
		return Outcome{}, err
	}
	return c.mutateTask(op, req.TaskID, req.Precondition, func(cur Aggregate) (Aggregate, error) {
		if cur.Worktree != nil {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s already has a worktree binding", cur.TaskID, cur.Generation)
		}
		next := cur.clone()
		next.Worktree = &req.Binding
		next.Revision++
		return next, nil
	})
}

// CanonicalBindEndpointRequest binds one generation-scoped endpoint lease to a
// task and transitions the task into working. The request is the typed intent
// for the operation digest.
type CanonicalBindEndpointRequest struct {
	HomeID       domain.HomeID
	TaskID       domain.TaskID
	Precondition domain.Precondition
	Binding      EndpointBinding
	Reason       string
}

func (r CanonicalBindEndpointRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID     string          `json:"home_id"`
		TaskID     string          `json:"task_id"`
		Generation uint64          `json:"generation"`
		Revision   uint64          `json:"revision"`
		Binding    EndpointBinding `json:"binding"`
		Reason     string          `json:"reason,omitempty"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.Binding, r.Reason})
}

// BindEndpoint is the canonical operation that binds one generation-scoped
// endpoint lease to a task and transitions the queued task into working. It
// is generation-bound and fenced: the request carries the expected
// Generation/Revision precondition. The mutation requires a bound worktree,
// a queued phase, an owner, and no existing endpoint binding, and it evaluates
// durable Dispatch Holds for the spawn action before committing. A stale
// precondition fails closed with a typed domain.Conflict; an already-bound
// endpoint or a non-queued phase fails closed with a typed conflict. Repeating
// the same Operation ID with the same digest replays the durable prior
// outcome; reusing the Operation ID with a different intent conflicts.
func (c *Canonical) BindEndpoint(op domain.Operation, req CanonicalBindEndpointRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateEndpointBinding(req.Binding); err != nil {
		return Outcome{}, err
	}
	return c.mutateTask(op, req.TaskID, req.Precondition, func(cur Aggregate) (Aggregate, error) {
		if cur.Worktree == nil {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s has no worktree binding; bind endpoint requires a bound worktree", cur.TaskID, cur.Generation)
		}
		if strings.TrimSpace(cur.Definition.Owner) == "" {
			return Aggregate{}, conflictError(ErrPrecondition, "task %s generation %s is not ready to spawn: %s", cur.TaskID, cur.Generation, ReadinessMissingOwner)
		}
		if cur.Phase != PhaseQueued {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s is %s, bind endpoint requires queued", cur.TaskID, cur.Generation, cur.Phase)
		}
		if cur.Endpoint != nil {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s already has an endpoint binding", cur.TaskID, cur.Generation)
		}
		holds, err := c.listHolds()
		if err != nil {
			return Aggregate{}, err
		}
		if holdsBlockAction(holds, DispatchActionSpawn, cur) {
			return Aggregate{}, conflictError(ErrDispatchHeld, "%s: dispatch is held for %s", ErrDispatchHeld, cur.TaskID)
		}
		next := cur.clone()
		next.Endpoint = &req.Binding
		next.Phase = PhaseWorking
		next.PhaseDetail = req.Reason
		next.Revision++
		return next, nil
	})
}

// holdsBlockAction reports whether any committed hold for the given action
// matches the task.
func holdsBlockAction(holds []DispatchHold, action DispatchAction, agg Aggregate) bool {
	for _, hold := range holds {
		if hold.Matches(action, agg.TaskID, agg.Definition.Project, agg.Generation.String(), agg.Definition.ParentTaskID) {
			return true
		}
	}
	return false
}