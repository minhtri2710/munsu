package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func guardLegacyManifest(t *testing.T, root string) *LaunchManifest {
	t.Helper()
	manifest := guardManifestFixture(t, root, true)
	if _, err := WriteManifest(root, manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestGuardBurnDownCheckLegacyBriefMigrationRefusesUnsupportedPolicy(t *testing.T) {
	root := t.TempDir()
	manifest := guardLegacyManifest(t, root)
	unsupported := LegacyBriefMigrationPolicy("legacy-v0")
	manifest.LegacyBriefMigration = &unsupported

	err := CheckLegacyBriefMigration(root, manifest)
	if err == nil || !strings.Contains(err.Error(), "unsupported legacy brief migration policy") {
		t.Fatalf("CheckLegacyBriefMigration error = %v, want unsupported-policy refusal", err)
	}
}

func TestGuardBurnDownCheckLegacyBriefMigrationRefusesMissingManifestBrief(t *testing.T) {
	root := t.TempDir()
	manifest := guardLegacyManifest(t, root)
	filtered := manifest.Artifacts[:0]
	for _, entry := range manifest.Artifacts {
		if entry.Path != BriefName {
			filtered = append(filtered, entry)
		}
	}
	manifest.Artifacts = filtered

	err := CheckLegacyBriefMigration(root, manifest)
	if err == nil || !strings.Contains(err.Error(), "canonical brief not in manifest") {
		t.Fatalf("CheckLegacyBriefMigration error = %v, want missing-brief-entry refusal", err)
	}
}

func TestGuardBurnDownCheckLegacyBriefMigrationRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	manifest := guardLegacyManifest(t, root)
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("brief\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".soldier-md")); err != nil {
		t.Fatal(err)
	}

	err := CheckLegacyBriefMigration(root, manifest)
	if err == nil || !strings.Contains(err.Error(), "legacy .soldier-md is a symlink") {
		t.Fatalf("CheckLegacyBriefMigration error = %v, want symlink refusal", err)
	}
}
