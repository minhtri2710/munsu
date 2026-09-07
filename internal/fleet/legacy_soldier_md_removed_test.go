//go:build integration

package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShipSafetyCheck_LegacySoldierMdNoLongerAccepted verifies ADR-0008 removal:
// the legacy .soldier-md migration-recognition branch inside shipSafetyCheck is
// gone, so a leftover .soldier-md (even one whose bytes match the canonical
// brief digest) is now treated as ordinary unexplained dirt and blocks
// retirement instead of being silently accepted.
func TestShipSafetyCheck_LegacySoldierMdNoLongerAccepted(t *testing.T) {
	tmp := t.TempDir()
	wt, md := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	// Write a legacy .soldier-md whose content matches the canonical brief.
	briefContent, err := os.ReadFile(filepath.Join(wt, BriefName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".soldier-md"), briefContent, 0644); err != nil {
		t.Fatal(err)
	}

	_, err = shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, md), fakeTeardown{}, nil)
	if err == nil {
		t.Fatal("legacy .soldier-md matching the brief digest must no longer be accepted during retirement")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("unexpected error, want uncommitted-changes refusal: %v", err)
	}
}
