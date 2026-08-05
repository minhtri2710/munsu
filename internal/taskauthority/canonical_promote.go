package taskauthority

import (
	"encoding/json"

	"github.com/minhtri2710/munsu/internal/domain"
)

// CanonicalPromoteRequest is the typed intent of the scout → ship kind
// promotion. It pins the exact generation/revision precondition, the expected
// current kind (scout), the target kind (ship), and the reason. Kind is an
// authoritative TaskDefinition field, so the flip is a named generation-bound
// operation — never a generic kind-mutation API.
type CanonicalPromoteRequest struct {
	HomeID       domain.HomeID
	TaskID       domain.TaskID
	Precondition domain.Precondition
	CurrentKind  string
	TargetKind   string
	Reason       string
}

func (r CanonicalPromoteRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID      string `json:"home_id"`
		TaskID      string `json:"task_id"`
		Generation  uint64 `json:"generation"`
		Revision    uint64 `json:"revision"`
		CurrentKind string `json:"current_kind"`
		TargetKind  string `json:"target_kind"`
		Reason      string `json:"reason"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.CurrentKind, r.TargetKind, r.Reason})
}

// Promote is the canonical operation that promotes a terminal scout Task
// Generation to ship kind. Kind is an authoritative TaskDefinition field, so
// the change commits inside the Aggregate with the exact generation/revision
// precondition revalidated in one atomic journaled transaction, exactly one
// Revision advance, and the durable idempotency receipt. Promotion requires
// the current generation, phase done or resolved (a live or retired task
// never promotes), and current kind scout; other kinds, live phases, stale
// preconditions, and active transfer reservations fail closed. Repeating the
// same Operation ID with the same digest replays the durable prior outcome;
// reusing the Operation ID with a different intent conflicts.
func (c *Canonical) Promote(op domain.Operation, req CanonicalPromoteRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if req.CurrentKind != "scout" || req.TargetKind != "ship" {
		return Outcome{}, validationError("promotion requires scout -> ship kind promotion")
	}
	return c.mutateTask(op, req.TaskID, req.Precondition, func(cur Aggregate) (Aggregate, error) {
		if cur.Definition.Kind != req.CurrentKind {
			return Aggregate{}, preconditionError("task %s generation %s has kind %q, only scout tasks promote to ship", cur.TaskID, cur.Generation, cur.Definition.Kind)
		}
		if cur.Phase != PhaseDone && cur.Phase != PhaseResolved {
			return Aggregate{}, preconditionError("task %s generation %s is %s, promotion requires a done or resolved scout", cur.TaskID, cur.Generation, cur.Phase)
		}
		next := cur.clone()
		next.Definition.Kind = req.TargetKind
		next.Revision++
		return next, nil
	})
}
