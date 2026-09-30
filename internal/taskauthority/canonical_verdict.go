package taskauthority

import (
	"encoding/json"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
)

// ReviewVerdictRecord is the durable review verdict of the task's current
// head (ADR-0025). It is one record, not a history: a later verdict replaces
// it, so a verdict can only ever speak for the head it names, and a repair
// head has none until it is reviewed on its own. Delivery authorization
// embeds the verdict it relied on as immutable evidence.
type ReviewVerdictRecord struct {
	OperationID string               `json:"operation_id"`
	Verdict     domain.ReviewVerdict `json:"verdict"`
	RecordedAt  int64                `json:"recorded_at"`
}

// validateReviewVerdictRecord checks the persisted record shape: the recording
// Operation ID, a valid verdict and a recording timestamp.
func validateReviewVerdictRecord(r ReviewVerdictRecord) error {
	if r.OperationID == "" || strings.ContainsAny(r.OperationID, `/\\`) {
		return validationError("review verdict record missing operation id")
	}
	if err := r.Verdict.Validate(); err != nil {
		return validationError("%v", err)
	}
	if r.RecordedAt <= 0 {
		return validationError("review verdict record missing recorded timestamp")
	}
	return nil
}

// CanonicalRecordReviewVerdictRequest is the typed intent for recording one
// review verdict on the task's current head.
type CanonicalRecordReviewVerdictRequest struct {
	HomeID       domain.HomeID
	TaskID       domain.TaskID
	Precondition domain.Precondition
	Verdict      domain.ReviewVerdict
}

func (r CanonicalRecordReviewVerdictRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID     string               `json:"home_id"`
		TaskID     string               `json:"task_id"`
		Generation uint64               `json:"generation"`
		Revision   uint64               `json:"revision"`
		Verdict    domain.ReviewVerdict `json:"verdict"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.Verdict})
}

// RecordReviewVerdict records the review verdict of the working task's head
// as the reviewer observed it. It fails closed unless the verdict is valid (a non-empty
// review range, a reviewer that is not the author, a frozen tree), names the
// bound endpoint's incarnation as the authoring soldier instance, and comes
// from the current generation of the review task that reads this task at the
// verdict head (checkVerdictReviewer). The head itself is observed by Fleet at
// record time, not compared with a stored head. Same Operation ID with the same digest
// replays; the record replaces any earlier verdict.
func (c *Canonical) RecordReviewVerdict(op domain.Operation, req CanonicalRecordReviewVerdictRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := req.TaskID.Validate(); err != nil {
		return Outcome{}, err
	}
	if err := req.Precondition.Validate(); err != nil {
		return Outcome{}, err
	}
	if err := req.Verdict.Validate(); err != nil {
		return Outcome{}, validationError("%v", err)
	}
	return c.mutateTask(op, req.TaskID, req.Precondition, func(cur Aggregate) (Aggregate, error) {
		if cur.Phase != PhaseWorking {
			return Aggregate{}, preconditionError("review verdict requires a working task; task %s is %s", cur.TaskID, cur.Phase)
		}
		if cur.Worktree == nil || cur.Endpoint == nil {
			return Aggregate{}, preconditionError("review verdict requires the bound worktree and endpoint of task %s", cur.TaskID)
		}
		if err := c.checkVerdictReviewer(cur, req.Verdict); err != nil {
			return Aggregate{}, err
		}
		if cur.Endpoint.Incarnation != req.Verdict.Author {
			return Aggregate{}, preconditionError("review verdict author %q is not the authoring soldier instance %q of task %s", req.Verdict.Author, cur.Endpoint.Incarnation, cur.TaskID)
		}
		next := cur.clone()
		next.ReviewVerdict = &ReviewVerdictRecord{
			OperationID: op.ID.Value(),
			Verdict:     req.Verdict,
			RecordedAt:  c.now().UnixNano(),
		}
		next.Revision++
		return next, nil
	})
}

// checkVerdictReviewer requires the verdict's reviewer to be the current
// generation of a review task that reads exactly this task at exactly the
// verdict head, running as the recorded reviewer instance, with a Before tree
// recorded at its launch that the verdict carries. It reads the review task
// without its lock, as checkReviewTarget does; a respawn between this read and
// the write is a recorded residual (ADR-0025). The checks are consistency
// checks over documents a soldier can also write (F1), not authentication.
func (c *Canonical) checkVerdictReviewer(cur Aggregate, v domain.ReviewVerdict) error {
	id, err := domain.NewTaskID(v.ReviewerTask)
	if err != nil {
		return validationError("review verdict reviewer task: %v", err)
	}
	rev, err := c.Get(id)
	if err != nil {
		return err
	}
	switch {
	case rev.Definition.Kind != KindReview:
		return preconditionError("review verdict reviewer %s is a %s task, not a review task", rev.TaskID, rev.Definition.Kind)
	case rev.Definition.ReviewTaskID != cur.TaskID:
		return preconditionError("review verdict reviewer %s reviews %q, not task %s", rev.TaskID, rev.Definition.ReviewTaskID, cur.TaskID)
	case uint64(rev.Generation) != v.ReviewerGeneration:
		return preconditionError("review verdict reviewer generation %d is not the current generation %s of %s", v.ReviewerGeneration, rev.Generation, rev.TaskID)
	case rev.Definition.ReviewHead != v.HeadSHA:
		return preconditionError("review verdict head %q is not the head %q reviewer %s was asked to review", v.HeadSHA, rev.Definition.ReviewHead, rev.TaskID)
	case rev.Endpoint == nil || rev.Endpoint.Incarnation != v.ReviewerIncarnation:
		return preconditionError("review verdict reviewer instance %q is not the bound endpoint instance of %s", v.ReviewerIncarnation, rev.TaskID)
	case rev.LaunchEvidence == nil || rev.LaunchEvidence.ReviewTree == nil:
		return preconditionError("reviewer %s generation %s recorded no tree before its review", rev.TaskID, rev.Generation)
	case *rev.LaunchEvidence.ReviewTree != v.Before:
		return preconditionError("review verdict before tree does not match the tree recorded at the launch of reviewer %s", rev.TaskID)
	}
	return nil
}
