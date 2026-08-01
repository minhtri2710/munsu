package taskauthority

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

// RunStoreContractSuite runs the shared adapter contract assertions. Every
// Store implementation (in-memory, filesystem) must pass this suite so the two
// adapters have identical receipts, Revision behavior, idempotency, and
// rollback semantics.
func RunStoreContractSuite(t *testing.T, newStore func() Store) {
	t.Run("empty view", func(t *testing.T) {
		s := newStore()
		v, err := s.View()
		if err != nil {
			t.Fatal(err)
		}
		if len(v.Aggregates) != 0 || len(v.Holds) != 0 || len(v.Receipts) != 0 {
			t.Fatalf("expected empty view, got %+v", v)
		}
	})

	t.Run("rollback on callback error", func(t *testing.T) {
		s := newStore()
		_, err := s.Update(op("op-rollback", "t1"), func(tx *Tx) error {
			if err := tx.PutAggregate(mustAggregate(t, "t1", 1, "queued")); err != nil {
				return err
			}
			return errors.New("boom")
		})
		if err == nil {
			t.Fatal("expected callback error")
		}
		v, _ := s.View()
		if len(v.Aggregates) != 0 || len(v.Receipts) != 0 || len(v.Audit) != 0 {
			t.Fatalf("callback error must not mutate state: %+v", v)
		}
	})

	t.Run("staged records persist with revision", func(t *testing.T) {
		s := newStore()
		_, err := s.Update(op("op-create", "t1"), func(tx *Tx) error {
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
		if agg.Revision != 1 || agg.Phase != PhaseQueued {
			t.Fatalf("aggregate = %+v", agg)
		}
		if len(v.Audit) != 1 {
			t.Fatalf("audit = %d events, want 1", len(v.Audit))
		}
		if len(v.Receipts) != 1 {
			t.Fatalf("receipts = %d, want 1", len(v.Receipts))
		}
		receipt := v.Receipts[0]
		if receipt.TaskID != "t1" || receipt.Generation != 1 || receipt.Revision != 1 || receipt.Phase != PhaseQueued {
			t.Fatalf("receipt outcome = %+v", receipt)
		}
	})

	t.Run("revision advances exactly once", func(t *testing.T) {
		s := newStore()
		if _, err := s.Update(op("op-1", "t1"), func(tx *Tx) error {
			return tx.PutAggregate(mustAggregate(t, "t1", 1, "queued"))
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Update(op("op-2", "t1"), func(tx *Tx) error {
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
			revision Revision
		}{{"unchanged", 1}, {"skipped", 3}} {
			t.Run(tc.name, func(t *testing.T) {
				s := newStore()
				if _, err := s.Update(op("op-1", "t1"), func(tx *Tx) error {
					return tx.PutAggregate(mustAggregate(t, "t1", 1, "queued"))
				}); err != nil {
					t.Fatal(err)
				}
				_, err := s.Update(op("op-2", "t1"), func(tx *Tx) error {
					agg := mustAggregate(t, "t1", 1, "working")
					agg.Revision = tc.revision
					return tx.PutAggregate(agg)
				})
				if !errors.Is(err, ErrConflict) {
					t.Fatalf("revision %d update = %v, want ErrConflict", tc.revision, err)
				}
			})
		}
	})

	t.Run("duplicate operation id same digest replays", func(t *testing.T) {
		s := newStore()
		runs := 0
		first, err := s.Update(op("op-replay", "t1"), func(tx *Tx) error {
			runs++
			return tx.PutAggregate(mustAggregate(t, "t1", 1, "queued"))
		})
		if err != nil {
			t.Fatal(err)
		}
		second, err := s.Update(op("op-replay", "t1"), func(tx *Tx) error {
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
		if _, err := s.Update(op("op-conflict", "t1"), func(tx *Tx) error {
			return tx.PutAggregate(mustAggregate(t, "t1", 1, "queued"))
		}); err != nil {
			t.Fatal(err)
		}
		other := op("op-conflict", "t1")
		other.Digest = digestOf("different-intent")
		_, err := s.Update(other, func(tx *Tx) error { return nil })
		if !errors.Is(err, ErrOperationConflict) {
			t.Fatalf("error = %v, want ErrOperationConflict", err)
		}
	})

	t.Run("generation replacement", func(t *testing.T) {
		s := newStore()
		gen1 := mustAggregate(t, "t1", 1, "done")
		gen1.Current = true
		if _, err := s.Update(op("op-gen1", "t1"), func(tx *Tx) error {
			return tx.PutAggregate(gen1)
		}); err != nil {
			t.Fatal(err)
		}
		old := mustAggregate(t, "t1", 1, "done")
		old.Current = false
		old.Revision = 2
		newGen := mustAggregate(t, "t1", 2, "queued")
		newGen.Current = true
		receipt, err := s.Update(op("op-reopen", "t1"), func(tx *Tx) error {
			if err := tx.PutAggregate(old); err != nil {
				return err
			}
			return tx.PutAggregate(newGen)
		})
		if err != nil {
			t.Fatal(err)
		}
		if receipt.TaskID != "t1" || receipt.Generation != 2 || receipt.Revision != FirstRevision || receipt.Phase != PhaseQueued || !receipt.Reopened {
			t.Fatalf("reopen receipt = %+v", receipt)
		}
		v, _ := s.View()
		cur, ok := v.Current("t1")
		if !ok || cur.Generation != 2 || cur.Revision != FirstRevision {
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

	t.Run("staging two currents for one task is rejected", func(t *testing.T) {
		s := newStore()
		_, err := s.Update(op("op-bad", "t1"), func(tx *Tx) error {
			if err := tx.PutAggregate(mustAggregate(t, "t1", 1, "queued")); err != nil {
				return err
			}
			return tx.PutAggregate(mustAggregate(t, "t1", 2, "queued"))
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("error = %v, want ErrConflict", err)
		}
		v, _ := s.View()
		if len(v.Aggregates) != 0 {
			t.Fatalf("rejected transaction must not mutate: %d aggregates", len(v.Aggregates))
		}
	})

	t.Run("holds and decisions persist", func(t *testing.T) {
		s := newStore()
		hold := mustHold(t, "hold-1", "start", "t1", "test hold")
		if _, err := s.Update(op("op-hold", ""), func(tx *Tx) error {
			return tx.PutHold(hold)
		}); err != nil {
			t.Fatal(err)
		}
		v, _ := s.View()
		got, ok := v.Hold("hold-1")
		if !ok || got.ID != "hold-1" || got.ReleasedAt != 0 {
			t.Fatalf("hold = %+v", got)
		}
	})

	t.Run("invalid operation identity is rejected", func(t *testing.T) {
		s := newStore()
		_, err := s.Update(Operation{ID: "../escape", Digest: strings.Repeat("a", 64)}, func(tx *Tx) error {
			return nil
		})
		if err == nil {
			t.Fatal("expected validation error for unsafe operation id")
		}
	})
}

// op builds a valid operation with a deterministic digest derived from taskID.
func op(id, taskID string) Operation {
	return Operation{ID: id, Digest: digestOf("op:" + id + ":" + taskID)}
}

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func mustAggregate(t *testing.T, taskID string, generation uint64, phase string) Aggregate {
	t.Helper()
	agg, err := NewAggregate(taskID, "owner", "work", "ship", "", "")
	if err != nil {
		t.Fatal(err)
	}
	agg.Generation = Generation(generation)
	agg.Phase = Phase(phase)
	agg.Revision = FirstRevision
	if err := validateAggregate(agg); err != nil {
		t.Fatal(err)
	}
	return agg
}

func mustAudit(t *testing.T, opID, taskID string, generation uint64, before, after string) AuditEvent {
	t.Helper()
	ev := AuditEvent{
		OperationID: opID,
		Actor:       Actor{ID: "test-actor", Rank: "general"},
		Kind:        AuditLifecycle,
		TaskID:      taskID,
		Generation:  Generation(generation),
		Reason:      "test",
		Before:      Phase(before),
		After:       Phase(after),
		At:          time.Now().UnixNano(),
	}
	if err := ev.Validate(); err != nil {
		t.Fatal(err)
	}
	return ev
}

func mustHold(t *testing.T, id, action, taskID, reason string) DispatchHold {
	t.Helper()
	hold := DispatchHold{
		SchemaVersion: taskAuthoritySchema,
		ID:            id,
		Scope:         DispatchHoldScope{TaskIDs: []string{taskID}},
		Actions:       []DispatchAction{DispatchAction(action)},
		Reason:        reason,
		CreatedAt:     time.Now().UnixNano(),
	}
	if taskID == "" {
		hold.Scope = DispatchHoldScope{}
	}
	if err := validateHold(hold); err != nil {
		t.Fatal(err)
	}
	return hold
}
