package fleet

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

// TestHandoffJournalCrashDuringCommitConverges proves Home's mechanical
// write-ahead recovery converges an interrupted journal create Commit. The
// crash is simulated by writing a Home journal record directly (the durable
// state a crash leaves behind: the write-ahead record exists but the index
// and journal items are not yet applied). Reopening the home replays the
// record, and RecoverTaskHandoffs then resumes the SAME transfer.
func TestHandoffJournalCrashDuringCommitConverges(t *testing.T) {
	parent, captain := seedHandoffPair(t)
	parentAuth := mustAuthority(t, parent)
	seedCanonicalQueuedTask(t, parentAuth, "TASK-1", "general")
	seedCanonicalQueuedTask(t, parentAuth, "TASK-2", "general")

	// Build the durable intent exactly as production would, but do NOT apply
	// it: leave only the Home write-ahead record on disk (the crash state).
	h, err := mhome.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	destHome, err := mhome.Open(captain)
	if err != nil {
		t.Fatal(err)
	}
	sourceAuth, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	destAuth, err := taskauthority.NewCanonical(destHome)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := buildTransferJournal(canonicalHandoffHomeOrDie(t, parent), canonicalHandoffHomeOrDie(t, captain), "test-sm", sourceAuth, destAuth, []string{"TASK-1", "TASK-2"})
	if err != nil {
		t.Fatal(err)
	}
	idx := handoffJournalIndex{Version: handoffIndexVersion, HomeRevision: 1, Active: []string{journal.ID}}
	items, err := handoffJournalItems(idx, journal)
	if err != nil {
		t.Fatal(err)
	}
	// Fabricate the interrupted-commit record: scope txn file present, no
	// items applied (mirrors a crash after the write-ahead record is fsynced
	// but before the change-set items are applied).
	rec := map[string]any{
		"txn_id":            handoffTxnID(journal.ID, "create"),
		"scope":             handoffLockScope,
		"fence_token":       1,
		"expected_revision": 0,
		"new_revision":      1,
		"items":             items,
		"committed":         false,
	}
	recData, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	journalDir := filepath.Join(parent, ".journal")
	if err := os.MkdirAll(journalDir, 0700); err != nil {
		t.Fatal(err)
	}
	wantRecord := filepath.Join(journalDir, handoffLockScope+"."+handoffTxnID(journal.ID, "create")+".json")
	if err := os.WriteFile(wantRecord, recData, 0600); err != nil {
		t.Fatal(err)
	}

	// Reopening the home runs Home's mechanical recovery: the write-ahead
	// record is replayed, so the index and journal become durable and the
	// record is removed.
	if _, err := mhome.Open(parent); err != nil {
		t.Fatalf("reopen after interrupted commit: %v", err)
	}
	if _, err := os.Stat(wantRecord); !os.IsNotExist(err) {
		t.Fatalf("write-ahead record not removed by recovery: %v", err)
	}
	if got := pendingJournalCount(t, parent); got != 1 {
		t.Fatalf("after mechanical recovery: pending = %d, want 1", got)
	}

	// Recovery resumes the SAME transfer and commits terminal truth.
	if err := RecoverTaskHandoffs(parent); err != nil {
		t.Fatalf("RecoverTaskHandoffs after interrupted commit: %v", err)
	}
	for _, taskID := range []string{"TASK-1", "TASK-2"} {
		mustTransferNoOwner(t, parent, taskID)
		agg := mustTransferOwner(t, captain, taskID)
		if agg.Generation != 1 || agg.Definition.Owner != "captain:test-sm" {
			t.Fatalf("%s aggregate = %+v", taskID, agg)
		}
	}
	if pendingJournalCount(t, parent) != 0 {
		t.Fatalf("pending journal remains after recovery")
	}
	if completedJournalCount(t, parent) != 1 {
		t.Fatalf("terminal journal record not retained after recovery")
	}
}

// TestHandoffJournalRejectsMalformedIndex fails closed when the bounded index
// document is corrupt (cannot be decoded), so no active transfer is inferred.
func TestHandoffJournalRejectsMalformedIndex(t *testing.T) {
	parent := t.TempDir()
	seedCanonicalTransferHome(t, parent)
	dir := filepath.Join(parent, "state", taskHandoffDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverTaskHandoffs(parent); err == nil {
		t.Fatal("expected malformed index to fail closed")
	}
	if _, err := os.Stat(filepath.Join(dir, "index.json")); err != nil {
		t.Fatalf("malformed index removed: %v", err)
	}
}

// TestHandoffJournalRejectsMissingReferencedJournal fails closed when the
// index references a journal that is absent (contradictory index state).
func TestHandoffJournalRejectsMissingReferencedJournal(t *testing.T) {
	parent := t.TempDir()
	seedCanonicalTransferHome(t, parent)
	h, err := mhome.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	// Build a valid index citing a journal that has no record, through the
	// production Home causal path (Lock + Commit).
	lk, err := h.Lock(handoffLockScope)
	if err != nil {
		t.Fatal(err)
	}
	idx := handoffJournalIndex{Version: handoffIndexVersion, HomeRevision: 1, Active: []string{"ghost-transfer"}}
	idxData, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	items := []mhome.ChangeItem{
		{Root: mhome.RootState, Key: handoffIndexKey, Data: append(idxData, '\n')},
	}
	if _, err := h.Commit(lk, "ghost-index-create", 0, items); err != nil {
		t.Fatal(err)
	}
	lk.Release()

	if err := RecoverTaskHandoffs(parent); err == nil {
		t.Fatal("expected missing referenced journal to fail closed")
	}
}

// TestHandoffJournalTerminalEntriesNotResumed proves a completed transfer's
// terminal journal record is never resumed: it is retained on disk but is not
// in the active index, so a later recovery is a no-op and ownership is
// unchanged.
func TestHandoffJournalTerminalEntriesNotResumed(t *testing.T) {
	parent, captain := seedHandoffPair(t)
	seedCanonicalQueuedTask(t, mustAuthority(t, parent), "TASK-1", "general")

	if err := Handoff(parent, captain, []string{"TASK-1"}); err != nil {
		t.Fatalf("Handoff: %v", err)
	}
	if pendingJournalCount(t, parent) != 0 {
		t.Fatalf("active index not empty after transfer")
	}
	if completedJournalCount(t, parent) != 1 {
		t.Fatalf("terminal record not retained")
	}

	// A later recovery must not re-resume the completed transfer.
	if err := RecoverTaskHandoffs(parent); err != nil {
		t.Fatalf("recovery of terminal-only home: %v", err)
	}
	mustTransferNoOwner(t, parent, "TASK-1")
	agg := mustTransferOwner(t, captain, "TASK-1")
	if agg.Generation != 1 || agg.Definition.Owner != "captain:test-sm" {
		t.Fatalf("terminal transfer was re-resumed: %+v", agg)
	}
	if pendingJournalCount(t, parent) != 0 {
		t.Fatalf("terminal transfer re-entered active index")
	}
	if completedJournalCount(t, parent) != 1 {
		t.Fatalf("terminal record lost during later recovery")
	}
}

// TestHandoffConcurrentTransfersCompetingHandoffs runs two Handoffs of
// disjoint task sets concurrently on the same source home. Both must succeed
// through the single fenced handoff scope; the active index is empty at the
// end and both terminal records are retained.
func TestHandoffConcurrentTransfersCompetingHandoffs(t *testing.T) {
	parent, captain := seedHandoffPair(t)
	parentAuth := mustAuthority(t, parent)
	seedCanonicalQueuedTask(t, parentAuth, "TASK-1", "general")
	seedCanonicalQueuedTask(t, parentAuth, "TASK-2", "general")

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, tasks := range [][]string{{"TASK-1"}, {"TASK-2"}} {
		wg.Add(1)
		go func(i int, tasks []string) {
			defer wg.Done()
			errs[i] = Handoff(parent, captain, tasks)
		}(i, tasks)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Handoff %d: %v", i, err)
		}
	}
	for _, taskID := range []string{"TASK-1", "TASK-2"} {
		mustTransferNoOwner(t, parent, taskID)
		agg := mustTransferOwner(t, captain, taskID)
		if agg.Generation != 1 || agg.Definition.Owner != "captain:test-sm" {
			t.Fatalf("%s aggregate = %+v", taskID, agg)
		}
	}
	if pendingJournalCount(t, parent) != 0 {
		t.Fatalf("active index not empty after concurrent handoffs")
	}
	if completedJournalCount(t, parent) != 2 {
		t.Fatalf("terminal records after concurrent handoffs = %d, want 2", completedJournalCount(t, parent))
	}
}

// TestHandoffConcurrentSameTaskFailClosed runs two Handoffs of the SAME task
// concurrently. Exactly one wins the destination; the other fails closed with
// a destination-ownership conflict, and no contradictory dual ownership
// results.
func TestHandoffConcurrentSameTaskFailClosed(t *testing.T) {
	parent, captain := seedHandoffPair(t)
	seedCanonicalQueuedTask(t, mustAuthority(t, parent), "TASK-1", "general")

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = Handoff(parent, captain, []string{"TASK-1"})
		}(i)
	}
	wg.Wait()

	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
			continue
		}
		// The loser fails closed: it either observes the winner's completed
		// transfer (source no longer owns the task) or the winner's in-flight
		// destination claim (destination currently owns the task). Both are
		// typed handoff conflicts; no success path exposes dual ownership.
		msg := err.Error()
		if !strings.Contains(msg, "destination already has current authority") && !strings.Contains(msg, "not owned by the source home") && !strings.Contains(msg, "reading source task") {
			t.Fatalf("concurrent same-task error = %v, want handoff conflict", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent same-task successes = %d, want exactly 1", successes)
	}
	mustTransferNoOwner(t, parent, "TASK-1")
	agg := mustTransferOwner(t, captain, "TASK-1")
	if agg.Generation != 1 || agg.Definition.Owner != "captain:test-sm" {
		t.Fatalf("TASK-1 aggregate = %+v", agg)
	}
	if pendingJournalCount(t, parent) != 0 {
		t.Fatalf("active index not empty after concurrent same-task handoff")
	}
}

// TestHandoffJournalFencedStaleLock proves a Commit through a released (stale)
// handoff lock fails closed with Home's fenced error, and that an attempt to
// write a journal with a stale expected revision fails closed.
func TestHandoffJournalFencedStaleLock(t *testing.T) {
	parent := t.TempDir()
	seedCanonicalTransferHome(t, parent)
	h, err := mhome.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	journal := &taskHandoffJournal{
		Version: 1,
		ID:      "fenced-transfer",
		Phase:   handoffPhasePrepared,
	}

	// A released lock cannot be used for a commit.
	lk, err := h.Lock(handoffLockScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
	if err := writeHandoffJournal(h, lk, journal); !errors.Is(err, mhome.ErrFenced) {
		t.Fatalf("write with released lock = %v, want ErrFenced", err)
	}

	// A stale expected revision (index already advanced in a prior commit)
	// must fail closed through Home's optimistic concurrency gate.
	lk2, err := h.Lock(handoffLockScope)
	if err != nil {
		t.Fatal(err)
	}
	defer lk2.Release()
	idx := handoffJournalIndex{Version: handoffIndexVersion, HomeRevision: 1, Active: []string{journal.ID}}
	items, err := handoffJournalItems(idx, journal)
	if err != nil {
		t.Fatal(err)
	}
	// First commit succeeds: HomeRevision 0 -> 1 in the handoff scope.
	if _, err := h.Commit(lk2, handoffTxnID(journal.ID, "create"), 0, items); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	// Replaying the same transition with the now-stale expected revision 0
	// must conflict.
	if _, err := h.Commit(lk2, handoffTxnID(journal.ID, "create"), 0, items); !errors.Is(err, mhome.ErrConflict) {
		t.Fatalf("stale-revision commit = %v, want ErrConflict", err)
	}
}

// canonicalHandoffHomeOrDie canonicalizes a home path the way production
// does (durableTaskHandoff canonicalizes the parent before journaling).
func canonicalHandoffHomeOrDie(t *testing.T, path string) string {
	t.Helper()
	canonical, err := canonicalHandoffHome(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
