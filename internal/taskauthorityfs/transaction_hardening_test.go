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

// authorityRootRelDirs is the chain of authority-root components the write
// path materializes from the trust boundary (homeDir). A symlink at any of
// them must fail closed instead of redirecting authority writes outside the
// home.
var authorityRootRelDirs = []string{
	"state",
	"state/.task-authority",
	"state/.task-authority/v2",
}

// assertDirEmpty fails when dir contains any entry. Used to prove an outside
// directory was never touched by an authority write.
func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory %s was modified by the authority path: %v", dir, names)
	}
}

// TestWritePathRejectsSymlinkedRootComponents proves the authority write path
// never follows a symlink at any root component (state, .task-authority, v2):
// Update fails closed and no file of any kind is created or modified outside
// the home.
func TestWritePathRejectsSymlinkedRootComponents(t *testing.T) {
	for _, rel := range authorityRootRelDirs {
		t.Run("symlink at "+rel, func(t *testing.T) {
			home := t.TempDir()
			outside := t.TempDir()
			parent := filepath.Dir(filepath.Join(home, filepath.FromSlash(rel)))
			if err := os.MkdirAll(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(home, filepath.FromSlash(rel))); err != nil {
				t.Fatal(err)
			}

			_, err := openStore(t, home).Update(testOp("op-link", "t1"), createUpdate("op-link", "t1"))
			if err == nil {
				t.Fatalf("Update succeeded through symlinked %s, want fail closed", rel)
			}
			assertDirEmpty(t, outside)
		})
	}
}

// TestTrustBoundaryIsHomeDir pins the explicit trust boundary: homeDir itself
// is the caller-named boundary and may be (or pass through) an OS-resolved
// symlink — macOS /tmp and /var make rejecting that impossible — while every
// component below it must stay link-checked.
func TestTrustBoundaryIsHomeDir(t *testing.T) {
	t.Run("symlinked homeDir is the boundary", func(t *testing.T) {
		target := t.TempDir()
		home := filepath.Join(t.TempDir(), "home")
		if err := os.Symlink(target, home); err != nil {
			t.Fatal(err)
		}
		s := openStore(t, home)
		op := testOp("op-boundary", "t1")
		if _, err := s.Update(op, createUpdate("op-boundary", "t1")); err != nil {
			t.Fatalf("Update through symlinked homeDir: %v", err)
		}
		// The documents physically land inside the symlink target.
		for _, rel := range []string{
			"state/.task-authority/v2/aggregates/t1/1.json",
			"state/.task-authority/v2/aggregates/t1/current",
		} {
			if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(rel))); err != nil {
				t.Fatalf("document not written under the home target at %s: %v", rel, err)
			}
		}
		assertCommittedCreate(t, openStore(t, home), op, "t1")
	})

	t.Run("components below the boundary stay link-checked", func(t *testing.T) {
		home := t.TempDir()
		outside := t.TempDir()
		s := openStore(t, home)
		if _, err := s.Update(testOp("op-a", "t1"), createUpdate("op-a", "t1")); err != nil {
			t.Fatal(err)
		}
		// The boundary does not extend below homeDir: replacing the now-real
		// state with a link must fail closed and touch nothing outside.
		if err := os.RemoveAll(filepath.Join(home, "state")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(home, "state")); err != nil {
			t.Fatal(err)
		}
		_, err := s.Update(testOp("op-b", "t2"), createUpdate("op-b", "t2"))
		if err == nil {
			t.Fatal("Update succeeded through a symlinked state below the boundary")
		}
		assertDirEmpty(t, outside)
	})
}

// TestRecoveryRejectsSymlinkedTransactionsDir proves recovery never reads
// manifests through a symlinked transactions directory: an outside manifest
// must not be applied as this home's own, and recovery fails closed.
func TestRecoveryRejectsSymlinkedTransactionsDir(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()

	// A real pending manifest lives outside, reachable only through the
	// symlinked transactions directory.
	m := capturePendingManifest(t, "op-outside", "t1")
	data, err := EncodeTransactionManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	txns := filepath.Join(outside, "transactions")
	if err := os.MkdirAll(txns, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txns, fileIDEncode(m.OperationID)+documentExt), data, 0o600); err != nil {
		t.Fatal(err)
	}

	abs := filepath.Join(home, "state", ".task-authority", "v2", "transactions")
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(txns, abs); err != nil {
		t.Fatal(err)
	}

	_, err = openStore(t, home).View()
	if !errors.Is(err, ErrCorruptDocument) {
		t.Fatalf("View error = %v, want ErrCorruptDocument", err)
	}
	// The outside manifest was neither applied into the home nor removed.
	if _, err := os.Stat(filepath.Join(txns, fileIDEncode(m.OperationID)+documentExt)); err != nil {
		t.Fatalf("outside manifest was touched during recovery: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "state", ".task-authority", "v2", "aggregates", "t1", "1.json")); !os.IsNotExist(err) {
		t.Fatalf("recovery applied an outside manifest into the home (stat err = %v)", err)
	}
}

// TestRecoveryRejectsSymlinkedManifestFile proves the manifest file itself is
// never read through a link: a symlinked manifest entry is corruption before
// any recovery can act on it.
func TestRecoveryRejectsSymlinkedManifestFile(t *testing.T) {
	home := t.TempDir()
	m := capturePendingManifest(t, "op-mlink", "t1")
	writeManifestDoc(t, home, m)

	// Replace the manifest file with a link to a valid manifest outside.
	outside := filepath.Join(t.TempDir(), "manifest.json")
	data, err := EncodeTransactionManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, data, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestAbs := filepath.Join(home, "state", ".task-authority", "v2", "transactions", fileIDEncode(m.OperationID)+documentExt)
	if err := os.Remove(manifestAbs); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, manifestAbs); err != nil {
		t.Fatal(err)
	}

	_, err = openStore(t, home).View()
	if !errors.Is(err, ErrCorruptDocument) {
		t.Fatalf("View error = %v, want ErrCorruptDocument", err)
	}
}

// TestCommittedMarkerRecoveryIsIdempotent proves recovery of a manifest whose
// commit marker is already durable (crash between the marker and manifest
// retirement) converges to the committed view, retires the manifest, replays
// the receipt, and is a no-op on the second pass.
func TestCommittedMarkerRecoveryIsIdempotent(t *testing.T) {
	home := t.TempDir()
	op := testOp("op-committed", "t1")
	m := capturePendingManifest(t, op.ID, "t1")
	m.State = ManifestCommitted
	writeManifestDoc(t, home, m)

	s := openStore(t, home)
	v1 := mustView(t, s)
	assertCommittedCreate(t, s, op, "t1")
	assertNoManifests(t, home)

	// Idempotent: a second canonical read observes identical state and the
	// durable receipt replays without running the callback.
	v2 := mustView(t, s)
	if !reflect.DeepEqual(v1, v2) {
		t.Fatal("repeated views after committed-marker recovery differ")
	}
	runs := 0
	replay, err := s.Update(op, func(tx *taskauthority.Tx) error {
		runs++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if runs != 0 || !replay.Replayed {
		t.Fatalf("replay runs=%d Replayed=%v, want 0/true", runs, replay.Replayed)
	}
	if replay.CommittedAt <= 0 {
		t.Fatalf("replayed receipt lost its committed timestamp: %+v", replay)
	}
}

// TestReplayReceiptSurvivesReopen proves every fresh Store instance replays
// from the durable receipt without running the callback, preserving the
// original committed timestamp and intent digest across reopens.
func TestReplayReceiptSurvivesReopen(t *testing.T) {
	home := t.TempDir()
	op := testOp("op-reopen-replay", "t1")
	first, err := openStore(t, home).Update(op, createUpdate("op-reopen-replay", "t1"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s := openStore(t, home)
		runs := 0
		replay, err := s.Update(op, func(tx *taskauthority.Tx) error {
			runs++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if runs != 0 || !replay.Replayed {
			t.Fatalf("replay %d runs=%d Replayed=%v, want 0/true", i, runs, replay.Replayed)
		}
		if replay.CommittedAt != first.CommittedAt || replay.Digest != op.Digest || replay.OperationID != op.ID {
			t.Fatalf("replay %d receipt = %+v, want identity of %+v", i, replay, first)
		}
	}
}

// TestUpdateFailureLeavesNoTrace proves every update failure mode — callback
// error, transaction validation error, malformed intent digest, unsafe
// operation identity, and multi-task staging — leaves no manifest, no
// authority document, and no half-written temp file anywhere in the home.
func TestUpdateFailureLeavesNoTrace(t *testing.T) {
	run := func(name string, update func(*Store) error) {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			s := openStore(t, home)
			if err := update(s); err == nil {
				t.Fatal("expected update failure")
			}
			v := mustView(t, s)
			if len(v.Aggregates) != 0 || len(v.Holds) != 0 || len(v.Interpretations) != 0 ||
				len(v.Decisions) != 0 || len(v.Receipts) != 0 || len(v.Audit) != 0 {
				t.Fatalf("failed update left records: %+v", v)
			}
			assertNoManifests(t, home)
			var tempFiles []string
			_ = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() && strings.HasPrefix(d.Name(), ".txn-") {
					tempFiles = append(tempFiles, path)
				}
				return nil
			})
			if len(tempFiles) != 0 {
				t.Fatalf("failed update left temp files: %v", tempFiles)
			}
		})
	}

	run("callback error", func(s *Store) error {
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
		return err
	})

	run("validation error", func(s *Store) error {
		_, err := s.Update(testOp("op-val", "t1"), func(tx *taskauthority.Tx) error {
			agg, err := taskauthority.NewAggregate("t1", "owner", "work", "ship", "", "")
			if err != nil {
				return err
			}
			agg.Revision = 2 // a new generation must start at FirstRevision
			return tx.PutAggregate(agg)
		})
		return err
	})

	run("malformed digest short hex", func(s *Store) error {
		op := testOp("op-hex", "t1")
		op.Digest = strings.Repeat("a", 63)
		_, err := s.Update(op, func(tx *taskauthority.Tx) error { return nil })
		return err
	})

	run("malformed digest non-hex", func(s *Store) error {
		op := testOp("op-hex", "t1")
		op.Digest = strings.Repeat("z", 64)
		_, err := s.Update(op, func(tx *taskauthority.Tx) error { return nil })
		return err
	})

	run("unsafe operation id", func(s *Store) error {
		_, err := s.Update(taskauthority.Operation{ID: "../escape", Digest: strings.Repeat("a", 64)}, func(tx *taskauthority.Tx) error {
			return nil
		})
		return err
	})

	run("multi-task staging", func(s *Store) error {
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
		return err
	})
}
