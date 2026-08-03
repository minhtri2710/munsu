// Package storecontract owns the one authoritative Store contract suite
// shared by every taskauthority.Store adapter. The in-memory adapter tests
// and the filesystem adapter tests run this identical suite so the two
// implementations cannot drift apart in receipts, Revision behavior,
// idempotency, rollback/no-partial-apply, conflict typing, dispatch records,
// audit, or reopen semantics.
//
// The suite lives in non-test files because Go test files are compiled only
// into their own package's test binary: an importable harness is required for
// the filesystem adapter (a different package) to run the identical contract.
package storecontract

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// Run executes the shared adapter contract assertions. Every Store
// implementation (in-memory, filesystem) must pass this suite so the two
// adapters have identical receipts, Revision behavior, idempotency, and
// rollback semantics. newStore must return a fresh empty Store; the suite
// treats each call as an independent store.
func Run(t *testing.T, newStore func() taskauthority.Store) {
	t.Run("empty view", func(t *testing.T) {
		s := newStore()
		v, err := s.View()
		if err != nil {
			t.Fatal(err)
		}
		if len(v.Aggregates) != 0 || len(v.Holds) != 0 || len(v.Interpretations) != 0 ||
			len(v.Decisions) != 0 || len(v.Receipts) != 0 || len(v.Audit) != 0 {
			t.Fatalf("expected empty view, got %+v", v)
		}
	})

	t.Run("rollback on callback error", func(t *testing.T) {
		s := newStore()
		_, err := s.Update(op("op-rollback", "t1"), func(tx *taskauthority.Tx) error {
			if err := tx.PutAggregate(mustAggregate(t, "t1", 1, "queued")); err != nil {
				return err
			}
			if err := tx.PutHold(mustHold(t, "hold-1", "start", "t1", "test hold")); err != nil {
				return err
			}
			if err := tx.AppendAudit(mustAudit(t, "op-rollback", "t1", 1, "queued", "queued")); err != nil {
				return err
			}
			return errors.New("boom")
		})
		if err == nil {
			t.Fatal("expected callback error")
		}
		v, _ := s.View()
		if len(v.Aggregates) != 0 || len(v.Holds) != 0 || len(v.Interpretations) != 0 ||
			len(v.Decisions) != 0 || len(v.Receipts) != 0 || len(v.Audit) != 0 {
			t.Fatalf("callback error must not mutate state: %+v", v)
		}
	})

	t.Run("staged records persist with revision", func(t *testing.T) {
		s := newStore()
		_, err := s.Update(op("op-create", "t1"), func(tx *taskauthority.Tx) error {
			agg := mustAggregate(t, "t1", 1, "queued")
			agg.Revision = 1
			if err := tx.PutAggregate(agg); err != nil {
				return err
			}
			return tx.AppendAudit(mustAudit(t, "op-create", "t1", 1, "queued", "queued"))
		})
		if err != nil {
			t.Fatal(err)
		}
		v, _ := s.View()
		agg, ok := v.Current("t1")
		if !ok {
			t.Fatal("current aggregate missing after commit")
		}
		if agg.Revision != 1 || agg.Phase != taskauthority.PhaseQueued {
			t.Fatalf("aggregate = %+v", agg)
		}
		if len(v.Audit) != 1 {
			t.Fatalf("audit = %d events, want 1", len(v.Audit))
		}
		if len(v.Receipts) != 1 {
			t.Fatalf("receipts = %d, want 1", len(v.Receipts))
		}
		receipt := v.Receipts[0]
		if receipt.TaskID != "t1" || receipt.Generation != 1 || receipt.Revision != 1 || receipt.Phase != taskauthority.PhaseQueued {
			t.Fatalf("receipt outcome = %+v", receipt)
		}
	})

	t.Run("revision advances exactly once", func(t *testing.T) {
		s := newStore()
		if _, err := s.Update(op("op-1", "t1"), func(tx *taskauthority.Tx) error {
			return tx.PutAggregate(mustAggregate(t, "t1", 1, "queued"))
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Update(op("op-2", "t1"), func(tx *taskauthority.Tx) error {
			agg := mustAggregate(t, "t1", 1, "working")
			agg.Revision = 2
			return tx.PutAggregate(agg)
		}); err != nil {
			t.Fatal(err)
		}
		v, _ := s.View()
		agg, _ := v.Current("t1")
		if agg.Revision != 2 {
			t.Fatalf("revision = %d, want 2", agg.Revision)
		}
	})

	t.Run("revision cannot stay unchanged or skip", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			revision taskauthority.Revision
		}{{"unchanged", 1}, {"skipped", 3}} {
			t.Run(tc.name, func(t *testing.T) {
				s := newStore()
				if _, err := s.Update(op("op-1", "t1"), func(tx *taskauthority.Tx) error {
					return tx.PutAggregate(mustAggregate(t, "t1", 1, "queued"))
				}); err != nil {
					t.Fatal(err)
				}
				_, err := s.Update(op("op-2", "t1"), func(tx *taskauthority.Tx) error {
					agg := mustAggregate(t, "t1", 1, "working")
					agg.Revision = tc.revision
					return tx.PutAggregate(agg)
				})
				if !errors.Is(err, taskauthority.ErrConflict) {
					t.Fatalf("revision %d update = %v, want ErrConflict", tc.revision, err)
				}
			})
		}
	})

	t.Run("duplicate operation id same digest replays", func(t *testing.T) {
		s := newStore()
		runs := 0
		first, err := s.Update(op("op-replay", "t1"), func(tx *taskauthority.Tx) error {
			runs++
			return tx.PutAggregate(mustAggregate(t, "t1", 1, "queued"))
		})
		if err != nil {
			t.Fatal(err)
		}
		second, err := s.Update(op("op-replay", "t1"), func(tx *taskauthority.Tx) error {
			runs++
			return tx.PutAggregate(mustAggregate(t, "t1", 1, "working"))
		})
		if err != nil {
			t.Fatal(err)
		}
		if runs != 1 {
			t.Fatalf("callback ran %d times, want 1 on replay", runs)
		}
		if second.OperationID != first.OperationID || second.Digest != first.Digest || second.CommittedAt != first.CommittedAt {
			t.Fatalf("replay receipt identity differs: %+v vs %+v", first, second)
		}
		if first.Replayed || !second.Replayed {
			t.Fatalf("replayed flags = %v/%v, want false/true", first.Replayed, second.Replayed)
		}
	})

	t.Run("duplicate operation id different digest conflicts", func(t *testing.T) {
		s := newStore()
		if _, err := s.Update(op("op-conflict", "t1"), func(tx *taskauthority.Tx) error {
			return tx.PutAggregate(mustAggregate(t, "t1", 1, "queued"))
		}); err != nil {
			t.Fatal(err)
		}
		other := op("op-conflict", "t1")
		other.Digest = digestOf("different-intent")
		_, err := s.Update(other, func(tx *taskauthority.Tx) error { return nil })
		if !errors.Is(err, taskauthority.ErrOperationConflict) {
			t.Fatalf("error = %v, want ErrOperationConflict", err)
		}
	})

	t.Run("generation replacement", func(t *testing.T) {
		s := newStore()
		gen1 := mustAggregate(t, "t1", 1, "done")
		gen1.Current = true
		if _, err := s.Update(op("op-gen1", "t1"), func(tx *taskauthority.Tx) error {
			return tx.PutAggregate(gen1)
		}); err != nil {
			t.Fatal(err)
		}
		old := mustAggregate(t, "t1", 1, "done")
		old.Current = false
		old.Revision = 2
		newGen := mustAggregate(t, "t1", 2, "queued")
		newGen.Current = true
		receipt, err := s.Update(op("op-reopen", "t1"), func(tx *taskauthority.Tx) error {
			if err := tx.PutAggregate(old); err != nil {
				return err
			}
			return tx.PutAggregate(newGen)
		})
		if err != nil {
			t.Fatal(err)
		}
		if receipt.TaskID != "t1" || receipt.Generation != 2 || receipt.Revision != taskauthority.FirstRevision || receipt.Phase != taskauthority.PhaseQueued || !receipt.Reopened {
			t.Fatalf("reopen receipt = %+v", receipt)
		}
		v, _ := s.View()
		cur, ok := v.Current("t1")
		if !ok || cur.Generation != 2 || cur.Revision != taskauthority.FirstRevision {
			t.Fatalf("current = %+v", cur)
		}
		historical, ok := v.Aggregate("t1", 1)
		if !ok || historical.Current {
			t.Fatalf("gen1 must be preserved as historical: %+v", historical)
		}
		if len(v.Aggregates) != 2 {
			t.Fatalf("aggregates = %d, want 2 (current + historical)", len(v.Aggregates))
		}
	})

	t.Run("reopened receipt replays", func(t *testing.T) {
		s := newStore()
		gen1 := mustAggregate(t, "t1", 1, "done")
		gen1.Current = true
		if _, err := s.Update(op("op-gen1", "t1"), func(tx *taskauthority.Tx) error {
			return tx.PutAggregate(gen1)
		}); err != nil {
			t.Fatal(err)
		}
		old := mustAggregate(t, "t1", 1, "done")
		old.Current = false
		old.Revision = 2
		newGen := mustAggregate(t, "t1", 2, "queued")
		newGen.Current = true
		reopenOp := op("op-reopen", "t1")
		receipt, err := s.Update(reopenOp, func(tx *taskauthority.Tx) error {
			if err := tx.PutAggregate(old); err != nil {
				return err
			}
			return tx.PutAggregate(newGen)
		})
		if err != nil {
			t.Fatal(err)
		}
		if !receipt.Reopened || receipt.Generation != 2 {
			t.Fatalf("reopen receipt = %+v", receipt)
		}
		runs := 0
		replay, err := s.Update(reopenOp, func(tx *taskauthority.Tx) error {
			runs++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if runs != 0 || !replay.Replayed || !replay.Reopened {
			t.Fatalf("reopen replay runs=%d Replayed=%v Reopened=%v, want 0/true/true", runs, replay.Replayed, replay.Reopened)
		}
		if replay.Generation != receipt.Generation || replay.Revision != receipt.Revision || replay.CommittedAt != receipt.CommittedAt {
			t.Fatalf("reopen replay receipt identity differs: %+v vs %+v", replay, receipt)
		}
	})

	t.Run("staging two currents for one task is rejected", func(t *testing.T) {
		s := newStore()
		_, err := s.Update(op("op-bad", "t1"), func(tx *taskauthority.Tx) error {
			if err := tx.PutAggregate(mustAggregate(t, "t1", 1, "queued")); err != nil {
				return err
			}
			return tx.PutAggregate(mustAggregate(t, "t1", 2, "queued"))
		})
		if !errors.Is(err, taskauthority.ErrConflict) {
			t.Fatalf("error = %v, want ErrConflict", err)
		}
		v, _ := s.View()
		if len(v.Aggregates) != 0 {
			t.Fatalf("rejected transaction must not mutate: %d aggregates", len(v.Aggregates))
		}
	})

	t.Run("multi-task transaction rejected", func(t *testing.T) {
		s := newStore()
		_, err := s.Update(op("op-multi", "t1"), func(tx *taskauthority.Tx) error {
			if err := tx.PutAggregate(mustAggregate(t, "t1", 1, "queued")); err != nil {
				return err
			}
			return tx.PutAggregate(mustAggregate(t, "t2", 1, "queued"))
		})
		if !errors.Is(err, taskauthority.ErrInvalidInput) {
			t.Fatalf("error = %v, want ErrInvalidInput", err)
		}
		v, _ := s.View()
		if len(v.Aggregates) != 0 || len(v.Receipts) != 0 {
			t.Fatalf("rejected multi-task transaction must not mutate: %+v", v)
		}
	})

	t.Run("holds, interpretations, and decisions persist", func(t *testing.T) {
		s := newStore()
		if _, err := s.Update(op("op-hold", ""), func(tx *taskauthority.Tx) error {
			return tx.PutHold(mustHold(t, "hold-1", "start", "t1", "test hold"))
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Update(op("op-interp", ""), func(tx *taskauthority.Tx) error {
			return tx.PutInterpretation(mustInterpretation(t, "interp-1"))
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Update(op("op-decision", ""), func(tx *taskauthority.Tx) error {
			return tx.PutDecision(mustDecision(t, "decision-1", "interp-1"))
		}); err != nil {
			t.Fatal(err)
		}
		v, _ := s.View()
		got, ok := v.Hold("hold-1")
		if !ok || got.ID != "hold-1" || got.ReleasedAt != 0 {
			t.Fatalf("hold = %+v", got)
		}
		var interp *taskauthority.DispatchInterpretation
		for i := range v.Interpretations {
			if v.Interpretations[i].ID == "interp-1" {
				interp = &v.Interpretations[i]
			}
		}
		if interp == nil || interp.Outcome != taskauthority.DispatchInterpretationAccepted {
			t.Fatalf("interpretation missing: %+v", v.Interpretations)
		}
		var decision *taskauthority.DispatchDecision
		for i := range v.Decisions {
			if v.Decisions[i].Key == "decision-1" {
				decision = &v.Decisions[i]
			}
		}
		if decision == nil || decision.InterpretationID != "interp-1" {
			t.Fatalf("decision missing: %+v", v.Decisions)
		}
	})

	t.Run("multiple audit events in one transaction rejected", func(t *testing.T) {
		s := newStore()
		_, err := s.Update(op("op-multi-audit", "t1"), func(tx *taskauthority.Tx) error {
			if err := tx.PutAggregate(mustAggregate(t, "t1", 1, "queued")); err != nil {
				return err
			}
			if err := tx.AppendAudit(mustAudit(t, "op-multi-audit", "t1", 1, "", "queued")); err != nil {
				return err
			}
			return tx.AppendAudit(mustAudit(t, "op-multi-audit", "t1", 1, "queued", "working"))
		})
		if !errors.Is(err, taskauthority.ErrInvalidInput) {
			t.Fatalf("error = %v, want ErrInvalidInput", err)
		}
		v, _ := s.View()
		if len(v.Aggregates) != 0 || len(v.Audit) != 0 || len(v.Receipts) != 0 {
			t.Fatalf("rejected multi-audit transaction must not mutate: %+v", v)
		}
	})

	t.Run("audit events accumulate across operations", func(t *testing.T) {
		s := newStore()
		for i, id := range []string{"op-a1", "op-a2"} {
			_, err := s.Update(op(id, "t1"), func(tx *taskauthority.Tx) error {
				agg := mustAggregate(t, "t1", 1, "queued")
				agg.Revision = taskauthority.Revision(i + 1)
				if err := tx.PutAggregate(agg); err != nil {
					return err
				}
				return tx.AppendAudit(mustAudit(t, id, "t1", 1, "", "queued"))
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		v, _ := s.View()
		if len(v.Audit) != 2 {
			t.Fatalf("audit = %d events, want 2", len(v.Audit))
		}
		seen := map[string]bool{}
		for _, ev := range v.Audit {
			seen[ev.OperationID] = true
		}
		if !seen["op-a1"] || !seen["op-a2"] {
			t.Fatalf("audit events = %+v, want op-a1 and op-a2", v.Audit)
		}
	})

	t.Run("foreign audit record commits keyed by its own operation", func(t *testing.T) {
		s := newStore()
		_, err := s.Update(op("op-receive", "t1"), func(tx *taskauthority.Tx) error {
			if err := tx.PutAggregate(mustAggregate(t, "t1", 1, "queued")); err != nil {
				return err
			}
			// A historical audit event transferred from the source authority
			// carries its own operation ID; the destination receive operation
			// stages it alongside the transaction's own typed audit event.
			historical := mustAudit(t, "source-create-t1-1", "t1", 1, "", "queued")
			if err := tx.PutAuditRecord(historical); err != nil {
				return err
			}
			return tx.AppendAudit(mustAudit(t, "op-receive", "t1", 1, "", "queued"))
		})
		if err != nil {
			t.Fatal(err)
		}
		v, _ := s.View()
		if len(v.Audit) != 2 {
			t.Fatalf("audit = %d events, want 2", len(v.Audit))
		}
		seen := map[string]bool{}
		for _, ev := range v.Audit {
			seen[ev.OperationID] = true
		}
		if !seen["source-create-t1-1"] || !seen["op-receive"] {
			t.Fatalf("audit events = %+v, want source history and receive audit", v.Audit)
		}
	})

	t.Run("empty transaction receipt", func(t *testing.T) {
		s := newStore()
		first, err := s.Update(op("op-empty", ""), func(tx *taskauthority.Tx) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		if first.OperationID != "op-empty" || first.Digest == "" || first.CommittedAt <= 0 ||
			first.TaskID != "" || first.Generation != 0 || first.Revision != 0 || first.Phase != "" {
			t.Fatalf("empty transaction receipt = %+v, want identity without task outcome", first)
		}
		v, _ := s.View()
		if len(v.Aggregates) != 0 || len(v.Audit) != 0 || len(v.Holds) != 0 || len(v.Receipts) != 1 {
			t.Fatalf("empty transaction view = %+v, want one receipt only", v)
		}
		second, err := s.Update(op("op-empty", ""), func(tx *taskauthority.Tx) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		if !second.Replayed || second.CommittedAt != first.CommittedAt || second.OperationID != first.OperationID {
			t.Fatalf("empty transaction replay = %+v, want original receipt", second)
		}
	})

	t.Run("non-hex 64-character digest is rejected", func(t *testing.T) {
		s := newStore()
		bad := op("op-nonhex", "t1")
		bad.Digest = strings.Repeat("z", 64)
		_, err := s.Update(bad, func(tx *taskauthority.Tx) error {
			return tx.PutAggregate(mustAggregate(t, "t1", 1, "queued"))
		})
		if !errors.Is(err, taskauthority.ErrInvalidInput) {
			t.Fatalf("error = %v, want ErrInvalidInput", err)
		}
		v, _ := s.View()
		if len(v.Aggregates) != 0 || len(v.Receipts) != 0 {
			t.Fatalf("rejected digest must not mutate: %+v", v)
		}
	})

	t.Run("invalid operation identity is rejected", func(t *testing.T) {
		s := newStore()
		_, err := s.Update(taskauthority.Operation{ID: "../escape", Digest: strings.Repeat("a", 64)}, func(tx *taskauthority.Tx) error {
			return nil
		})
		if !errors.Is(err, taskauthority.ErrInvalidInput) {
			t.Fatalf("error = %v, want ErrInvalidInput", err)
		}
	})
}

// op builds a valid operation with a deterministic digest derived from taskID.
func op(id, taskID string) taskauthority.Operation {
	return taskauthority.Operation{ID: id, Digest: digestOf("op:" + id + ":" + taskID)}
}

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func mustAggregate(t *testing.T, taskID string, generation uint64, phase string) taskauthority.Aggregate {
	t.Helper()
	agg, err := taskauthority.NewAggregate(taskID, "owner", "work", "ship", "", "")
	if err != nil {
		t.Fatal(err)
	}
	agg.Generation = taskauthority.Generation(generation)
	agg.Phase = taskauthority.Phase(phase)
	agg.Revision = taskauthority.FirstRevision
	return agg
}

func mustAudit(t *testing.T, opID, taskID string, generation uint64, before, after string) taskauthority.AuditEvent {
	t.Helper()
	ev := taskauthority.AuditEvent{
		OperationID: opID,
		Actor:       taskauthority.Actor{ID: "test-actor", Rank: "general"},
		Kind:        taskauthority.AuditLifecycle,
		TaskID:      taskID,
		Generation:  taskauthority.Generation(generation),
		Reason:      "test",
		Before:      taskauthority.Phase(before),
		After:       taskauthority.Phase(after),
		At:          time.Now().UnixNano(),
	}
	if err := ev.Validate(); err != nil {
		t.Fatal(err)
	}
	return ev
}

func mustHold(t *testing.T, id, action, taskID, reason string) taskauthority.DispatchHold {
	t.Helper()
	hold := taskauthority.DispatchHold{
		SchemaVersion: taskauthority.TaskAuthoritySchema,
		ID:            id,
		Scope:         taskauthority.DispatchHoldScope{TaskIDs: []string{taskID}},
		Actions:       []taskauthority.DispatchAction{taskauthority.DispatchAction(action)},
		Reason:        reason,
		CreatedAt:     time.Now().UnixNano(),
	}
	if taskID == "" {
		hold.Scope = taskauthority.DispatchHoldScope{}
	}
	return hold
}

func mustInterpretation(t *testing.T, id string) taskauthority.DispatchInterpretation {
	t.Helper()
	return taskauthority.DispatchInterpretation{
		SchemaVersion:            taskauthority.TaskAuthoritySchema,
		ID:                       id,
		RequestedOrder:           []string{"t1"},
		SelectedTasks:            []string{"t1"},
		DependencySnapshotDigest: strings.Repeat("d", 64),
		Outcome:                  taskauthority.DispatchInterpretationAccepted,
		CreatedAt:                time.Now().UnixNano(),
	}
}

func mustDecision(t *testing.T, key, interpretationID string) taskauthority.DispatchDecision {
	t.Helper()
	return taskauthority.DispatchDecision{
		SchemaVersion:    taskauthority.TaskAuthoritySchema,
		Key:              key,
		InterpretationID: interpretationID,
		Reason:           "test decision",
		CreatedAt:        time.Now().UnixNano(),
	}
}
