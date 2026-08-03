package taskauthorityfs

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

// fullV1Home writes one representative v1 home with every source family: a
// task aggregate (current + historical), a matching worktree lease, and one
// record from each dispatch-control family.
func fullV1Home(t *testing.T, home string) {
	t.Helper()
	writeV1Agg(t, home, "t1", "1", true, map[string]any{"definition": "one", "state": "working"})
	writeV1Agg(t, home, "t1", "2", false, map[string]any{"state": "done", "worktree": v1WorktreeBinding("2", "lease-2", "tok-2")})
	writeV1Lease(t, home, "t1", "2", "lease-2", "tok-2")
	writeV1Hold(t, home, "hold-1", "dispatch decision required", []string{"start"})
	writeV1Interpretation(t, home, "interpretation-1")
	writeV1Decision(t, home, "decision-1", "interpretation-1")
}

func readMigrationJournalForTest(t *testing.T, home string) *migrationJournal {
	t.Helper()
	journal, ok, err := readMigrationJournal(home)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return nil
	}
	return journal
}

// verifyMigratedHome asserts the migrated home serves exactly the plan
// targets through the v2 store and carries a durable receipt.
func verifyMigratedHome(t *testing.T, home string, plan *MigrationPlan) {
	t.Helper()
	store, err := NewStore(home)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.View()
	if err != nil {
		t.Fatalf("View after migration: %v", err)
	}
	if !reflect.DeepEqual(view.Aggregates, plan.Aggregates) {
		t.Fatalf("view aggregates != plan aggregates:\n%+v\n%+v", view.Aggregates, plan.Aggregates)
	}
	if !reflect.DeepEqual(view.Holds, plan.Holds) || !reflect.DeepEqual(view.Interpretations, plan.Interpretations) || !reflect.DeepEqual(view.Decisions, plan.Decisions) {
		t.Fatalf("view dispatch records != plan dispatch records")
	}
	if _, err := os.Stat(filepath.Join(home, "state", ".task-authority-migration", "receipt.json")); err != nil {
		t.Fatalf("no durable receipt after migration: %v", err)
	}
}

// TestMigrationApplyResumesAfterPartialArchive proves the archive phase is
// recoverable from a crash after every individual rename: the archive intent
// is durable before the first move, and the retry discovers the archive,
// verifies the union of remaining sources plus already-archived files equals
// the plan inventory, continues the remaining moves idempotently, and
// completes. It must never require the full source set to remain in place
// after the first move.
func TestMigrationApplyResumesAfterPartialArchive(t *testing.T) {
	crashes := []string{
		"archive-move-aggregates",
		"archive-move-worktree-leases",
		"archive-move-dispatch",
	}
	for _, crash := range crashes {
		t.Run(crash, func(t *testing.T) {
			home := t.TempDir()
			fullV1Home(t, home)
			plan := planHome(t, home)
			sourceCount := len(plan.Sources)

			migrationCrashAfter = crash
			_, err := ApplyMigration(plan)
			migrationCrashAfter = ""
			if err == nil {
				t.Fatalf("crash injection %s did not fail apply", crash)
			}

			// The archive intent must be durable before the first rename.
			journal := readMigrationJournalForTest(t, home)
			if journal == nil {
				t.Fatal("no journal after interrupted archive")
			}
			if journal.ArchivePath == "" {
				t.Fatal("journal does not persist the archive path before the first rename")
			}
			if journal.Stage != journalStageArchiving {
				t.Fatalf("journal stage = %q, want %q", journal.Stage, journalStageArchiving)
			}
			// Some sources are already archived; only the final move leaves
			// nothing behind. Recovery must not demand the whole source set in
			// place after the first move.
			if crash != "archive-move-dispatch" && !v1SourcesRemain(home) {
				t.Fatal("expected some v1 sources to remain after partial archive")
			}

			receipt, err := ApplyMigration(plan)
			if err != nil {
				t.Fatalf("retry after %s: %v", crash, err)
			}
			if receipt.SourceDigest != plan.SourceDigest || receipt.RecordCount != plan.RecordCount {
				t.Fatalf("retry receipt = %+v", receipt)
			}
			if v1SourcesRemain(home) {
				t.Fatal("v1 sources remain after retry")
			}
			// The archive must cover every planned source file.
			archiveRoot := filepath.Join(home, filepath.FromSlash(journal.ArchivePath))
			archived, err := snapshotTree(t, archiveRoot)
			if err != nil {
				t.Fatal(err)
			}
			if len(archived) != sourceCount {
				t.Fatalf("archive holds %d files, want %d planned sources", len(archived), sourceCount)
			}
			verifyMigratedHome(t, home, plan)
		})
	}
}

// TestMigrationApplyRecoversManuallyPartiallyArchivedHome simulates the
// crash state directly without crash injection: v2 targets committed, then
// the aggregates location moved into the deterministic archive path with
// leases and dispatch still in place. The retry must discover the archive,
// verify the union against the plan digest, move the remaining sources, and
// complete.
func TestMigrationApplyRecoversManuallyPartiallyArchivedHome(t *testing.T) {
	home := t.TempDir()
	fullV1Home(t, home)
	plan := planHome(t, home)

	// Commit the v2 targets, then crash before archiving.
	migrationCrashAfter = "committed"
	_, err := ApplyMigration(plan)
	migrationCrashAfter = ""
	if err == nil {
		t.Fatal("crash injection did not fail apply")
	}
	if !v1SourcesRemain(home) {
		t.Fatal("expected v1 sources intact after committed crash")
	}

	// Simulate a crashed first archive rename: aggregates moved into the
	// deterministic archive path, everything else still in place.
	archiveRel := "state/.task-authority-migration/archive-" + plan.SourceDigest
	archiveRoot := filepath.Join(home, filepath.FromSlash(archiveRel))
	if err := os.MkdirAll(archiveRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(home, "state", ".task-authority", "aggregates"), filepath.Join(archiveRoot, "aggregates")); err != nil {
		t.Fatal(err)
	}

	receipt, err := ApplyMigration(plan)
	if err != nil {
		t.Fatalf("retry after manual partial archive: %v", err)
	}
	if receipt.SourceDigest != plan.SourceDigest || receipt.RecordCount != plan.RecordCount {
		t.Fatalf("retry receipt = %+v", receipt)
	}
	if v1SourcesRemain(home) {
		t.Fatal("v1 sources remain after retry")
	}
	verifyMigratedHome(t, home, plan)
}

// TestMigrationSymlinkAttacks plants a symlink at every migration-owned path
// component and proves apply fails closed without writing, removing, or
// moving anything outside the home.
func TestMigrationSymlinkAttacks(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, home, outside string, plan *MigrationPlan)
	}{
		{
			name: "migration dir",
			plant: func(t *testing.T, home, outside string, plan *MigrationPlan) {
				mustSymlink(t, outside, filepath.Join(home, "state", ".task-authority-migration"))
			},
		},
		{
			name: "stage dir",
			plant: func(t *testing.T, home, outside string, plan *MigrationPlan) {
				mustSymlink(t, outside, filepath.Join(home, "state", ".task-authority-migration", "stage", plan.SourceDigest))
			},
		},
		{
			name: "stage parent",
			plant: func(t *testing.T, home, outside string, plan *MigrationPlan) {
				mustSymlink(t, outside, filepath.Join(home, "state", ".task-authority-migration", "stage"))
			},
		},
		{
			name: "target parent",
			plant: func(t *testing.T, home, outside string, plan *MigrationPlan) {
				mustSymlink(t, outside, filepath.Join(home, "state", ".task-authority"))
			},
		},
		{
			name: "old dir",
			plant: func(t *testing.T, home, outside string, plan *MigrationPlan) {
				// The v1 target parent must be real; only the .old cleanup path
				// is attacked.
				mustSymlink(t, outside, filepath.Join(home, "state", ".task-authority", "v1.old"))
			},
		},
		{
			name: "installing dir",
			plant: func(t *testing.T, home, outside string, plan *MigrationPlan) {
				mustSymlink(t, outside, filepath.Join(home, "state", ".task-authority", "v1.installing"))
			},
		},
		{
			name: "archive path",
			plant: func(t *testing.T, home, outside string, plan *MigrationPlan) {
				mustSymlink(t, outside, filepath.Join(home, "state", ".task-authority-migration", "archive-"+plan.SourceDigest))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			fullV1Home(t, home)
			outside := t.TempDir()
			writeDocAt(t, outside, "sentinel.txt", []byte("keep me"))
			plan := planHome(t, home)
			before, err := snapshotTree(t, outside)
			if err != nil {
				t.Fatal(err)
			}
			tc.plant(t, home, outside, plan)

			if _, err := ApplyMigration(plan); err == nil {
				t.Fatalf("apply succeeded with symlink attack at %s, want fail closed", tc.name)
			}
			after, err := snapshotTree(t, outside)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("symlink attack at %s modified the outside directory:\nbefore=%v\nafter=%v", tc.name, before, after)
			}
		})
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	// A real directory may already occupy the link path (the v1 sources live
	// under state/.task-authority); the attack replaces it with the link.
	if err := os.RemoveAll(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// TestMigrationRejectsSymlinkedDocuments proves the journal, receipt, and
// manifest are verified as regular non-symlink files before reading:
// replacing one with a link to an outside file holding the original content
// fails closed instead of following the link.
func TestMigrationRejectsSymlinkedDocuments(t *testing.T) {
	t.Run("journal", func(t *testing.T) {
		home := t.TempDir()
		fullV1Home(t, home)
		plan := planHome(t, home)

		migrationCrashAfter = "committed"
		if _, err := ApplyMigration(plan); err == nil {
			t.Fatal("crash injection did not fail apply")
		}
		migrationCrashAfter = ""

		journalPath := filepath.Join(home, "state", ".task-authority-migration", "journal.json")
		original := readFileBytes(t, journalPath)
		outside := t.TempDir()
		writeDocAt(t, outside, "journal.json", original)
		if err := os.Remove(journalPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, "journal.json"), journalPath); err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyMigration(plan); err == nil {
			t.Fatal("apply succeeded reading journal through a symlink, want fail closed")
		}
	})

	t.Run("receipt", func(t *testing.T) {
		home := t.TempDir()
		fullV1Home(t, home)
		plan := planHome(t, home)

		migrationCrashAfter = "receipt"
		if _, err := ApplyMigration(plan); err == nil {
			t.Fatal("crash injection did not fail apply")
		}
		migrationCrashAfter = ""

		receiptPath := filepath.Join(home, "state", ".task-authority-migration", "receipt.json")
		original := readFileBytes(t, receiptPath)
		outside := t.TempDir()
		writeDocAt(t, outside, "receipt.json", original)
		if err := os.Remove(receiptPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, "receipt.json"), receiptPath); err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyMigration(plan); err == nil {
			t.Fatal("apply succeeded reading receipt through a symlink, want fail closed")
		}
	})

	t.Run("manifest", func(t *testing.T) {
		home := t.TempDir()
		fullV1Home(t, home)
		plan := planHome(t, home)

		migrationCrashAfter = "committed"
		if _, err := ApplyMigration(plan); err == nil {
			t.Fatal("crash injection did not fail apply")
		}
		migrationCrashAfter = ""

		manifestPath := filepath.Join(home, "state", ".task-authority-migration", "manifest.json")
		original := readFileBytes(t, manifestPath)
		outside := t.TempDir()
		writeDocAt(t, outside, "manifest.json", original)
		if err := os.Remove(manifestPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, "manifest.json"), manifestPath); err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyMigration(plan); err == nil {
			t.Fatal("apply succeeded reading manifest through a symlink, want fail closed")
		}
	})
}

// TestMigrationSyncsParentsAfterRename proves every migration rename is
// followed by an explicit directory sync of the source and destination
// parents: the namespace install and each archive move.
func TestMigrationSyncsParentsAfterRename(t *testing.T) {
	home := t.TempDir()
	fullV1Home(t, home)
	plan := planHome(t, home)

	canon, err := canonicalMigrationHome(home)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	synced := map[string]bool{}
	orig := migrationSyncDir
	migrationSyncDir = func(dir string) error {
		mu.Lock()
		synced[filepath.Clean(dir)] = true
		mu.Unlock()
		return orig(dir)
	}
	defer func() { migrationSyncDir = orig }()

	mustApply(t, plan)

	want := map[string]bool{
		filepath.Clean(filepath.Join(canon, "state")):                                                                                      true, // parent of .dispatch archive move
		filepath.Clean(filepath.Join(canon, "state", ".task-authority")):                                                                   true, // parent of aggregates move + v2 install
		filepath.Clean(filepath.Join(canon, "state", ".task-authority-migration")):                                                         true, // journal/manifest/receipt renames
		filepath.Clean(filepath.Join(canon, "state", ".task-authority-migration", "stage", plan.SourceDigest, "state", ".task-authority")): true, // install source parent
	}
	mu.Lock()
	defer mu.Unlock()
	for dir := range want {
		if !synced[dir] {
			t.Fatalf("parent directory %s was never synced after a migration rename", dir)
		}
	}
}

// TestMigrationSyncFailureFailsClosed proves a failed directory sync aborts
// the migration without leaving unrecoverable state: the retry converges to
// a completed migration.
func TestMigrationSyncFailureFailsClosed(t *testing.T) {
	home := t.TempDir()
	fullV1Home(t, home)
	plan := planHome(t, home)

	orig := migrationSyncDir
	fail := true
	migrationSyncDir = func(dir string) error {
		if fail {
			return errors.New("injected fsync failure")
		}
		return orig(dir)
	}
	defer func() { migrationSyncDir = orig }()

	if _, err := ApplyMigration(plan); err == nil {
		t.Fatal("apply succeeded despite injected fsync failure, want fail closed")
	}
	fail = false
	mustApply(t, plan)
	if v1SourcesRemain(home) {
		t.Fatal("v1 sources remain after retry")
	}
	verifyMigratedHome(t, home, plan)
}
