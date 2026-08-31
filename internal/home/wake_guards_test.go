package home

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSweepRejectsRecordScopeMismatch drives the journal sweep over a planted
// record whose body scope does not match the locked scope its filename places
// it under. Recovery must fail closed rather than apply another scope's items.
func TestSweepRejectsRecordScopeMismatch(t *testing.T) {
	h := newTestHome(t)
	if err := os.MkdirAll(h.journalDir(), 0755); err != nil {
		t.Fatal(err)
	}
	rec := journalRecord{
		TxnID:            "mismatch-txn",
		Scope:            "otherscope",
		FenceToken:       1,
		ExpectedRevision: 0,
		NewRevision:      1,
		Items:            []ChangeItem{{Root: RootData, Key: "k", Data: []byte("x")}},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	// Filename places the record under scope "scope"; the body says otherwise.
	if err := os.WriteFile(filepath.Join(h.journalDir(), "scope.mismatch-txn.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	_, err = h.Commit(lk, "txn", 0, []ChangeItem{{Root: RootData, Key: "k2", Data: []byte("y")}})
	if err == nil || !strings.Contains(err.Error(), "does not match locked scope") {
		t.Fatalf("Commit over scope-mismatched record: got %v, want a locked-scope mismatch error", err)
	}
}

// TestSweepRejectsRecordFilenameMismatch drives the sweep over a planted record
// whose valid body txn id reconstructs a different filename than the file it is
// stored under. A record that does not match its own scope/txn id is corruption.
func TestSweepRejectsRecordFilenameMismatch(t *testing.T) {
	h := newTestHome(t)
	if err := os.MkdirAll(h.journalDir(), 0755); err != nil {
		t.Fatal(err)
	}
	rec := journalRecord{
		TxnID:            "body-txn",
		Scope:            "scope",
		FenceToken:       1,
		ExpectedRevision: 0,
		NewRevision:      1,
		Items:            []ChangeItem{{Root: RootData, Key: "k", Data: []byte("x")}},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	// The body txn id is "body-txn"; the filename encodes "file-txn".
	if err := os.WriteFile(filepath.Join(h.journalDir(), "scope.file-txn.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	_, err = h.Commit(lk, "txn", 0, []ChangeItem{{Root: RootData, Key: "k2", Data: []byte("y")}})
	if err == nil || !strings.Contains(err.Error(), "does not match its scope/txn id") {
		t.Fatalf("Commit over filename-mismatched record: got %v, want a scope/txn id mismatch error", err)
	}
}

// TestReadLeaseTreatsTombstoneAsAbsent proves readLease reports a tombstoned
// lease file as absent rather than parsing the tombstone marker as lease data.
func TestReadLeaseTreatsTombstoneAsAbsent(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(LeaseDir(home), 0700); err != nil {
		t.Fatal(err)
	}
	leasePath := LeaseFilePath(home, "lease-1")
	if err := os.WriteFile(leasePath, []byte(wakeLeaseTombstone+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readLease(home, "lease-1", leasePath); !isLeaseAbsent(err) {
		t.Fatalf("readLease over tombstone: got %v, want a lease-absent error", err)
	}
}

// TestLeaseContainsEventTreatsTombstoneAsAbsent proves the resolution read path
// treats a tombstoned lease as absent instead of scanning it for events.
func TestLeaseContainsEventTreatsTombstoneAsAbsent(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(LeaseDir(home), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LeaseFilePath(home, "lease-1"), []byte(wakeLeaseTombstone), 0600); err != nil {
		t.Fatal(err)
	}
	found, err := leaseContainsEvent(home, "lease-1", "evt:1")
	if found || !isLeaseAbsent(err) {
		t.Fatalf("leaseContainsEvent over tombstone: found=%v err=%v, want found=false and a lease-absent error", found, err)
	}
}

// TestApplyWakeLeaseActionRejectsInvalidAction proves the apply path fails
// closed on a lease action that is neither write nor remove.
func TestApplyWakeLeaseActionRejectsInvalidAction(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(LeaseDir(home), 0700); err != nil {
		t.Fatal(err)
	}
	err := applyWakeLeaseAction(home, wakeMutation{leaseAction: "bogus-action", leaseID: "lease-1"})
	if err == nil || !strings.Contains(err.Error(), "invalid wake mutation lease action") {
		t.Fatalf("applyWakeLeaseAction with bogus action: got %v, want an invalid-action error", err)
	}
}

// TestApplyWakeMutationLockedRejectsEmptyMutation proves a mutation that touches
// neither the queue nor a lease is refused rather than journaled as a no-op.
func TestApplyWakeMutationLockedRejectsEmptyMutation(t *testing.T) {
	home := t.TempDir()
	err := applyWakeMutationLocked(home, wakeMutation{})
	if err == nil || !strings.Contains(err.Error(), "empty wake mutation") {
		t.Fatalf("applyWakeMutationLocked with empty mutation: got %v, want an empty-mutation error", err)
	}
}

// TestCheckWakeMutationWritableRejectsNonDirectory proves the writability
// precheck refuses when a target path exists but is not a directory.
func TestCheckWakeMutationWritableRejectsNonDirectory(t *testing.T) {
	home := t.TempDir()
	// The queue's parent directory (state) exists as a plain file.
	if err := os.WriteFile(filepath.Join(home, "state"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	err := checkWakeMutationWritable(home, wakeMutation{queueSet: true, queueData: []byte("q")})
	if err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("checkWakeMutationWritable over a non-directory target: got %v, want a not-a-directory error", err)
	}
}

// TestReadWakeMutationJournalRejectsInvalidState proves the journal reader fails
// closed on a state value outside {pending, complete}.
func TestReadWakeMutationJournalRejectsInvalidState(t *testing.T) {
	home := t.TempDir()
	if err := writeWakeMutationJournal(home, wakeMutationJournal{State: "bogus"}); err != nil {
		t.Fatal(err)
	}
	_, err := readWakeMutationJournal(home)
	if err == nil || !strings.Contains(err.Error(), "invalid wake mutation journal state") {
		t.Fatalf("readWakeMutationJournal with bogus state: got %v, want an invalid-state error", err)
	}
}

// TestReadWakeMutationJournalRejectsInvalidLeaseAction proves the reader fails
// closed on a lease action outside the known set.
func TestReadWakeMutationJournalRejectsInvalidLeaseAction(t *testing.T) {
	home := t.TempDir()
	if err := writeWakeMutationJournal(home, wakeMutationJournal{State: "pending", LeaseAction: "bogus"}); err != nil {
		t.Fatal(err)
	}
	_, err := readWakeMutationJournal(home)
	if err == nil || !strings.Contains(err.Error(), "invalid wake mutation lease action") {
		t.Fatalf("readWakeMutationJournal with bogus lease action: got %v, want an invalid-action error", err)
	}
}

// TestReadWakeMutationJournalRejectsEmptyPending proves a pending journal that
// records neither a queue write nor a lease action is treated as corruption.
func TestReadWakeMutationJournalRejectsEmptyPending(t *testing.T) {
	home := t.TempDir()
	if err := writeWakeMutationJournal(home, wakeMutationJournal{State: "pending"}); err != nil {
		t.Fatal(err)
	}
	_, err := readWakeMutationJournal(home)
	if err == nil || !strings.Contains(err.Error(), "empty pending wake mutation") {
		t.Fatalf("readWakeMutationJournal with empty pending journal: got %v, want an empty-pending error", err)
	}
}

// TestValidatedLeaseDirRejectsNonDirectoryLeaseRoot proves the lease-root
// resolver refuses when .wake-leases exists as a plain file.
func TestValidatedLeaseDirRejectsNonDirectoryLeaseRoot(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LeaseDir(home), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := validatedLeaseDir(home); err == nil || !strings.Contains(err.Error(), "lease directory is not a directory") {
		t.Fatalf("validatedLeaseDir over a non-directory lease root: got %v, want a not-a-directory error", err)
	}
}
