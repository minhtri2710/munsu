//go:build integration && !windows

package fleet

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/testutil"
)

const statusTestHeadSHA = "0000111122223333444455556666777788889999"
const statusTestMergedSHA = "aaaabbbbccccddddeeeeffff0000111122223333"

// TestRetireMergedPollRefusesSymlinkedStatus pins that publication goes through
// home's state-path validation: a .status symlinked out of the home is refused,
// and the file it points at is never written.
func TestRetireMergedPollRefusesSymlinkedStatus(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, statusTestHeadSHA, "main")
	defer cleanup()
	defer installMockMergeStatus(t, true, statusTestHeadSHA, statusTestMergedSHA)()

	outside := filepath.Join(t.TempDir(), "outside.status")
	if err := os.WriteFile(outside, nil, 0600); err != nil {
		t.Fatal(err)
	}
	statusPath, err := mhome.StatusFilePath(home, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, statusPath); err != nil {
		t.Fatal(err)
	}

	if err := observeAndRetire(t, home, taskID, checkPath, retirementPollAuth(t, home, taskID)); err == nil {
		t.Fatal("retirement published through a symlinked status file")
	}
	if data, err := os.ReadFile(outside); err != nil || len(data) != 0 {
		t.Fatalf("symlink target = %q (%v), want untouched", data, err)
	}
}

// TestRetireMergedPollStatusIsPrivate pins home's 0600 mode on the status file
// the publication step creates.
func TestRetireMergedPollStatusIsPrivate(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, statusTestHeadSHA, "main")
	defer cleanup()
	defer installMockMergeStatus(t, true, statusTestHeadSHA, statusTestMergedSHA)()

	if err := observeAndRetire(t, home, taskID, checkPath, retirementPollAuth(t, home, taskID)); err != nil {
		t.Fatalf("retire: %v", err)
	}
	statusPath, err := mhome.StatusFilePath(home, taskID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("status mode = %o, want 0600", perm)
	}
}

// TestRetireMergedPollReentryRefusesUnreadableStatus pins that the re-entry
// check fails closed: a status that cannot be read is surfaced, not taken as
// "not yet retired".
func TestRetireMergedPollReentryRefusesUnreadableStatus(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, statusTestHeadSHA, "main")
	defer cleanup()
	defer installMockMergeStatus(t, true, statusTestHeadSHA, statusTestMergedSHA)()

	if err := observeAndRetire(t, home, taskID, checkPath, retirementPollAuth(t, home, taskID)); err != nil {
		t.Fatalf("retire: %v", err)
	}
	statusPath, err := mhome.StatusFilePath(home, taskID)
	if err != nil {
		t.Fatal(err)
	}
	testutil.MakePathUnreadable(t, statusPath)

	err = RetireMergedPoll(home, taskID, checkPath, mergedResultForTest("", ""), retirementPollAuth(t, home, taskID))
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("re-entry error = %v, want the status read failure", err)
	}
}
