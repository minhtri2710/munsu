package taskauthority

import (
	"sort"
	"strings"
)

// CreateHoldRequest creates one durable dispatch hold.
type CreateHoldRequest struct {
	OperationID string
	Actor       Actor
	ID          string
	Scope       DispatchHoldScope
	Actions     []DispatchAction
	Reason      string
}

func (r CreateHoldRequest) digestPayload() any {
	scope := normalizeScope(r.Scope)
	return struct {
		ID      string
		Scope   DispatchHoldScope
		Actions []DispatchAction
		Reason  string
	}{r.ID, scope, uniqueActions(r.Actions), r.Reason}
}

// CreateHold is the named semantic operation that creates one durable
// dispatch hold. Repeating the same Operation ID replays; re-creating an
// identical active hold is a successful no-op; re-creating a different hold
// under the same ID conflicts.
func (a *Authority) CreateHold(req CreateHoldRequest) (HoldResult, error) {
	if req.ID == "" || strings.ContainsAny(req.ID, `/\\`) {
		return HoldResult{}, validationError("dispatch hold ID must be a safe non-empty value")
	}
	if len(req.Actions) == 0 || strings.TrimSpace(req.Reason) == "" {
		return HoldResult{}, validationError("dispatch hold requires actions and reason")
	}
	op, err := a.operation(req.OperationID, req.Actor, req.digestPayload())
	if err != nil {
		return HoldResult{}, err
	}
	replayed, err := a.store.Update(op, func(tx *Tx) error {
		existing, ok := tx.Hold(req.ID)
		if !ok {
			hold := DispatchHold{
				SchemaVersion: TaskAuthoritySchema,
				ID:            req.ID,
				Scope:         normalizeScope(req.Scope),
				Actions:       uniqueActions(req.Actions),
				Reason:        req.Reason,
				CreatedAt:     a.now().UnixNano(),
			}
			if err := tx.PutHold(hold); err != nil {
				return err
			}
			return tx.AppendAudit(a.dispatchAudit(op, "hold created: "+req.ID))
		}
		if existing.ReleasedAt != 0 {
			return conflictError(ErrConflict, "hold %s is already released", req.ID)
		}
		if !holdEquivalent(existing, req) {
			return conflictError(ErrConflict, "hold %s already exists with a different definition", req.ID)
		}
		return nil
	})
	if err != nil {
		return HoldResult{}, err
	}
	return HoldResult{HoldID: req.ID, Replayed: replayed.Replayed}, nil
}

// ReleaseHoldRequest releases one durable dispatch hold.
type ReleaseHoldRequest struct {
	OperationID string
	Actor       Actor
	ID          string
	Reason      string
}

func (r ReleaseHoldRequest) digestPayload() any {
	return struct {
		ID     string
		Reason string
	}{r.ID, r.Reason}
}

// ReleaseHold is the named semantic operation that releases one dispatch
// hold. Releasing an already-released hold is a successful no-op.
func (a *Authority) ReleaseHold(req ReleaseHoldRequest) (HoldResult, error) {
	if req.ID == "" {
		return HoldResult{}, validationError("dispatch hold ID must not be empty")
	}
	op, err := a.operation(req.OperationID, req.Actor, req.digestPayload())
	if err != nil {
		return HoldResult{}, err
	}
	replayed, err := a.store.Update(op, func(tx *Tx) error {
		hold, ok := tx.Hold(req.ID)
		if !ok {
			return conflictError(ErrHoldNotFound, "dispatch hold %s not found", req.ID)
		}
		if hold.ReleasedAt != 0 {
			return nil
		}
		updated := hold.clone()
		updated.ReleasedAt = a.now().UnixNano()
		if err := tx.PutHold(updated); err != nil {
			return err
		}
		return tx.AppendAudit(a.dispatchAudit(op, "hold released: "+req.ID))
	})
	if err != nil {
		return HoldResult{}, err
	}
	return HoldResult{HoldID: req.ID, Replayed: replayed.Replayed}, nil
}

// HoldResult is the outcome of a dispatch-control operation.
type HoldResult struct {
	HoldID   string
	Replayed bool
}

func normalizeScope(scope DispatchHoldScope) DispatchHoldScope {
	return DispatchHoldScope{
		ProjectIDs:  uniqueSortedStrings(scope.ProjectIDs),
		TaskIDs:     uniqueSortedStrings(scope.TaskIDs),
		Generations: uniqueSortedStrings(scope.Generations),
		ParentIDs:   uniqueSortedStrings(scope.ParentIDs),
	}
}

func uniqueActions(actions []DispatchAction) []DispatchAction {
	out := append([]DispatchAction(nil), actions...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	result := out[:0]
	for _, action := range out {
		if len(result) == 0 || result[len(result)-1] != action {
			result = append(result, action)
		}
	}
	return result
}

func uniqueSortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	result := out[:0]
	for _, value := range out {
		if value != "" && (len(result) == 0 || result[len(result)-1] != value) {
			result = append(result, value)
		}
	}
	return result
}

// holdEquivalent reports whether an existing active hold matches the request.
func holdEquivalent(hold DispatchHold, req CreateHoldRequest) bool {
	if hold.Reason != req.Reason {
		return false
	}
	if len(hold.Actions) != len(uniqueActions(req.Actions)) {
		return false
	}
	for i := range hold.Actions {
		if hold.Actions[i] != uniqueActions(req.Actions)[i] {
			return false
		}
	}
	return scopesEqual(hold.Scope, normalizeScope(req.Scope))
}

func scopesEqual(a, b DispatchHoldScope) bool {
	return equalStrings(a.ProjectIDs, b.ProjectIDs) &&
		equalStrings(a.TaskIDs, b.TaskIDs) &&
		equalStrings(a.Generations, b.Generations) &&
		equalStrings(a.ParentIDs, b.ParentIDs)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
