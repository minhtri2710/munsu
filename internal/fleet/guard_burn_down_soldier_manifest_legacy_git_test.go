package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardBurnDownCheckLegacyBriefMigrationRefusesNonRegular(t *testing.T) {
	root := t.TempDir()
	manifest := guardLegacyManifest(t, root)
	if err := os.Mkdir(filepath.Join(root, ".soldier-md"), 0755); err != nil {
		t.Fatal(err)
	}

	err := CheckLegacyBriefMigration(root, manifest)
	if err == nil || !strings.Contains(err.Error(), "legacy .soldier-md is not a regular file") {
		t.Fatalf("CheckLegacyBriefMigration error = %v, want non-regular refusal", err)
	}
}

func TestGuardBurnDownCheckLegacyBriefMigrationRefusesTrackedFile(t *testing.T) {
	root := t.TempDir()
	manifest := guardLegacyManifest(t, root)
	brief, err := os.ReadFile(filepath.Join(root, BriefName))
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(root, ".soldier-md")
	if err := os.WriteFile(legacy, brief, 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init"},
		{"config", "user.name", "Munsu Test"},
		{"config", "user.email", "munsu@example.invalid"},
		{"add", ".soldier-md"},
		{"commit", "-m", "track legacy brief"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}

	err = CheckLegacyBriefMigration(root, manifest)
	if err == nil || !strings.Contains(err.Error(), "legacy .soldier-md is tracked by git") {
		t.Fatalf("CheckLegacyBriefMigration error = %v, want tracked-file refusal", err)
	}
}
