package fleet

import (
	"errors"
	"fmt"

	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/taskauthorityfs"
)

// This file owns the canonical-read preference for snapshot and observation
// (Task 7.8, ADR-0007 §7): authoritative Task Authority records are the
// preferred source for kind, project, and lifecycle phase; the .meta and
// .status projections are display fallback only and can never override a
// newer authoritative lifecycle transition. A home with legacy v1 task
// authority state fails closed (migration is explicit and never automatic);
// a home with no authority state at all serves the empty view without
// creating anything.

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

// canonicalAggregates loads the current authoritative aggregates for one
// home, keyed by task ID. A home with no authority state returns an empty
// map; a home with legacy v1 records or corrupt/recovery-required state
// fails closed.
func canonicalAggregates(homeDir string) (map[string]taskauthority.Aggregate, error) {
	store, err := taskauthorityfs.NewStore(homeDir)
	if err != nil {
		return nil, err
	}
	view, err := store.View()
	if err != nil {
		return nil, err
	}
	out := make(map[string]taskauthority.Aggregate, len(view.Aggregates))
	for _, agg := range view.Aggregates {
		if agg.Current {
			out[agg.TaskID] = agg
		}
	}
	return out, nil
}

// currentCanonical reads one task's current authoritative aggregate. A
// missing record is ErrNotFound (the caller falls back to the projection and
// display tiers); v1 migration, corruption, and recovery-required homes fail
// closed.
func currentCanonical(homeDir, id string) (taskauthority.Aggregate, error) {
	store, err := taskauthorityfs.NewStore(homeDir)
	if err != nil {
		return taskauthority.Aggregate{}, err
	}
	return taskauthority.New(store).Get(id)
}

// currentCanonicalAggregate reads one task's current authoritative aggregate,
// reporting whether the task has a canonical record. A missing record is not
// an error (the caller falls back to the projection and display tiers); v1
// migration, corruption, and recovery-required homes fail closed.
func currentCanonicalAggregate(homeDir, id string) (taskauthority.Aggregate, bool, error) {
	agg, err := currentCanonical(homeDir, id)
	if err != nil {
		if errors.Is(err, taskauthority.ErrNotFound) {
			return taskauthority.Aggregate{}, false, nil
		}
		return taskauthority.Aggregate{}, false, err
	}
	return agg, true, nil
}
