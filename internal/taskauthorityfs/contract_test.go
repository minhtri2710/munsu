package taskauthorityfs

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/taskauthority/storecontract"
)

// TestStoreContract runs the one authoritative Store contract suite against a
// fresh filesystem Store over a temporary on-disk home. Every contract
// assertion — receipts, Revision behavior, idempotency, rollback, conflict
// typing, dispatch records, audit, reopen semantics, and typed errors — must
// hold for the filesystem adapter exactly as it holds for the in-memory
// adapter. Each sub-test uses its own fresh home.
func TestStoreContract(t *testing.T) {
	storecontract.Run(t, func() taskauthority.Store {
		return openStore(t, t.TempDir())
	})
}

// TestStoreContractDurableResultsAcrossReopen proves records committed by one
// Store instance are durably visible to a fresh Store instance constructed on
// the same home, and that replaying a committed operation after reopen
// returns the original receipt without re-running the callback.
func TestStoreContractDurableResultsAcrossReopen(t *testing.T) {
	home := t.TempDir()
	op := testOp("op-durable", "t1")
	first, err := openStore(t, home).Update(op, createUpdate("op-durable", "t1"))
	if err != nil {
		t.Fatal(err)
	}

	s2 := openStore(t, home)
	v := mustView(t, s2)
	agg, ok := v.Current("t1")
	if !ok || agg.Revision != taskauthority.FirstRevision || agg.Phase != taskauthority.PhaseQueued {
		t.Fatalf("durable current aggregate = %+v", agg)
	}
	if len(v.Audit) != 1 || len(v.Receipts) != 1 {
		t.Fatalf("durable records = %d audit, %d receipts; want 1/1", len(v.Audit), len(v.Receipts))
	}

	runs := 0
	replay, err := s2.Update(op, func(tx *taskauthority.Tx) error {
		runs++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if runs != 0 || !replay.Replayed {
		t.Fatalf("replay runs=%d Replayed=%v, want 0/true", runs, replay.Replayed)
	}
	if replay.OperationID != first.OperationID || replay.Digest != first.Digest || replay.CommittedAt != first.CommittedAt {
		t.Fatalf("replay receipt identity differs: %+v vs %+v", replay, first)
	}
}

// TestStoreContractReopenedReceiptDurableAcrossReopen proves the reopened
// receipt semantics survive closing and reconstructing the Store: a reopen
// transaction's receipt carries Reopened=true from a fresh instance, the new
// Generation is current, and the prior Generation is preserved as a
// historical record at the advanced Revision.
func TestStoreContractReopenedReceiptDurableAcrossReopen(t *testing.T) {
	home := t.TempDir()
	s1 := openStore(t, home)
	if _, err := s1.Update(testOp("op-create", "t1"), createUpdate("op-create", "t1")); err != nil {
		t.Fatal(err)
	}
	reopenOp := testOp("op-reopen", "t1")
	reopenReceipt, err := s1.Update(reopenOp, reopenUpdate("op-reopen", "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if !reopenReceipt.Reopened || reopenReceipt.Generation != 2 {
		t.Fatalf("reopen receipt = %+v, want Reopened generation 2", reopenReceipt)
	}

	s2 := openStore(t, home)
	v := mustView(t, s2)
	cur, ok := v.Current("t1")
	if !ok || cur.Generation != 2 || cur.Revision != taskauthority.FirstRevision {
		t.Fatalf("durable current after reopen = %+v", cur)
	}
	historical, ok := v.Aggregate("t1", 1)
	if !ok || historical.Current || historical.Revision != 2 {
		t.Fatalf("durable historical generation 1 = %+v", historical)
	}

	runs := 0
	replay, err := s2.Update(reopenOp, func(tx *taskauthority.Tx) error {
		runs++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if runs != 0 || !replay.Replayed || !replay.Reopened {
		t.Fatalf("reopened replay runs=%d Replayed=%v Reopened=%v, want 0/true/true", runs, replay.Replayed, replay.Reopened)
	}
	if replay.Generation != reopenReceipt.Generation || replay.Revision != reopenReceipt.Revision || replay.CommittedAt != reopenReceipt.CommittedAt {
		t.Fatalf("reopened replay receipt identity differs: %+v vs %+v", replay, reopenReceipt)
	}
}

// TestStoreContractInterruptedUpdateReplaysAfterReopen proves an update that
// crashed mid-journal on one Store instance is recovered to the fully
// committed state by a fresh Store instance on the same home, and the
// operation then replays from the durable receipt without re-running the
// callback.
func TestStoreContractInterruptedUpdateReplaysAfterReopen(t *testing.T) {
	home := t.TempDir()
	op := testOp("op-crash", "t1")
	s := openStore(t, home)
	s.fault = &faultInjector{stage: faultStageAfterDataWrite, afterWrite: 1, err: errInjected}
	if _, err := s.Update(op, createUpdate("op-crash", "t1")); !errors.Is(err, errInjected) {
		t.Fatalf("Update error = %v, want injected crash", err)
	}

	s2 := openStore(t, home)
	assertCommittedCreate(t, s2, op, "t1")
	runs := 0
	replay, err := s2.Update(op, func(tx *taskauthority.Tx) error {
		runs++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if runs != 0 || !replay.Replayed {
		t.Fatalf("replay runs=%d Replayed=%v, want 0/true", runs, replay.Replayed)
	}
}
