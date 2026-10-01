//go:build integration

package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestRetirementSkipsVanishedMetaAndRefusesRealReadError(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "workspace-vanished-meta"
	auth := mergeTestAuth(t, homeDir, taskID)
	writeRetireMeta(t, homeDir, taskID, "@1", filepath.Join(homeDir, "worktree"))

	vanishedMeta, err := home.MetaFilePath(homeDir, "aaa-vanished")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home.StateDir(homeDir), "missing-target"), vanishedMeta); err != nil {
		t.Fatal(err)
	}

	realErrorMeta, err := home.MetaFilePath(homeDir, "bbb-real-error")
	if err != nil {
		t.Fatal(err)
	}
	realErrorTarget := realErrorMeta + ".target"
	if err := os.Mkdir(realErrorTarget, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realErrorTarget, realErrorMeta); err != nil {
		t.Fatal(err)
	}

	rec := &recordingTeardown{alive: true}
	_, err = RetireTask(Options{HomeDir: homeDir, ID: taskID, Force: true}, rec, fakeRetirementJournals{}, auth)
	if err == nil || !strings.Contains(err.Error(), "bbb-real-error.meta") {
		t.Fatalf("RetireTask error = %v, want the real read error after skipping vanished meta", err)
	}
	if len(rec.disposed) != 0 {
		t.Fatalf("workspace close proceeded after real read error: disposed=%v", rec.disposed)
	}
}
