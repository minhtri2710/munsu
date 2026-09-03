package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func guardManifestFixture(t *testing.T, root string) *LaunchManifest {
	t.Helper()
	setupGuardManifestFiles(t, root)
	entries := make([]ManifestEntry, 0, 5)
	for _, name := range []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName} {
		entry, err := ManifestEntryForFile(root, name, DisposalPolicyCleanable)
		if err != nil {
			t.Fatalf("ManifestEntryForFile(%q): %v", name, err)
		}
		entries = append(entries, entry)
	}
	return BuildManifest(entries)
}

func setupGuardManifestFiles(t *testing.T, root string) {
	t.Helper()
	for name, content := range map[string]string{
		CharterName:      "charter\n",
		BriefName:        "brief\n",
		EnvelopeName:     "envelope\n",
		PromptName:       "prompt\n",
		LaunchScriptName: "launch\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
	}
}

func TestGuardBurnDownManifestContainmentWaiversPinned(t *testing.T) {
	t.Run("direct-child symlinks are rejected before containment", func(t *testing.T) {
		root := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.WriteFile(outside, []byte("outside\n"), 0644); err != nil {
			t.Fatal(err)
		}

		manifestPath := filepath.Join(root, ManifestName)
		if err := os.Symlink(outside, manifestPath); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadManifest(root); err == nil || !strings.Contains(err.Error(), "is a symlink") {
			t.Fatalf("ReadManifest symlink error = %v", err)
		}
		if err := verifyManifestFile(root); err == nil || !strings.Contains(err.Error(), "is a symlink") {
			t.Fatalf("verifyManifestFile symlink error = %v", err)
		}
	})

	t.Run("non-regular direct children are rejected before containment", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ManifestName), 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadManifest(root); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("ReadManifest directory error = %v", err)
		}
		if err := verifyManifestFile(root); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("verifyManifestFile directory error = %v", err)
		}
	})

	t.Run("symlinked root canonicalizes both sides", func(t *testing.T) {
		actual := t.TempDir()
		manifest := guardManifestFixture(t, actual)
		if _, err := WriteManifest(actual, manifest); err != nil {
			t.Fatal(err)
		}
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(actual, alias); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadManifest(alias); err != nil {
			t.Fatalf("ReadManifest through symlinked root: %v", err)
		}
		if err := verifyManifestFile(alias); err != nil {
			t.Fatalf("verifyManifestFile through symlinked root: %v", err)
		}
	})

	t.Run("hard link remains lexically inside after resolution", func(t *testing.T) {
		root := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.WriteFile(outside, []byte("hard-linked\n"), 0644); err != nil {
			t.Fatal(err)
		}
		inside := filepath.Join(root, ManifestName)
		if err := os.Link(outside, inside); err != nil {
			t.Fatal(err)
		}
		realPath, err := filepath.EvalSymlinks(inside)
		if err != nil {
			t.Fatal(err)
		}
		if !isWithinWorktreeRoot(root, realPath) {
			t.Fatalf("hard-linked path %q escaped root %q", realPath, root)
		}
		if err := verifyManifestFile(root); err != nil {
			t.Fatalf("verifyManifestFile hard link: %v", err)
		}
	})

	t.Run("unresolved root fails before containment", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "missing")
		if _, err := ReadManifest(root); err == nil || strings.Contains(err.Error(), "symlink escapes") {
			t.Fatalf("ReadManifest unresolved root error = %v", err)
		}
		if err := verifyManifestFile(root); err == nil || strings.Contains(err.Error(), "symlink escapes") {
			t.Fatalf("verifyManifestFile unresolved root error = %v", err)
		}
	})
}
