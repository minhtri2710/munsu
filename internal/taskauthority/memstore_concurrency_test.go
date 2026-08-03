package taskauthority

import (
	"errors"
	"fmt"
	"testing"
)

// TestMemStoreConcurrentUpdatesSerialize proves concurrent updates serialize
// without partial visibility: each transaction observes committed state and
// every staged change lands exactly once.
func TestMemStoreConcurrentUpdatesSerialize(t *testing.T) {
	s := newMemStore()
	if _, err := s.Update(op("op-1", "t1"), func(tx *Tx) error {
		return tx.PutAggregate(mustAggregate(t, "t1", 1, "queued"))
	}); err != nil {
		t.Fatal(err)
	}

	const workers = 16
	done := make(chan error, workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			_, err := s.Update(Operation{ID: fmt.Sprintf("op-conc-%d", i), Digest: digestOf(fmt.Sprintf("op-conc-%d", i))}, func(tx *Tx) error {
				cur, ok := tx.Current("t1")
				if !ok {
					return errors.New("current aggregate missing inside transaction")
				}
				updated := cur.clone()
				updated.Revision++
				return tx.PutAggregate(updated)
			})
			done <- err
		}()
	}
	for i := 0; i < workers; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	v, err := s.View()
	if err != nil {
		t.Fatal(err)
	}
	agg, ok := v.Current("t1")
	if !ok {
		t.Fatal("current aggregate missing")
	}
	if agg.Revision != FirstRevision+Revision(workers) {
		t.Fatalf("revision = %d, want %d", agg.Revision, FirstRevision+Revision(workers))
	}
	if len(v.Receipts) != workers+1 {
		t.Fatalf("receipts = %d, want %d", len(v.Receipts), workers+1)
	}
}

// TestTxApplyFailureRollsBack proves a failing applier leaves the committed
// state untouched: the transaction boundary, not the callback, owns atomicity.
func TestTxApplyFailureRollsBack(t *testing.T) {
	s := newMemStore()
	if _, err := s.Update(op("op-1", "t1"), func(tx *Tx) error {
		return tx.PutAggregate(mustAggregate(t, "t1", 1, "queued"))
	}); err != nil {
		t.Fatal(err)
	}
	tx := NewTx(s.viewUnlocked())
	bad := mustAggregate(t, "t2", 1, "queued")
	if err := tx.PutAggregate(bad); err != nil {
		t.Fatal(err)
	}
	err := tx.Apply(&failApplier{err: errors.New("disk full")})
	if err == nil {
		t.Fatal("expected applier failure")
	}
	v, _ := s.View()
	if len(v.Aggregates) != 1 {
		t.Fatalf("failed apply must not mutate committed state: %d aggregates", len(v.Aggregates))
	}
}

// failApplier returns a static error from every apply method; tests use it
// to prove partial application is rejected by the adapter.
type failApplier struct{ err error }

func (f *failApplier) ApplyAggregate(Aggregate) error { return f.err }
func (f *failApplier) ApplyHold(DispatchHold) error   { return f.err }
func (f *failApplier) ApplyInterpretation(DispatchInterpretation) error {
	return f.err
}
func (f *failApplier) ApplyDecision(DispatchDecision) error { return f.err }
func (f *failApplier) ApplyAudit(AuditEvent) error          { return f.err }
func (f *failApplier) ApplyAuditRecord(AuditEvent) error    { return f.err }
func (f *failApplier) ApplyLeaseMarker(LeaseMarker) error   { return f.err }

// viewUnlocked exposes the committed view for direct transaction tests.
func (s *memStore) viewUnlocked() View {
	return s.state.view()
}
