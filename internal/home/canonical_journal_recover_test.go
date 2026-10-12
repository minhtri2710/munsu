package home

import (
	"errors"
	"os"
	"path/filepath"
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

	if _, err := hB.Commit(lkA, "txn", 0, []ChangeItem{{Root: RootState, Key: "k", Data: []byte("x")}}); !errors.Is(err, ErrForeignLock) {
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
		Items:            []ChangeItem{{Root: RootState, Key: "swept-key", Data: []byte("swept")}},
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
	if _, err := h.Commit(lk, "new-txn", 0, []ChangeItem{{Root: RootState, Key: "new-key", Data: []byte("new")}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Commit from stale revision: got %v, want ErrConflict", err)
	}

	// The swept record's item is now durable.
	if data, err := h.Read(RootState, "swept-key"); err != nil {
		t.Fatalf("Read swept-key: %v", err)
	} else if string(data) != "swept" {
		t.Fatalf("swept-key = %q, want %q", data, "swept")
	}

	// Retrying from the recovered revision 1 succeeds.
	if _, err := h.Commit(lk, "new-txn", 1, []ChangeItem{{Root: RootState, Key: "new-key", Data: []byte("new")}}); err != nil {
		t.Fatalf("Commit from recovered revision: %v", err)
	}
	if data, err := h.Read(RootState, "new-key"); err != nil {
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
		Items:            []ChangeItem{{Root: RootState, Key: "pending-key", Data: []byte("pending")}},
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
	if data, err := h.Read(RootState, "pending-key"); err != nil {
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

	_, err = h.Commit(lk, "txn", 0, []ChangeItem{{Root: RootState, Key: "k", Data: []byte("x")}})
	if err == nil || !strings.Contains(err.Error(), "no items") {
		t.Fatalf("Commit over invalid record: got %v, want a 'no items' error", err)
	}
}

// TestJournalRecordDecodeFailsClosed proves a record that is valid except for an
// unknown field or trailing data is corruption: Open recovery and the next
// Commit on that scope both refuse it, and the record stays on disk unapplied.
func TestJournalRecordDecodeFailsClosed(t *testing.T) {
	for name, mutate := range map[string]func(string) string{
		"unknown field": func(s string) string { return strings.Replace(s, "{", `{"committed":false,`, 1) },
		"trailing data": func(s string) string { return s + "{}\n" },
	} {
		t.Run(name, func(t *testing.T) {
			h := newTestHome(t)
			rec := journalRecord{
				TxnID: "txn", Scope: "scope", FenceToken: 1, ExpectedRevision: 0, NewRevision: 1,
				Items: []ChangeItem{{Root: RootState, Key: "k", Data: []byte("v")}},
			}
			if err := h.writeJournalRecord(rec); err != nil {
				t.Fatal(err)
			}
			path := h.journalPath("scope", "txn")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(mutate(string(data))), 0600); err != nil {
				t.Fatal(err)
			}

			if _, err := Open(h.root); err == nil || !strings.Contains(err.Error(), "decode journal record") {
				t.Fatalf("Open = %v, want decode journal record error", err)
			}
			lk, err := h.Lock("scope")
			if err != nil {
				t.Fatal(err)
			}
			defer lk.Release()
			if _, err := h.Commit(lk, "next", 0, []ChangeItem{{Root: RootState, Key: "n", Data: []byte("n")}}); err == nil || !strings.Contains(err.Error(), "decode journal record") {
				t.Fatalf("Commit = %v, want decode journal record error", err)
			}
			if _, err := h.Read(RootState, "k"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("refused record applied: Read k err = %v", err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("refused record removed: %v", err)
			}
		})
	}
}

// TestJournalWritesOnlyTheStateRoot proves a journal writes only state: replay
// refuses a record a seat planted for config (the fence escape through the next
// unfenced Open) and names the rm that clears it, and Commit refuses another
// root before any record is durable. A state item still replays and commits.
func TestJournalWritesOnlyTheStateRoot(t *testing.T) {
	for _, tc := range []struct {
		name, root string
		replay     bool
	}{
		{"replay refuses config", RootConfig, true},
		{"replay applies state", RootState, true},
		{"commit refuses projects", RootProjects, false},
		{"commit applies state", RootState, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHome(t)
			refused := tc.root != RootState
			target := filepath.Join(h.root, tc.root, "forged.json")
			// The txn id is the writer's choice; a quote in it must not end the rm's quoting.
			record := h.journalPath("fleet-projects", "forged'1")
			item := ChangeItem{Root: tc.root, Key: "forged.json", Data: []byte("{}\n")}
			if tc.replay {
				if err := h.writeJournalRecord(journalRecord{TxnID: "forged'1", Scope: "fleet-projects", FenceToken: 1,
					ExpectedRevision: 0, NewRevision: 1, Items: []ChangeItem{item}}); err != nil {
					t.Fatal(err)
				}
				_, err := Open(h.root)
				if refused {
					want := `journal record fleet-projects.forged'1.json writes root "config"; a journal writes only state; remove it: rm '` +
						strings.ReplaceAll(record, "'", `'\''`) + `'`
					if err == nil || !strings.Contains(err.Error(), want) {
						t.Fatalf("Open = %v, want the refusal %q", err, want)
					}
				} else if err != nil {
					t.Fatalf("Open = %v, want the state record replayed", err)
				}
			} else {
				lk, err := h.Lock("fleet-projects")
				if err != nil {
					t.Fatal(err)
				}
				defer lk.Release()
				_, err = h.Commit(lk, "forged'1", 0, []ChangeItem{item})
				if refused {
					if !errors.Is(err, ErrJournalRoot) || errors.Is(err, ErrInDoubt) || !strings.Contains(err.Error(), `item root "projects"`) {
						t.Fatalf("Commit = %v, want ErrJournalRoot before any record is durable", err)
					}
				} else if err != nil {
					t.Fatalf("Commit = %v, want the state item committed", err)
				}
			}
			if _, err := os.Stat(target); refused == (err == nil) {
				t.Fatalf("%s written = %v, want %v", target, err == nil, !refused)
			}
			if _, err := os.Stat(record); (tc.replay && refused) != (err == nil) {
				t.Fatalf("record on disk = %v, want %v (kept only for a refused replay)", err == nil, tc.replay && refused)
			}
		})
	}
}
