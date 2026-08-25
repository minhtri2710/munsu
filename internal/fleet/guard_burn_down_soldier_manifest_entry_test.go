package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardBurnDownVerifyManifestEntryRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(target, []byte("outside\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "artifact")); err != nil {
		t.Fatal(err)
	}

	err := verifyManifestEntry(root, &ManifestEntry{
		Path:   "artifact",
		SHA256: guardManifestTestSHA,
		Policy: DisposalPolicyCleanable,
	})
	if err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("verifyManifestEntry error = %v, want symlink refusal", err)
	}
}

func TestGuardBurnDownVerifyManifestEntryRefusesNonRegularFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "artifact"), 0755); err != nil {
		t.Fatal(err)
	}

	err := verifyManifestEntry(root, &ManifestEntry{
		Path:   "artifact",
		SHA256: guardManifestTestSHA,
		Policy: DisposalPolicyCleanable,
	})
	if err == nil || !strings.Contains(err.Error(), "is not a regular file") {
		t.Fatalf("verifyManifestEntry error = %v, want non-regular refusal", err)
	}
}

func TestGuardBurnDownVerifyManifestEntryRefusesOutsidePath(t *testing.T) {
	root := t.TempDir()
	outsideRoot := t.TempDir()
	outside := filepath.Join(outsideRoot, "artifact")
	if err := os.WriteFile(outside, []byte("outside\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := verifyManifestEntry(root, &ManifestEntry{
		Path:   filepath.Join("..", filepath.Base(outsideRoot), "artifact"),
		SHA256: guardManifestTestSHA,
		Policy: DisposalPolicyCleanable,
	})
	if err == nil || !strings.Contains(err.Error(), "symlink escapes worktree root") {
		t.Fatalf("verifyManifestEntry error = %v, want containment refusal", err)
	}
}
