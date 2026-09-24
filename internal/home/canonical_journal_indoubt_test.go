package home

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestCommitRollsForwardAfterDurableApplyFailure proves a failure after the
// journal record is durable is rolled forward in place: Commit succeeds, the
// revision advances and the record is gone.
func TestCommitRollsForwardAfterDurableApplyFailure(t *testing.T) {
	h := newTestHome(t)
	orig := commitStep
	t.Cleanup(func() { commitStep = orig })
	commitStep = func() error { return errors.New("injected apply failure") }

	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()

	rev, err := h.Commit(lk, "txn", 0, []ChangeItem{{Root: RootData, Key: "k", Data: []byte("v")}})
	if err != nil || rev != 1 {
		t.Fatalf("Commit = %d, %v; want 1, nil", rev, err)
	}
	if got, err := h.readRevision("scope"); err != nil || got != 1 {
		t.Fatalf("revision = %d, %v; want 1", got, err)
	}
	if data, err := h.Read(RootData, "k"); err != nil || string(data) != "v" {
		t.Fatalf("Read k = %q, %v; want v", data, err)
	}
	if _, err := os.Stat(h.journalPath("scope", "txn")); !os.IsNotExist(err) {
		t.Fatalf("journal record still present: %v", err)
	}
}

// TestCommitReturnsInDoubtWhenRollForwardFails proves a failure that persists
// through roll-forward returns ErrInDoubt, and the next Commit applies the
// durable record.
func TestCommitReturnsInDoubtWhenRollForwardFails(t *testing.T) {
	h := newTestHome(t)
	target, err := h.Path(RootData, "k")
	if err != nil {
		t.Fatal(err)
	}
	// A non-empty directory at the target makes every write of k fail.
	if err := os.MkdirAll(filepath.Join(target, "block"), 0700); err != nil {
		t.Fatal(err)
	}

	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()

	if _, err := h.Commit(lk, "txn", 0, []ChangeItem{{Root: RootData, Key: "k", Data: []byte("v")}}); !errors.Is(err, ErrInDoubt) {
		t.Fatalf("Commit: got %v, want ErrInDoubt", err)
	}
	if _, err := os.Stat(h.journalPath("scope", "txn")); err != nil {
		t.Fatalf("durable record missing after ErrInDoubt: %v", err)
	}

	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	// Retrying from the old revision conflicts with its own now-applied change.
	if _, err := h.Commit(lk, "next", 0, []ChangeItem{{Root: RootData, Key: "n", Data: []byte("n")}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Commit from stale revision: got %v, want ErrConflict", err)
	}
	if data, err := h.Read(RootData, "k"); err != nil || string(data) != "v" {
		t.Fatalf("Read k = %q, %v; want v", data, err)
	}
	if rev, err := h.Commit(lk, "next", 1, []ChangeItem{{Root: RootData, Key: "n", Data: []byte("n")}}); err != nil || rev != 2 {
		t.Fatalf("Commit from applied revision = %d, %v; want 2, nil", rev, err)
	}
}

// TestCommitPreDurableFailureIsPlain proves a failure before the record is
// durable stays a plain error and changes nothing.
func TestCommitPreDurableFailureIsPlain(t *testing.T) {
	h := newTestHome(t)
	// A non-empty directory at the record path fails Commit before it writes
	// its record.
	if err := os.MkdirAll(filepath.Join(h.journalPath("scope", "txn"), "block"), 0700); err != nil {
		t.Fatal(err)
	}

	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()

	_, err = h.Commit(lk, "txn", 0, []ChangeItem{{Root: RootData, Key: "k", Data: []byte("v")}})
	if err == nil || errors.Is(err, ErrInDoubt) {
		t.Fatalf("Commit: got %v, want a plain error", err)
	}
	if rev, err := h.readRevision("scope"); err != nil || rev != 0 {
		t.Fatalf("revision = %d, %v; want 0", rev, err)
	}
	if _, err := h.Read(RootData, "k"); err == nil {
		t.Fatal("item k written by a failed pre-durable commit")
	}
}
