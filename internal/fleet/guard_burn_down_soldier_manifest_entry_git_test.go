package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func guardManifestGitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func TestGuardBurnDownVerifyManifestEntryRefusesTrackedFile(t *testing.T) {
	root := t.TempDir()
	guardManifestGitRun(t, root, "init")
	guardManifestGitRun(t, root, "config", "user.name", "Munsu Test")
	guardManifestGitRun(t, root, "config", "user.email", "munsu@example.invalid")
	path := filepath.Join(root, "artifact")
	if err := os.WriteFile(path, []byte("tracked\n"), 0644); err != nil {
		t.Fatal(err)
	}
	guardManifestGitRun(t, root, "add", "artifact")
	guardManifestGitRun(t, root, "commit", "-m", "track artifact")

	err := verifyManifestEntry(root, &ManifestEntry{
		Path:   "artifact",
		SHA256: sha256Content([]byte("tracked\n")),
		Policy: DisposalPolicyCleanable,
	})
	if err == nil || !strings.Contains(err.Error(), "is tracked by git") {
		t.Fatalf("verifyManifestEntry error = %v, want tracked-file refusal", err)
	}
}

func TestGuardBurnDownVerifyManifestEntryRefusesUnsupportedPolicy(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "artifact")
	content := []byte("untracked\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	err := verifyManifestEntry(root, &ManifestEntry{
		Path:   "artifact",
		SHA256: sha256Content(content),
		Policy: DisposalPolicy("delete-whenever"),
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported disposal policy") {
		t.Fatalf("verifyManifestEntry error = %v, want unsupported-policy refusal", err)
	}
}
