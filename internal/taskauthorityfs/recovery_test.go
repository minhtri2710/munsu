package taskauthorityfs

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// errInjected is the deterministic failure returned by fault hooks to
// simulate a process crash mid-transaction.
var errInjected = errors.New("injected crash")

// TestCrashRecoveryConvergesEveryStage proves recovery idempotence at every
// journal stage: a crash before the pending manifest leaves no mutation, and
// a crash at every later stage converges to the fully committed view and
// receipt when a fresh Store reads the home. Recovery must be a no-op on the
// second read, and replay must return the original receipt without running
// the callback.
func TestCrashRecoveryConvergesEveryStage(t *testing.T) {
	cases := []struct {
		name       string
		stage      faultStage
		afterWrite int
	}{
		{"before manifest", faultStageBeforeManifest, 0},
		{"after pending manifest", faultStageAfterManifest, 0},
		{"after first data write", faultStageAfterDataWrite, 1},
		{"after second data write", faultStageAfterDataWrite, 2},
		{"after third data write", faultStageAfterDataWrite, 3},
		{"after fourth data write", faultStageAfterDataWrite, 4},
		{"after fifth data write", faultStageAfterDataWrite, 5},
		{"after sixth data write", faultStageAfterDataWrite, 6},
		{"before commit marker", faultStageBeforeCommit, 0},
		{"after commit marker", faultStageAfterCommit, 0},
		{"during cleanup", faultStageDuringCleanup, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			op := testOp("op-crash", "t1")
			s := openStore(t, home)
			s.fault = &faultInjector{stage: tc.stage, afterWrite: tc.afterWrite, err: errInjected}
			_, err := s.Update(op, createUpdate("op-crash", "t1"))
			if err == nil {
				// The fault never fired (a data-write index beyond the entry
				// count); the transaction committed normally.
				assertCommittedCreate(t, openStore(t, home), op, "t1")
				return
			}
			if !errors.Is(err, errInjected) {
				t.Fatalf("Update error = %v, want injected crash", err)
			}

			// A fresh Store must recover before any canonical read.
			s2 := openStore(t, home)
			if tc.stage == faultStageBeforeManifest {
				v := mustView(t, s2)
				if len(v.Aggregates) != 0 || len(v.Receipts) != 0 || len(v.Audit) != 0 {
					t.Fatalf("before-manifest crash must leave no mutation: %+v", v)
				}
			} else {
				assertCommittedCreate(t, s2, op, "t1")
			}
			assertNoManifests(t, home)

			// Recovery is idempotent: the second view sees identical state.
			first := mustView(t, s2)
			second := mustView(t, s2)
			if !reflect.DeepEqual(first, second) {
				t.Fatal("repeated views after recovery differ")
			}

			// The durable receipt replays without running the callback.
			if tc.stage == faultStageBeforeManifest {
				return // nothing committed, nothing to replay
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
		})
	}
}

// capturePendingManifest runs one create transaction with a crash immediately
// after the pending manifest is durable and returns the captured manifest.
func capturePendingManifest(t *testing.T, opID, taskID string) TransactionManifest {
	t.Helper()
	home := t.TempDir()
	s := openStore(t, home)
	s.fault = &faultInjector{stage: faultStageAfterManifest, err: errInjected}
	if _, err := s.Update(testOp(opID, taskID), createUpdate(opID, taskID)); !errors.Is(err, errInjected) {
		t.Fatalf("Update error = %v, want injected crash", err)
	}
	return readManifest(t, home, opID)
}

// TestRecoveryMultipleManifests proves recovery processes every manifest in
// deterministic order and converges each to its committed view and receipt.
func TestRecoveryMultipleManifests(t *testing.T) {
	m1 := capturePendingManifest(t, "op-a", "t1")
	m2 := capturePendingManifest(t, "op-b", "t2")
	home := t.TempDir()
	writeManifestDoc(t, home, m1)
	writeManifestDoc(t, home, m2)

	s := openStore(t, home)
	v := mustView(t, s)
	if _, ok := v.Current("t1"); !ok {
		t.Fatal("t1 not committed after multi-manifest recovery")
	}
	if _, ok := v.Current("t2"); !ok {
		t.Fatal("t2 not committed after multi-manifest recovery")
	}
	for _, id := range []string{"op-a", "op-b"} {
		if _, ok := findReceipt(v.Receipts, id); !ok {
			t.Fatalf("receipt %s missing after multi-manifest recovery", id)
		}
	}
	assertNoManifests(t, home)
}

// TestUpdateRecoversBeforeApplying proves a new Update completes any
// interrupted transaction before committing its own.
func TestUpdateRecoveryBeforeApplying(t *testing.T) {
	home := t.TempDir()
	s := openStore(t, home)
	s.fault = &faultInjector{stage: faultStageAfterDataWrite, afterWrite: 1, err: errInjected}
	if _, err := s.Update(testOp("op-crash", "t1"), createUpdate("op-crash", "t1")); !errors.Is(err, errInjected) {
		t.Fatalf("Update error = %v, want injected crash", err)
	}

	s2 := openStore(t, home)
	if _, err := s2.Update(testOp("op-next", "t2"), createUpdate("op-next", "t2")); err != nil {
		t.Fatal(err)
	}
	assertCommittedCreate(t, s2, testOp("op-crash", "t1"), "t1")
	assertCommittedCreate(t, s2, testOp("op-next", "t2"), "t2")
	assertNoManifests(t, home)
}

// TestRecoveryFailsClosedOnDivergence proves recovery fails closed when the
// data files no longer match the manifest's pinned pre-state: the home stays
// unreadable until a human repairs it.
func TestRecoveryFailsClosedOnDivergence(t *testing.T) {
	t.Run("before digest no longer matches", func(t *testing.T) {
		home := t.TempDir()
		s := openStore(t, home)
		if _, err := s.Update(testOp("op-gen1", "t1"), createUpdate("op-gen1", "t1")); err != nil {
			t.Fatal(err)
		}
		s.fault = &faultInjector{stage: faultStageAfterManifest, err: errInjected}
		if _, err := s.Update(testOp("op-reopen", "t1"), reopenUpdate("op-reopen", "t1")); !errors.Is(err, errInjected) {
			t.Fatalf("Update error = %v, want injected crash", err)
		}
		m := readManifest(t, home, "op-reopen")
		if len(m.Before) == 0 {
			t.Fatal("reopen manifest must pin before state")
		}
		m.Before[0].Digest = strings.Repeat("f", 64) // valid hex, wrong pre-state
		writeManifestDoc(t, home, m)

		_, err := openStore(t, home).View()
		if !errors.Is(err, ErrRecoveryRequired) {
			t.Fatalf("View error = %v, want ErrRecoveryRequired", err)
		}
		var re *RecoveryRequiredError
		if !errors.As(err, &re) {
			t.Fatalf("View error %v is not a *RecoveryRequiredError", err)
		}
		if re.ManifestPath == "" || re.Reason == "" {
			t.Fatalf("RecoveryRequiredError = %+v, want manifest path and reason", re)
		}
	})

	t.Run("unexplained file without a before pin", func(t *testing.T) {
		home := t.TempDir()
		s := openStore(t, home)
		s.fault = &faultInjector{stage: faultStageAfterManifest, err: errInjected}
		if _, err := s.Update(testOp("op-create", "t1"), createUpdate("op-create", "t1")); !errors.Is(err, errInjected) {
			t.Fatalf("Update error = %v, want injected crash", err)
		}
		// A create manifest pins no before state; a foreign file at the
		// aggregate path matches neither the after digest nor any before pin.
		agg, err := taskauthority.NewAggregate("t1", "other-owner", "x", "y", "", "")
		if err != nil {
			t.Fatal(err)
		}
		data, err := EncodeAggregate(agg)
		if err != nil {
			t.Fatal(err)
		}
		writeDocAt(t, home, "state/.task-authority/v2/aggregates/t1/1.json", data)

		_, err = openStore(t, home).View()
		if !errors.Is(err, ErrRecoveryRequired) {
			t.Fatalf("View error = %v, want ErrRecoveryRequired", err)
		}
	})

	t.Run("symlinked document fails closed", func(t *testing.T) {
		home := t.TempDir()
		s := openStore(t, home)
		s.fault = &faultInjector{stage: faultStageAfterManifest, err: errInjected}
		if _, err := s.Update(testOp("op-create", "t1"), createUpdate("op-create", "t1")); !errors.Is(err, errInjected) {
			t.Fatalf("Update error = %v, want injected crash", err)
		}
		// Point the aggregate path at a symlink; recovery must reject it
		// instead of reading through it.
		outside := filepath.Join(t.TempDir(), "1.json")
		agg, err := taskauthority.NewAggregate("t1", "owner", "work", "ship", "", "")
		if err != nil {
			t.Fatal(err)
		}
		data, err := EncodeAggregate(agg)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(outside, data, 0o600); err != nil {
			t.Fatal(err)
		}
		abs := filepath.Join(home, "state", ".task-authority", "v2", "aggregates", "t1")
		if err := os.MkdirAll(abs, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(abs, "1.json")); err != nil {
			t.Fatal(err)
		}

		_, err = openStore(t, home).View()
		if !errors.Is(err, ErrRecoveryRequired) && !errors.Is(err, ErrCorruptDocument) {
			t.Fatalf("View error = %v, want recovery failure (symlink rejected)", err)
		}
	})
}

// TestRecoveryRejectsSymlinkedParent proves the write path never follows a
// symlinked directory component when materializing documents during recovery.
func TestRecoveryRejectsSymlinkedParent(t *testing.T) {
	home := t.TempDir()
	m := capturePendingManifest(t, "op-link", "t1")
	// Replace the aggregates directory with a symlink to a directory outside
	// the home; recovery must fail closed instead of writing through it.
	outside := filepath.Join(t.TempDir(), "aggregates")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(home, "state", ".task-authority", "v2")
	if err := os.MkdirAll(abs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(abs, "aggregates")); err != nil {
		t.Fatal(err)
	}
	writeManifestDoc(t, home, m)

	if _, err := openStore(t, home).View(); err == nil {
		t.Fatal("View succeeded through a symlinked parent, want fail closed")
	}
}
