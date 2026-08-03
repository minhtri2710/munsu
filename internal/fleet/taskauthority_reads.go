package fleet

import (
	"errors"
	"fmt"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	tauth "github.com/minhtri2710/munsu/internal/taskauthority"
)

// This file owns the canonical-read preference for snapshot and observation
// (Task 7.8, ADR-0007 §7): authoritative Task Authority records are the
// preferred source for kind, project, and lifecycle phase; the .meta and
// .status projections are display fallback only and can never override a
// newer authoritative lifecycle transition. A home that is not a canonical
// v1 home fails closed (migration is explicit and never automatic); a
// canonical home with no authority state at all serves the empty view
// without creating anything.

// LegacyDeliveryEvidenceError is the typed fail-closed outcome of a snapshot
// or observation read over a task whose .meta carries delivery evidence
// claims without an authoritative Task Authority record (Task 7.8 decision
// (a)): the canonical read refuses to silently project the legacy shape. The
// heal path is a mutation that commits authoritative evidence (merge,
// reconcile, or poll) or projection reconciliation (`munsu task reconcile`).
type LegacyDeliveryEvidenceError struct {
	TaskID string
	Field  string
}

func (e *LegacyDeliveryEvidenceError) Error() string {
	return fmt.Sprintf("task %s carries legacy delivery evidence %q without an authoritative Task Authority record; commit authoritative evidence (merge/reconcile/poll) or run 'munsu task reconcile'", e.TaskID, e.Field)
}

// legacyDeliveryClaim returns the meta key of a delivery evidence claim that
// has no meaning without an authoritative record: a meta-only
// delivery_state=merged (no authoritative merge attempt) and the legacy
// merge_authorization JSON (old shape incl. task_generation and RFC3339
// authorized_at — no longer consumed by the canonical read path since Task
// 7.4). Both are fail-closed rather than silently projected.
func legacyDeliveryClaim(meta map[string]string) string {
	switch {
	case meta[MetaDeliveryState] == string(DeliveryStateMerged):
		return MetaDeliveryState + "=merged"
	case meta[MetaMergeAuthorization] != "":
		return MetaMergeAuthorization
	default:
		return ""
	}
}

// canonicalAuthority opens the canonical home and constructs the canonical
// Task Authority over it. A directory that is not an initialized canonical
// v1 home (no verified identity/layout) fails closed; a canonical home with
// no task-authority state serves an empty view.
func canonicalAuthority(homeDir string) (*tauth.Canonical, error) {
	h, err := home.Open(homeDir)
	if err != nil {
		return nil, err
	}
	return tauth.NewCanonical(h)
}

// canonicalAggregates loads the current authoritative aggregates for one
// home, keyed by task ID. A canonical home with no authority state returns an
// empty map; a non-canonical home or corrupt/recovery-required state fails
// closed.
func canonicalAggregates(homeDir string) (map[string]tauth.Aggregate, error) {
	c, err := canonicalAuthority(homeDir)
	if err != nil {
		return nil, err
	}
	aggs, err := c.List()
	if err != nil {
		return nil, err
	}
	out := make(map[string]tauth.Aggregate, len(aggs))
	for _, agg := range aggs {
		out[agg.TaskID] = agg
	}
	return out, nil
}

// currentCanonical reads one task's current authoritative aggregate. A
// missing record is ErrNotFound (the caller falls back to the projection and
// display tiers); a non-canonical home and corrupt/recovery-required homes
// fail closed.
func currentCanonical(homeDir, id string) (tauth.Aggregate, error) {
	taskID, err := domain.NewTaskID(id)
	if err != nil {
		return tauth.Aggregate{}, err
	}
	c, err := canonicalAuthority(homeDir)
	if err != nil {
		return tauth.Aggregate{}, err
	}
	return c.Get(taskID)
}

// currentCanonicalAggregate reads one task's current authoritative aggregate,
// reporting whether the task has a canonical record. A missing record is not
// an error (the caller falls back to the projection and display tiers);
// non-canonical homes and corrupt state fail closed.
func currentCanonicalAggregate(homeDir, id string) (tauth.Aggregate, bool, error) {
	agg, err := currentCanonical(homeDir, id)
	if err != nil {
		if errors.Is(err, tauth.ErrNotFound) {
			return tauth.Aggregate{}, false, nil
		}
		return tauth.Aggregate{}, false, err
	}
	return agg, true, nil
}
