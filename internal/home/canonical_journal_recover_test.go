package home

import (
	"errors"
	"strings"
	"testing"
)

// TestCommitRejectsForeignLock proves Commit fails closed when driven by a lock
// acquired from a different home (F007): the lock's owning home must match the
// receiver, otherwise a caller could mutate home B's scope while holding only
// home A's exclusion.
func TestCommitRejectsForeignLock(t *testing.T) {
	hA := newTestHome(t)
	hB := newTestHome(t)

	lkA, err := hA.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	defer lkA.Release()

	if _, err := hB.Commit(lkA, "txn", 0, []ChangeItem{{Root: RootData, Key: "k", Data: []byte("x")}}); !errors.Is(err, ErrForeignLock) {
		t.Fatalf("Commit with foreign lock: got %v, want ErrForeignLock", err)
	}
}

// TestCommitSweepsInDoubtRecord proves the pre-transaction sweep (F005/F006):
// a leftover in-doubt journal record for the scope (a commit interrupted before
// its revision advance) is recovered in place when the next Commit opens, so its
// items become durable and the scope revision advances. A caller retrying from
// the stale revision then correctly gets ErrConflict rather than racing a second
// record at the same expected revision.
func TestCommitSweepsInDoubtRecord(t *testing.T) {
	h := newTestHome(t)

	// Plant a valid, in-doubt record: revision stays 0 (never advanced), as
	// after a crash between writeJournalRecord and writeRevision.
	rec := journalRecord{
		TxnID:            "swept-txn",
		Scope:            "scope",
		FenceToken:       1,
		ExpectedRevision: 0,
		NewRevision:      1,
		Items:            []ChangeItem{{Root: RootData, Key: "swept-key", Data: []byte("swept")}},
	}
	if err := h.writeJournalRecord(rec); err != nil {
		t.Fatalf("plant record: %v", err)
	}

	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()

	// A new commit from the stale revision 0 must fail: the sweep advanced the
	// scope to revision 1 by recovering the in-doubt record.
	if _, err := h.Commit(lk, "new-txn", 0, []ChangeItem{{Root: RootData, Key: "new-key", Data: []byte("new")}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Commit from stale revision: got %v, want ErrConflict", err)
	}

	// The swept record's item is now durable.
	if data, err := h.Read(RootData, "swept-key"); err != nil {
		t.Fatalf("Read swept-key: %v", err)
	} else if string(data) != "swept" {
		t.Fatalf("swept-key = %q, want %q", data, "swept")
	}

	// Retrying from the recovered revision 1 succeeds.
	if _, err := h.Commit(lk, "new-txn", 1, []ChangeItem{{Root: RootData, Key: "new-key", Data: []byte("new")}}); err != nil {
		t.Fatalf("Commit from recovered revision: %v", err)
	}
	if data, err := h.Read(RootData, "new-key"); err != nil {
		t.Fatalf("Read new-key: %v", err)
	} else if string(data) != "new" {
		t.Fatalf("new-key = %q, want %q", data, "new")
	}
}

func TestRecoverPendingAppliesInterruptedCommit(t *testing.T) {
	h := newTestHome(t)
	rec := journalRecord{
		TxnID:            "pending-txn",
		Scope:            "scope",
		FenceToken:       1,
		ExpectedRevision: 0,
		NewRevision:      1,
		Items:            []ChangeItem{{Root: RootData, Key: "pending-key", Data: []byte("pending")}},
	}
	if err := h.writeJournalRecord(rec); err != nil {
		t.Fatalf("plant record: %v", err)
	}

	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	if err := h.RecoverPending(lk); err != nil {
		t.Fatalf("RecoverPending: %v", err)
	}
	if data, err := h.Read(RootData, "pending-key"); err != nil {
		t.Fatalf("Read recovered item: %v", err)
	} else if string(data) != "pending" {
		t.Fatalf("recovered item = %q, want %q", data, "pending")
	}
	if rev, err := h.readRevision("scope"); err != nil {
		t.Fatalf("read recovered revision: %v", err)
	} else if rev != 1 {
		t.Fatalf("recovered revision = %d, want 1", rev)
	}
}

// TestSweepRejectsRecordWithoutItems proves recovery fails closed on a
// structurally invalid record (F008): a JSON-decodable record with no change
// items must not silently advance the scope revision.
func TestSweepRejectsRecordWithoutItems(t *testing.T) {
	h := newTestHome(t)

	rec := journalRecord{
		TxnID:            "empty-txn",
		Scope:            "scope",
		FenceToken:       1,
		ExpectedRevision: 0,
		NewRevision:      1,
		Items:            nil,
	}
	if err := h.writeJournalRecord(rec); err != nil {
		t.Fatalf("plant record: %v", err)
	}

	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()

	_, err = h.Commit(lk, "txn", 0, []ChangeItem{{Root: RootData, Key: "k", Data: []byte("x")}})
	if err == nil || !strings.Contains(err.Error(), "no items") {
		t.Fatalf("Commit over invalid record: got %v, want a 'no items' error", err)
	}
}
