package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardBurnDownReadManifestRefusesNonRegularFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ManifestName), 0755); err != nil {
		t.Fatal(err)
	}

	_, err := ReadManifest(root)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("ReadManifest error = %v, want non-regular refusal", err)
	}
}

func TestGuardBurnDownReadManifestRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(target, []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ManifestName)); err != nil {
		t.Fatal(err)
	}

	_, err := ReadManifest(root)
	if err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("ReadManifest error = %v, want symlink refusal", err)
	}
}
