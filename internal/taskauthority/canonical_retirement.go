package taskauthority

import (
	"encoding/json"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
)

// RetirementBoundary is the Task Authority surface that makes the retired
// lifecycle consequence reachable for a Fleet-owned Soldier retirement
// (#412). It is a single typed, exact-generation operation: Retire transitions
// the current generation to PhaseRetired under a truthful generation/revision
// precondition, with a durable idempotency receipt and replay/conflict
// semantics.
//
// Retire separates the active binding owner from the immutable retirement
// evidence: the retired generation no longer acts as an active endpoint or
// worktree owner (its active Endpoint/Worktree are nil), while the exact
// ownership evidence — lease IDs, fence tokens, handles/paths, repository
// identity, the task generation, and the retirement Operation ID — is durably
// preserved on the Task generation for #412 to release only resources still
// owned by that generation, and for diagnostics. Retire never launches/stops
// processes or releases Backend resources (that is #412's orchestration).

// CanonicalRetireRequest retires the current generation of a task. The
// request is the typed intent for the operation digest.
type CanonicalRetireRequest struct {
	HomeID       domain.HomeID
	TaskID       domain.TaskID
	Precondition domain.Precondition
	Reason       string
}

func (r CanonicalRetireRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID     string `json:"home_id"`
		TaskID     string `json:"task_id"`
		Generation uint64 `json:"generation"`
		Revision   uint64 `json:"revision"`
		Reason     string `json:"reason,omitempty"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.Reason})
}

// Retire transitions the current task generation into the retired terminal
// phase. It is exact-generation and idempotent: the request carries the
// expected Generation/Revision precondition, and reusing the same Operation
// ID with the same digest replays the durable prior outcome. The retired
// generation releases its ACTIVE endpoint/worktree ownership (Endpoint and
// Worktree become nil — the generation is no longer an active binding owner)
// while preserving the exact ownership evidence durably in Retirement for
// #412's resource release and diagnostics. A stale precondition, a generation
// that is not current, an already-retired generation, or a reserved-for-
// transfer generation fails closed with a typed conflict.
func (c *Canonical) Retire(op domain.Operation, req CanonicalRetireRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	return c.mutateTask(op, req.TaskID, req.Precondition, func(cur Aggregate) (Aggregate, error) {
		if cur.Phase == PhaseRetired {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s is already retired", cur.TaskID, cur.Generation)
		}
		next := cur.clone()
		next.Phase = PhaseRetired
		next.PhaseDetail = strings.TrimSpace(req.Reason)
		// Preserve the exact ownership evidence immutably; then clear the
		// active binding owner so no active binding query reports an owned
		// resource on a retired generation. Resource release itself is #412.
		if cur.Endpoint != nil || cur.Worktree != nil {
			next.Retirement = &RetirementEvidence{
				OperationID: op.ID.Value(),
				Generation:  cur.Generation,
				RetiredAt:   c.now().UnixNano(),
				Endpoint:    cur.Endpoint,
				Worktree:    cur.Worktree,
			}
		}
		next.Endpoint = nil
		next.Worktree = nil
		// A durable cleanup claim is committed atomically WITH the retirement
		// (BEO-16/P1a): the generation is pinned against Reopen/BindEndpoint/
		// acquisition from the instant the retire commits, so no window exists
		// between the retirement transition and the fleet cleanup claim. The
		// claim is reconciled by CompleteCleanup/AbortCleanup.
		next.CleanupClaim = &CleanupClaim{
			OperationID: op.ID.Value(),
			Generation:  cur.Generation,
			Status:      CleanupActive,
			ClaimedAt:   c.now().UnixNano(),
		}
		next.Revision++
		return next, nil
	})
}
