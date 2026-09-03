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

// TestLaunchManifestHasNoLegacyPolicyField verifies the spawn-time policy stamp
// (ADR-0008) was removed: a manifest built and written the way the spawn runner
// does no longer serializes a legacy_brief_migration field in the manifest
// artifact it writes to disk.
func TestLaunchManifestHasNoLegacyPolicyField(t *testing.T) {
	tmp := t.TempDir()
	setupTestLaunchFiles(t, tmp)
	writeTestManifest(t, tmp, nil)

	data, err := os.ReadFile(filepath.Join(tmp, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "legacy_brief_migration") {
		t.Errorf("manifest must not contain legacy_brief_migration field after removal: %s", string(data))
	}
}
