package fleet

import (
	"errors"
	"fmt"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// MergeAndRetireResult captures the full outcome of a composite
// merge-and-retire operation. The delivery execution and the retirement are
// independently durable and idempotent. When the delivery completed but
// retirement did not, the result is non-zero and retry resumes retirement
// only.
type MergeAndRetireResult struct {
	// MergeOutcome is the committed canonical delivery outcome status of the
	// delivery phase (completed, partial, remote-unknown, retryable).
	MergeOutcome taskauthority.DeliveryOutcomeStatus

	// MergeDetail is a human-readable detail about the delivery outcome.
	MergeDetail string

	// TeardownResult is the result of the retire/teardown phase, populated
	// when RetireTask was invoked (delivery completed or was already done).
	TeardownResult *TeardownResult

	// TeardownError is non-nil when the retire/teardown phase failed.
	TeardownError error
}

// IsError returns true when the composite operation has not fully succeeded.
// A merged-but-not-retired result is non-zero: retry resumes retirement only.
func (r *MergeAndRetireResult) IsError() bool {
	if r == nil {
		return true
	}
	if r.MergeOutcome != taskauthority.DeliveryOutcomeCompleted {
		return true
	}
	return r.TeardownError != nil
}

// MergeAndRetire composes the journaled delivery execution and retirement
// into one operation.
//
// Phase 1 - Delivery: merged truth is derived from the canonical committed
// delivery outcome, never from the .meta projection. A
// committed completed outcome skips delivery entirely (idempotent resume); a
// committed retryable outcome or no committed outcome runs the journaled
// Deliver; a committed terminal partial/remote-unknown outcome cannot be
// re-delivered and fails closed.
//
// Phase 2 - Retirement: the authoritative retired phase transition commits via
// the composed canonical Task Authority FIRST (durable receipt); RetireTask
// then performs the saga-side cleanup strictly after the receipt. When the
// delivery was already completed (resume after partial cleanup), Force=true
// skips worktree-based safety checks — the canonical completed outcome is
// sufficient proof. When delivery was performed by this call, Force=false so
// the normal safety checks still run.
//
// req is the delivery intent pr-merge built, Human's words included; it is the
// one source of the delivery identity, never the .meta projection.
//
// authority is the composed canonical Task Authority targeting the exact
// resolved task home (cross-home delivery); it is threaded into RetireTask
// and the canonical outcome reads (nil fails closed).
func MergeAndRetire(homeDir, id string, req DeliverRequest, backend BoundTeardown, journals RetirementJournalPort, authority *taskauthority.Canonical) *MergeAndRetireResult {
	if authority == nil {
		return &MergeAndRetireResult{
			MergeOutcome: taskauthority.DeliveryOutcomeRetryable,
			MergeDetail:  "merge-and-retire requires a composed task authority",
		}
	}
	taskID, err := domain.NewTaskID(id)
	if err != nil {
		return &MergeAndRetireResult{
			MergeOutcome: taskauthority.DeliveryOutcomeRetryable,
			MergeDetail:  fmt.Sprintf("resolving task identity: %v", err),
		}
	}

	// Capture the retirement target at invocation start: this
	// merge-and-retire invocation is a distinct explicit teardown request
	// bound to the generation it observes now. If the task reopens to a
	// newer generation after this capture, the delayed RetireTask fails
	// closed with a typed conflict instead of implicitly retiring the newer
	// generation.
	agg, err := authority.Get(taskID)
	if err != nil {
		return &MergeAndRetireResult{
			MergeOutcome: taskauthority.DeliveryOutcomeRetryable,
			MergeDetail:  fmt.Sprintf("resolving target generation: %v", err),
		}
	}
	targetGen := &agg.Generation

	// Phase 1: canonical committed delivery outcome (no .meta truth).
	alreadyMerged := false
	out, err := authority.DeliveryOutcome(taskID)
	switch {
	case err == nil:
		switch out.Status {
		case taskauthority.DeliveryOutcomeCompleted:
			alreadyMerged = true
		case taskauthority.DeliveryOutcomeRetryable:
			alreadyMerged = false
		default:
			// partial / remote-unknown are terminal: the same delivery
			// cannot be re-attempted.
			return &MergeAndRetireResult{
				MergeOutcome: out.Status,
				MergeDetail:  fmt.Sprintf("task %s committed terminal delivery outcome %q (%s); a new delivery conflicts and retirement is refused", id, out.Status, out.Detail),
			}
		}
	case errors.Is(err, taskauthority.ErrNotFound):
		alreadyMerged = false
	default:
		return &MergeAndRetireResult{
			MergeOutcome: taskauthority.DeliveryOutcomeRetryable,
			MergeDetail:  fmt.Sprintf("resolving canonical delivery outcome: %v", err),
		}
	}

	mergeOutcome := taskauthority.DeliveryOutcomeCompleted
	if !alreadyMerged {
		result, derr := Deliver(homeDir, id, req)
		if derr != nil {
			return &MergeAndRetireResult{
				MergeOutcome: taskauthority.DeliveryOutcomeRetryable,
				MergeDetail:  fmt.Sprintf("delivery execution failed: %v", derr),
			}
		}
		mergeOutcome = result.Status
		if mergeOutcome != taskauthority.DeliveryOutcomeCompleted {
			return &MergeAndRetireResult{
				MergeOutcome: mergeOutcome,
				MergeDetail:  result.Detail,
			}
		}
	}

	// Phase 2: RetireTask. When the delivery was already done (resume after
	// partial cleanup), Force=true skips worktree-based safety checks — the
	// canonical completed outcome is sufficient proof that work is landed.
	retireOpts := Options{
		HomeDir:            homeDir,
		ID:                 id,
		Force:              alreadyMerged,
		ExpectedGeneration: targetGen,
	}
	teardownResult, retireErr := RetireTask(retireOpts, backend, journals, authority)

	return &MergeAndRetireResult{
		MergeOutcome:   mergeOutcome,
		MergeDetail:    "provider confirms merged",
		TeardownResult: teardownResult,
		TeardownError:  retireErr,
	}
}
