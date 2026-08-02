package taskauthority

import (
	"encoding/json"
	"strings"
)

// LeaseMarker is the durable worktree lease marker committed in the same
// Store transaction as the worktree binding it accompanies. The lease read
// path (internal/home.TaskWorktreeLeaseActive) parses the marker document
// unchanged, so the JSON renders the generation as a decimal string.
type LeaseMarker struct {
	TaskID         string     `json:"task_id"`
	TaskGeneration Generation `json:"task_generation"`
	LeaseID        string     `json:"lease_id"`
	FenceToken     string     `json:"fence_token"`
}

// Validate checks the marker shape: a safe task identity, a positive
// generation, and non-empty lease and fence tokens.
func (m LeaseMarker) Validate() error {
	if err := validateTaskID(m.TaskID); err != nil {
		return err
	}
	if err := m.TaskGeneration.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(m.LeaseID) == "" {
		return validationError("lease marker missing lease id")
	}
	if strings.TrimSpace(m.FenceToken) == "" {
		return validationError("lease marker missing fence token")
	}
	return nil
}

// MarshalJSON renders the marker in the home-compatible format: the
// generation is a decimal string so the legacy lease read path parses it
// unchanged.
func (m LeaseMarker) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		TaskID         string `json:"task_id"`
		TaskGeneration string `json:"task_generation"`
		LeaseID        string `json:"lease_id"`
		FenceToken     string `json:"fence_token"`
	}{
		TaskID:         m.TaskID,
		TaskGeneration: m.TaskGeneration.String(),
		LeaseID:        m.LeaseID,
		FenceToken:     m.FenceToken,
	})
}

// BindWorktreeRequest binds one generation-scoped worktree to a task.
type BindWorktreeRequest struct {
	OperationID        string
	Actor              Actor
	TaskID             string
	ExpectedGeneration Generation
	Binding            WorktreeBinding
	Reason             string
}

// BindWorktree is the named semantic operation that commits a worktree
// binding and its lease marker atomically inside one Store transaction.
// It validates the full binding payload (repository identity, path,
// Git/Common directories, head, lease, fence token, bound timestamp)
// against the current Generation, advances the Task Revision, commits a
// typed binding audit event, and returns the durable lifecycle receipt.
// Repeating the same Operation ID with the same intent replays the original
// receipt; binding an already-bound generation fails closed with a typed
// conflict; reusing the Operation ID with a changed intent is a typed
// non-retryable conflict.
func (a *Authority) BindWorktree(req BindWorktreeRequest) (Result, error) {
	if err := req.ExpectedGeneration.Validate(); err != nil {
		return Result{}, err
	}
	if err := validateWorktreeBinding(req.Binding); err != nil {
		return Result{}, err
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID             string          `json:"task_id"`
		ExpectedGeneration uint64          `json:"expected_generation"`
		Binding            WorktreeBinding `json:"binding"`
		Reason             string          `json:"reason,omitempty"`
	}{req.TaskID, uint64(req.ExpectedGeneration), req.Binding, req.Reason})
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
		if cur.Worktree != nil {
			return conflictError(ErrConflict, "task %s generation %s already has a worktree binding", req.TaskID, cur.Generation)
		}
		updated := cur.clone()
		updated.Worktree = &req.Binding
		updated.Revision++
		if err := tx.PutAggregate(updated); err != nil {
			return err
		}
		marker := LeaseMarker{
			TaskID:         cur.TaskID,
			TaskGeneration: cur.Generation,
			LeaseID:        req.Binding.LeaseID,
			FenceToken:     req.Binding.FenceToken,
		}
		if err := tx.PutLeaseMarker(marker); err != nil {
			return err
		}
		return tx.AppendAudit(AuditEvent{
			OperationID: op.ID,
			Actor:       op.Actor,
			Kind:        AuditBinding,
			TaskID:      cur.TaskID,
			Generation:  cur.Generation,
			Reason:      req.Reason,
			At:          a.now().UnixNano(),
		})
	})
	if err != nil {
		return Result{}, err
	}
	return resultFromReceipt(receipt)
}
