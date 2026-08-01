package taskauthorityfs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// testOp builds a valid operation with a deterministic sha256 digest derived
// from the operation id and seed.
func testOp(id, seed string) taskauthority.Operation {
	sum := sha256.Sum256([]byte("op:" + id + ":" + seed))
	return taskauthority.Operation{ID: id, Digest: hex.EncodeToString(sum[:])}
}

// createUpdate stages a first-generation create for taskID with one typed
// lifecycle audit event.
func createUpdate(opID, taskID string) func(tx *taskauthority.Tx) error {
	return func(tx *taskauthority.Tx) error {
		agg, err := taskauthority.NewAggregate(taskID, "owner", "work", "ship", "", "")
		if err != nil {
			return err
		}
		if err := tx.PutAggregate(agg); err != nil {
			return err
		}
		return tx.AppendAudit(taskauthority.AuditEvent{
			OperationID: opID,
			Actor:       taskauthority.Actor{ID: "general", Rank: "general"},
			Kind:        taskauthority.AuditLifecycle,
			TaskID:      taskID,
			Generation:  1,
			Reason:      "create",
			After:       taskauthority.PhaseQueued,
			At:          time.Now().UnixNano(),
		})
	}
}

// reopenUpdate stages the next generation of taskID as current and demotes
// the committed generation to historical.
func reopenUpdate(opID, taskID string) func(tx *taskauthority.Tx) error {
	return func(tx *taskauthority.Tx) error {
		cur, ok := tx.Current(taskID)
		if !ok {
			return errors.New(taskID + " missing")
		}
		old := cur
		old.Current = false
		old.Revision = cur.Revision + 1
		if err := tx.PutAggregate(old); err != nil {
			return err
		}
		newAgg, err := taskauthority.NewAggregate(taskID, "owner", "work", "ship", "", "")
		if err != nil {
			return err
		}
		newAgg.Generation = cur.Generation + 1
		if err := tx.PutAggregate(newAgg); err != nil {
			return err
		}
		return tx.AppendAudit(taskauthority.AuditEvent{
			OperationID: opID,
			Actor:       taskauthority.Actor{ID: "general", Rank: "general"},
			Kind:        taskauthority.AuditLifecycle,
			TaskID:      taskID,
			Generation:  cur.Generation + 1,
			Reason:      "reopen",
			Before:      cur.Phase,
			After:       taskauthority.PhaseQueued,
			At:          time.Now().UnixNano(),
		})
	}
}

// assertCommittedCreate asserts a store holds the fully committed first
// generation of taskID written by op: current aggregate at revision one,
// durable receipt, and lifecycle audit event.
func assertCommittedCreate(t *testing.T, s *Store, op taskauthority.Operation, taskID string) taskauthority.View {
	t.Helper()
	v := mustView(t, s)
	cur, ok := v.Current(taskID)
	if !ok {
		t.Fatalf("current aggregate for %s missing", taskID)
	}
	if cur.Generation != 1 || cur.Revision != 1 || !cur.Current || cur.Phase != taskauthority.PhaseQueued {
		t.Fatalf("current aggregate = %+v, want generation 1 revision 1 queued", cur)
	}
	receipt, ok := findReceipt(v.Receipts, op.ID)
	if !ok {
		t.Fatalf("receipt for %s missing", op.ID)
	}
	if receipt.Digest != op.Digest || receipt.TaskID != taskID || receipt.Generation != 1 || receipt.Revision != 1 || receipt.Phase != taskauthority.PhaseQueued || receipt.Replayed {
		t.Fatalf("receipt = %+v, want committed create outcome for %s", receipt, op.ID)
	}
	var auditFound bool
	for _, ev := range v.Audit {
		if ev.OperationID == op.ID && ev.Kind == taskauthority.AuditLifecycle && ev.TaskID == taskID {
			auditFound = true
		}
	}
	if !auditFound {
		t.Fatalf("lifecycle audit event for %s missing", op.ID)
	}
	return v
}

// assertNoManifests asserts no visible transaction manifest remains.
func assertNoManifests(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, filepath.FromSlash(transactionsDir))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		t.Fatalf("transaction manifest remains after recovery: %s", entry.Name())
	}
}

// readManifest loads and decodes the pending manifest for opID.
func readManifest(t *testing.T, home, opID string) TransactionManifest {
	t.Helper()
	rel := relPath(t, func() (string, error) { return TransactionManifestRelPath(opID) })
	data, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	m, err := DecodeTransactionManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func findManifestEntry(entries []ManifestEntry, path string) (ManifestEntry, bool) {
	for _, entry := range entries {
		if entry.Path == path {
			return entry, true
		}
	}
	return ManifestEntry{}, false
}

func TestUpdateCommitsTransaction(t *testing.T) {
	home := t.TempDir()
	op := testOp("op-create", "t1")
	receipt, err := openStore(t, home).Update(op, createUpdate("op-create", "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.OperationID != "op-create" || receipt.TaskID != "t1" || receipt.Generation != 1 ||
		receipt.Revision != 1 || receipt.Phase != taskauthority.PhaseQueued || receipt.Replayed {
		t.Fatalf("receipt = %+v, want committed create outcome", receipt)
	}
	if receipt.CommittedAt <= 0 {
		t.Fatalf("receipt committed at %d, want positive timestamp", receipt.CommittedAt)
	}

	assertCommittedCreate(t, openStore(t, home), op, "t1")
	assertNoManifests(t, home)

	// Documents and directories carry private modes.
	aggRel := relPath(t, func() (string, error) { return AggregateRelPath("t1", 1) })
	info, err := os.Stat(filepath.Join(home, filepath.FromSlash(aggRel)))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("aggregate document mode = %o, want 0600", got)
	}
	stateInfo, err := os.Stat(filepath.Join(home, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if got := stateInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("state dir mode = %o, want 0700", got)
	}

	// A task-bound transaction acquires the per-task lock alongside dispatch.
	if _, err := os.Stat(filepath.Join(home, "state", "t1.meta.lock")); err != nil {
		t.Errorf("task-bound transaction did not create the task lock: %v", err)
	}
}

func TestUpdateCallbackErrorNoTransaction(t *testing.T) {
	home := t.TempDir()
	s := openStore(t, home)
	_, err := s.Update(testOp("op-cb", "t1"), func(tx *taskauthority.Tx) error {
		agg, err := taskauthority.NewAggregate("t1", "owner", "work", "ship", "", "")
		if err != nil {
			return err
		}
		if err := tx.PutAggregate(agg); err != nil {
			return err
		}
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("expected callback error")
	}
	v := mustView(t, openStore(t, home))
	if len(v.Aggregates) != 0 || len(v.Receipts) != 0 || len(v.Audit) != 0 {
		t.Fatalf("callback error must not mutate state: %+v", v)
	}
	assertNoManifests(t, home)
}

func TestUpdateValidationErrorNoTransaction(t *testing.T) {
	home := t.TempDir()
	s := openStore(t, home)
	_, err := s.Update(testOp("op-val", "t1"), func(tx *taskauthority.Tx) error {
		agg, err := taskauthority.NewAggregate("t1", "owner", "work", "ship", "", "")
		if err != nil {
			return err
		}
		agg.Revision = 2 // a new generation must start at FirstRevision
		return tx.PutAggregate(agg)
	})
	if !errors.Is(err, taskauthority.ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	v := mustView(t, openStore(t, home))
	if len(v.Aggregates) != 0 || len(v.Receipts) != 0 {
		t.Fatalf("validation error must not mutate state: %+v", v)
	}
	assertNoManifests(t, home)
}

func TestTransactionReplayAcrossReopen(t *testing.T) {
	home := t.TempDir()
	op := testOp("op-replay", "t1")
	first, err := openStore(t, home).Update(op, createUpdate("op-replay", "t1"))
	if err != nil {
		t.Fatal(err)
	}

	// A fresh Store instance replays from the durable receipt without running
	// the callback.
	s2 := openStore(t, home)
	runs := 0
	second, err := s2.Update(op, func(tx *taskauthority.Tx) error {
		runs++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Fatalf("callback ran %d times on replay, want 0", runs)
	}
	if second.OperationID != first.OperationID || second.Digest != first.Digest || second.CommittedAt != first.CommittedAt {
		t.Fatalf("replay receipt identity differs: %+v vs %+v", first, second)
	}
	if first.Replayed || !second.Replayed {
		t.Fatalf("replayed flags = %v/%v, want false/true", first.Replayed, second.Replayed)
	}

	// Reusing the id with a different intent conflicts.
	other := op
	other.Digest = strings.Repeat("b", 64)
	_, err = s2.Update(other, func(tx *taskauthority.Tx) error { return nil })
	if !errors.Is(err, taskauthority.ErrOperationConflict) {
		t.Fatalf("conflicting digest error = %v, want ErrOperationConflict", err)
	}
}

func TestUpdateHoldOnlyTransactionDispatchLockOnly(t *testing.T) {
	home := t.TempDir()
	s := openStore(t, home)
	hold := fixtureHold()
	_, err := s.Update(testOp("op-hold", ""), func(tx *taskauthority.Tx) error {
		return tx.PutHold(hold)
	})
	if err != nil {
		t.Fatal(err)
	}
	v := mustView(t, s)
	got, ok := v.Hold("hold-1")
	if !ok || got.ReleasedAt != 0 {
		t.Fatalf("hold = %+v", got)
	}
	if len(v.Receipts) != 1 || len(v.Aggregates) != 0 {
		t.Fatalf("hold-only transaction wrote unexpected records: %+v", v)
	}
	// No task identity was staged, so the dispatch lock alone is valid and no
	// per-task lock file is created.
	if _, err := os.Stat(filepath.Join(home, "state", "t1.meta.lock")); !os.IsNotExist(err) {
		t.Fatalf("hold-only transaction created a task lock (stat err = %v)", err)
	}
}

// TestUpdateTaskBoundBlocksOnTaskLock proves a task-bound Update acquires
// state/<taskID>.meta.lock while the dispatch lock remains held: while the
// test holds the task lock, the Update must not complete.
func TestUpdateTaskBoundTransactionBlocksOnTaskLock(t *testing.T) {
	home := t.TempDir()
	s := openStore(t, home)

	taskPath, err := taskLockPath(home, "t1")
	if err != nil {
		t.Fatal(err)
	}
	taskFile, err := lockFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := s.Update(testOp("op-lock", "t1"), createUpdate("op-lock", "t1"))
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("Update completed while the task lock was held (err=%v)", err)
	case <-time.After(300 * time.Millisecond):
	}

	releaseLock(taskFile)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Update after task lock release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Update never completed after the task lock was released")
	}
	assertCommittedCreate(t, openStore(t, home), testOp("op-lock", "t1"), "t1")
}

func TestUpdateRejectsMultiTaskTransaction(t *testing.T) {
	home := t.TempDir()
	s := openStore(t, home)
	_, err := s.Update(testOp("op-multi", "t1"), func(tx *taskauthority.Tx) error {
		agg1, err := taskauthority.NewAggregate("t1", "owner", "work", "ship", "", "")
		if err != nil {
			return err
		}
		if err := tx.PutAggregate(agg1); err != nil {
			return err
		}
		agg2, err := taskauthority.NewAggregate("t2", "owner", "work", "ship", "", "")
		if err != nil {
			return err
		}
		return tx.PutAggregate(agg2)
	})
	if err == nil {
		t.Fatal("expected multi-task rejection")
	}
	if !strings.Contains(err.Error(), "multi-task") {
		t.Fatalf("error = %v, want a clear multi-task message", err)
	}
	v := mustView(t, openStore(t, home))
	if len(v.Aggregates) != 0 || len(v.Receipts) != 0 {
		t.Fatalf("multi-task rejection must not mutate state: %+v", v)
	}
	assertNoManifests(t, home)
}

func TestUpdateRejectsMalformedDigestTransaction(t *testing.T) {
	home := t.TempDir()
	s := openStore(t, home)
	op := testOp("op-hex", "t1")
	op.Digest = strings.Repeat("z", 64) // valid length, invalid hex
	runs := 0
	_, err := s.Update(op, func(tx *taskauthority.Tx) error {
		runs++
		return nil
	})
	if !errors.Is(err, taskauthority.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
	if runs != 0 {
		t.Fatalf("callback ran %d times for a malformed digest, want 0", runs)
	}
	v := mustView(t, openStore(t, home))
	if len(v.Receipts) != 0 {
		t.Fatalf("malformed digest must not commit")
	}
}

func TestUpdateRejectsUnsafeOperationTransaction(t *testing.T) {
	home := t.TempDir()
	s := openStore(t, home)
	runs := 0
	_, err := s.Update(taskauthority.Operation{ID: "../escape", Digest: strings.Repeat("a", 64)}, func(tx *taskauthority.Tx) error {
		runs++
		return nil
	})
	if err == nil {
		t.Fatal("expected validation error for unsafe operation id")
	}
	if runs != 0 {
		t.Fatalf("callback ran %d times for an unsafe operation id, want 0", runs)
	}
}

func TestUpdateRejectsV1HomeTransaction(t *testing.T) {
	home := t.TempDir()
	writeDocAt(t, home, "state/.task-authority/aggregates/t1/1.json", []byte(`{"schema_version":"munsu.task-aggregate/v1"}`))
	_, err := openStore(t, home).Update(testOp("op-v1", "t1"), func(tx *taskauthority.Tx) error { return nil })
	if !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("error = %v, want ErrMigrationRequired", err)
	}
}

func TestUpdateTransactionManifestContent(t *testing.T) {
	home := t.TempDir()
	s := openStore(t, home)
	op := testOp("op-m", "t1")
	s.fault = &faultInjector{stage: faultStageAfterManifest, err: errInjected}
	if _, err := s.Update(op, createUpdate("op-m", "t1")); !errors.Is(err, errInjected) {
		t.Fatalf("Update error = %v, want injected crash after pending manifest", err)
	}

	m := readManifest(t, home, "op-m")
	if m.OperationID != "op-m" || m.Digest != op.Digest || m.State != ManifestPending {
		t.Fatalf("manifest identity/state = %+v", m)
	}
	if m.ExpectedGeneration != 1 {
		t.Fatalf("ExpectedGeneration = %d, want 1", m.ExpectedGeneration)
	}
	if m.Audit == nil || m.Audit.OperationID != "op-m" || m.Audit.Kind != taskauthority.AuditLifecycle {
		t.Fatalf("manifest audit = %+v", m.Audit)
	}
	if len(m.Before) != 0 {
		t.Fatalf("create manifest must pin no before state: %+v", m.Before)
	}

	want := []string{
		relPath(t, func() (string, error) { return AggregateRelPath("t1", 1) }),
		relPath(t, func() (string, error) { return CurrentPointerRelPath("t1") }),
		relPath(t, func() (string, error) { return AuditRelPath("op-m") }),
		relPath(t, func() (string, error) { return ReceiptRelPath("op-m") }),
	}
	sort.Strings(want)
	got := make([]string, 0, len(m.After))
	for _, entry := range m.After {
		got = append(got, entry.Path)
		if DigestHex([]byte(entry.Payload)) != entry.Digest {
			t.Fatalf("after entry %s payload does not match its digest", entry.Path)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after entry paths = %v, want %v", got, want)
	}

	receiptEntry, ok := findManifestEntry(m.After, relPath(t, func() (string, error) { return ReceiptRelPath("op-m") }))
	if !ok {
		t.Fatal("receipt after entry missing from manifest")
	}
	decoded, err := DecodeReceipt([]byte(receiptEntry.Payload))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.OperationID != "op-m" || decoded.Digest != op.Digest || decoded.TaskID != "t1" || decoded.Generation != 1 {
		t.Fatalf("manifest receipt entry = %+v", decoded)
	}
}

func TestUpdateTransactionManifestReopenedGeneration(t *testing.T) {
	home := t.TempDir()
	s := openStore(t, home)
	if _, err := s.Update(testOp("op-gen1", "t1"), createUpdate("op-gen1", "t1")); err != nil {
		t.Fatal(err)
	}

	op := testOp("op-reopen", "t1")
	s2 := openStore(t, home)
	s2.fault = &faultInjector{stage: faultStageAfterManifest, err: errInjected}
	if _, err := s2.Update(op, reopenUpdate("op-reopen", "t1")); !errors.Is(err, errInjected) {
		t.Fatalf("Update error = %v, want injected crash after pending manifest", err)
	}

	m := readManifest(t, home, "op-reopen")
	if m.ExpectedGeneration != 2 {
		t.Fatalf("ExpectedGeneration = %d, want 2", m.ExpectedGeneration)
	}
	gen1 := relPath(t, func() (string, error) { return AggregateRelPath("t1", 1) })
	gen2 := relPath(t, func() (string, error) { return AggregateRelPath("t1", 2) })
	ptr := relPath(t, func() (string, error) { return CurrentPointerRelPath("t1") })

	beforePaths := map[string]bool{}
	for _, entry := range m.Before {
		beforePaths[entry.Path] = true
	}
	if !beforePaths[gen1] || !beforePaths[ptr] {
		t.Fatalf("before entries must pin the old generation doc and pointer: %+v", m.Before)
	}

	afterPaths := map[string]bool{}
	for _, entry := range m.After {
		afterPaths[entry.Path] = true
	}
	for _, want := range []string{gen1, gen2, ptr, relPath(t, func() (string, error) { return ReceiptRelPath("op-reopen") }), relPath(t, func() (string, error) { return AuditRelPath("op-reopen") })} {
		if !afterPaths[want] {
			t.Fatalf("after entries must include %s: %+v", want, m.After)
		}
	}

	// The gen1 replacement payload demotes the committed generation.
	gen1Entry, ok := findManifestEntry(m.After, gen1)
	if !ok {
		t.Fatal("gen1 after entry missing")
	}
	demoted, err := DecodeAggregate([]byte(gen1Entry.Payload))
	if err != nil {
		t.Fatal(err)
	}
	if demoted.Current || demoted.Revision != 2 {
		t.Fatalf("gen1 replacement = %+v, want historical revision 2", demoted)
	}
}

func TestUpdateTransactionManifestDeterministic(t *testing.T) {
	var first []string
	for i := 0; i < 2; i++ {
		home := t.TempDir()
		s := openStore(t, home)
		s.fault = &faultInjector{stage: faultStageAfterManifest, err: errInjected}
		if _, err := s.Update(testOp("op-det", "t1"), createUpdate("op-det", "t1")); !errors.Is(err, errInjected) {
			t.Fatalf("Update error = %v, want injected crash", err)
		}
		m := readManifest(t, home, "op-det")
		paths := make([]string, 0, len(m.After))
		for _, entry := range m.After {
			paths = append(paths, entry.Path)
		}
		if i == 0 {
			first = paths
			continue
		}
		if !reflect.DeepEqual(first, paths) {
			t.Fatalf("manifest after entry order differs between identical transactions: %v vs %v", first, paths)
		}
	}
}
