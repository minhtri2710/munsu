package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardBurnDownVerifyManifestFileRefusesNonRegularFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ManifestName), 0755); err != nil {
		t.Fatal(err)
	}

	err := verifyManifestFile(root)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("verifyManifestFile error = %v, want non-regular refusal", err)
	}
}

func TestGuardBurnDownVerifyManifestFileRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(target, []byte("manifest\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ManifestName)); err != nil {
		t.Fatal(err)
	}

	err := verifyManifestFile(root)
	if err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("verifyManifestFile error = %v, want symlink refusal", err)
	}
}
