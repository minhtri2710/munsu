package fleet

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// MergeAndRetireResult captures the full outcome of a composite merge-and-retire
// operation. Both the merge delivery and the retirement are independently durable
// and idempotent transactions. When the merge succeeded but retirement did not
// complete, the result is non-zero and retry resumes retirement only.
type MergeAndRetireResult struct {
	// MergeOutcome is the outcome of the merge delivery phase.
	MergeOutcome MergeOutcome

	// MergeDetail is a human-readable detail about the merge outcome.
	MergeDetail string

	// TeardownResult is the result of the retire/teardown phase, populated
	// when RetireTask was invoked (merge succeeded or was already done).
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
	// Merge failed or is in a partial state
	switch r.MergeOutcome {
	case MergeOutcomeFailed, MergeOutcomeOpen, MergeOutcomeRemoteUnknown:
		return true
	}
	// Merge succeeded but teardown failed
	return r.TeardownError != nil
}

// MergeAndRetire composes the merge delivery and retirement into one operation.
//
// Phase 1 - Merge delivery: if the task is already in the merged state, the
// merge phase is skipped entirely (idempotent resume). Otherwise, PRMerge is
// called to perform the full merge and reconciliation.
//
// Phase 2 - Retirement: the authoritative retired phase transition commits via
// the composed Task Authority FIRST (durable receipt); RetireTask then performs
// the saga-side cleanup (meta/state removal, journal finalization) strictly
// after the receipt. RetireTask is called with Force=true when the merge was
// already done (retry after partial cleanup), so that worktree-based safety
// checks are skipped — the merged state is sufficient proof. When the merge was
// performed by this call, Force=false so the normal safety checks still run.
// A cleanup failure returns a typed partial result (retired committed, cleanup
// pending): retry resumes retirement only and never reruns merge/reconciliation.
//
// Returns a typed composite result. Callers check IsError() to determine the
// overall outcome. A merged-but-not-retired result is non-zero; retry resumes
// retirement only.
// authority is the composed canonical Task Authority targeting the exact
// resolved task home (cross-home delivery); it is threaded into PRMerge for
// the post-merge issue link reconciliation, unused when the merge phase is
// skipped, and required by the retirement transition (nil fails closed).
func MergeAndRetire(homeDir, id, prURL string, extraArgs []string, backend BoundTeardown, journals RetirementJournalPort, authority *taskauthority.Canonical) *MergeAndRetireResult {
	// Phase 1: Check if already merged (idempotent resume).
	meta, err := home.ReadMeta(homeDir, id)
	if err != nil {
		return &MergeAndRetireResult{
			MergeOutcome: MergeOutcomeFailed,
			MergeDetail:  fmt.Sprintf("reading meta: %v", err),
		}
	}

	alreadyMerged := meta[MetaDeliveryState] == string(DeliveryStateMerged)
	mergeOutcome := MergeOutcomeAlreadyMerged

	if !alreadyMerged {
		// Run the full merge delivery.
		if err := PRMerge(homeDir, id, prURL, extraArgs, authority); err != nil {
			return &MergeAndRetireResult{
				MergeOutcome: MergeOutcomeFailed,
				MergeDetail:  err.Error(),
			}
		}
		mergeOutcome = MergeOutcomeMerged
	}

	// Phase 2: Run RetireTask.
	// When the merge was already done (resume after partial cleanup), use
	// Force=true to skip worktree-based safety checks — the merged delivery
	// state is sufficient proof that work is landed. When the merge was
	// performed by this call, use Force=false for normal safety checks.
	retireOpts := Options{
		HomeDir: homeDir,
		ID:      id,
		Force:   alreadyMerged,
	}
	teardownResult, retireErr := RetireTask(retireOpts, backend, journals, authority)

	return &MergeAndRetireResult{
		MergeOutcome:   mergeOutcome,
		TeardownResult: teardownResult,
		TeardownError:  retireErr,
	}
}
