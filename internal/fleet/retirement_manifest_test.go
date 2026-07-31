//go:build integration

package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupWorktreeWithManifest creates a git repo with a remote and writes
// canonical launch artifacts including the manifest. Returns the worktree path
// and the manifest digest that should be stored in meta.
func setupWorktreeWithManifest(t *testing.T, wt, remote string, briefContent []byte) (string, string) {
	t.Helper()
	os.MkdirAll(wt, 0755)
	setupGitRepo(t, wt, remote)

	gitEnv := append(os.Environ(),
		fmt.Sprintf("GIT_CEILING_DIRECTORIES=%s", wt),
	)

	cmd := exec.Command("git", "checkout", "-b", "fm/manifest-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %s", out)
	}
	cmd = exec.Command("git", "push", "-u", "origin", "fm/manifest-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git push: %s", out)
	}

	// Write canonical launch artifacts.
	charter := DefaultCharter("manifest-test", "ship", "direct-PR")
	if briefContent == nil {
		briefContent = []byte("# Task: manifest-test\n\nCanonical brief.\n")
	}
	prompt := "complete prompt text"
	launchScript := "#!/usr/bin/env bash\necho hi\n"

	os.WriteFile(filepath.Join(wt, CharterName), []byte(charter), 0644)
	os.WriteFile(filepath.Join(wt, BriefName), briefContent, 0644)
	os.WriteFile(filepath.Join(wt, PromptName), []byte(prompt), 0644)
	os.WriteFile(filepath.Join(wt, EnvelopeName), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(wt, LaunchScriptName), []byte(launchScript), 0644)

	// Write envelope with correct brief SHA-256.
	env := &LaunchEnvelope{
		EnvelopeVersion: EnvelopeVersion,
		TaskID:          "manifest-test",
		DeliveryMode:    "direct-PR",
		ParentCaptainID: "captain-1",
		ParentHome:      "/tmp/parent",
		CharterSHA256:   sha256Content([]byte(charter)),
		BriefSHA256:     sha256Content(briefContent),
		PromptSHA256:    sha256Content([]byte(prompt)),
	}
	WriteEnvelope(wt, env)

	// Build manifest from actual file digests, with legacy migration policy.
	entries := []ManifestEntry{}
	for _, name := range []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName} {
		entry, err := ManifestEntryForFile(wt, name, DisposalPolicyCleanable)
		if err != nil {
			t.Fatalf("manifest entry for %s: %v", name, err)
		}
		entries = append(entries, entry)
	}
	manifest := BuildManifest(entries)
	policy := LegacyBriefMatchCanonicalV1
	manifest.LegacyBriefMigration = &policy
	digest, err := WriteManifest(wt, manifest)
	if err != nil {
		t.Fatalf("writing manifest: %v", err)
	}
	return wt, digest
}

// metaWithManifest creates a meta map with the given manifest digest.
func metaWithManifest(wtPath, manifestDigest string) map[string]string {
	return map[string]string{
		"worktree":               wtPath,
		"kind":                   "ship",
		"launch_manifest_sha256": manifestDigest,
	}
}

func TestShipSafetyCheck_CanonicalManifest(t *testing.T) {
	tmp := t.TempDir()
	wt, md := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, md), fakeTeardown{})
	if err != nil {
		t.Fatalf("canonical manifest should pass: %v", err)
	}
}

func TestShipSafetyCheck_ModifiedManifestArtifact(t *testing.T) {
	tmp := t.TempDir()
	wt, md := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	// Modify the brief file after the manifest was written.
	modifiedBrief := []byte("# Task: modified\n\nMODIFIED brief.\n")
	os.WriteFile(filepath.Join(wt, BriefName), modifiedBrief, 0644)

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, md), fakeTeardown{})
	if err == nil {
		t.Fatal("modified manifest artifact should block")
	}
	if !strings.Contains(err.Error(), "launch artifact verification failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShipSafetyCheck_TrackedFileBlocks(t *testing.T) {
	tmp := t.TempDir()
	wt, md := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	gitEnv := append(os.Environ(),
		fmt.Sprintf("GIT_CEILING_DIRECTORIES=%s", wt),
	)

	// Create a tracked file (committed) and then modify it.
	os.WriteFile(filepath.Join(wt, "tracked.go"), []byte("package main\n"), 0644)
	cmd := exec.Command("git", "add", "tracked.go")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s", out)
	}
	cmd = exec.Command("git", "commit", "-m", "add tracked.go")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s", out)
	}
	cmd = exec.Command("git", "push", "origin", "fm/manifest-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git push: %s", out)
	}

	// Now modify the tracked file.
	os.WriteFile(filepath.Join(wt, "tracked.go"), []byte("package main\n\nfunc main() {}\n"), 0644)

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, md), fakeTeardown{})
	if err == nil {
		t.Fatal("tracked modified file should block")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShipSafetyCheck_UnlistedFileBlocks(t *testing.T) {
	tmp := t.TempDir()
	wt, md := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	// Write an untracked file not in the manifest.
	os.WriteFile(filepath.Join(wt, "rogue.txt"), []byte("not a launch artifact\n"), 0644)

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, md), fakeTeardown{})
	if err == nil {
		t.Fatal("unlisted untracked file should block")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShipSafetyCheck_LegacyMatchCleaned(t *testing.T) {
	tmp := t.TempDir()
	wt, md := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	// Write legacy .soldier-md with content matching the canonical brief.
	briefContent, _ := os.ReadFile(filepath.Join(wt, BriefName))
	os.WriteFile(filepath.Join(wt, ".soldier-md"), briefContent, 0644)

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, md), fakeTeardown{})
	if err != nil {
		t.Fatalf("legacy .soldier-md matching brief digest should pass: %v", err)
	}
}

func TestShipSafetyCheck_LegacyMismatchBlocks(t *testing.T) {
	tmp := t.TempDir()
	wt, md := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	// Write legacy .soldier-md with DIFFERENT content.
	os.WriteFile(filepath.Join(wt, ".soldier-md"), []byte("# DIFFERENT brief\n\nNot matching.\n"), 0644)

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, md), fakeTeardown{})
	if err == nil {
		t.Fatal("legacy .soldier-md not matching brief digest should block")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShipSafetyCheck_LegacyWithoutCanonicalBriefBlocks(t *testing.T) {
	tmp := t.TempDir()
	wt, md := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	// Remove the canonical brief so there's no evidence to compare against.
	os.Remove(filepath.Join(wt, BriefName))

	// Write legacy .soldier-md.
	briefContent, _ := os.ReadFile(filepath.Join(tmp, "worktree", ".soldier-brief.md"))
	// Note: briefContent was read from the canonical brief, but since we removed it,
	// this will fail. Let me just read the brief content from the original source.
	// Actually, the brief was removed, so we can't read it anymore. Let me recreate
	// the brief content from the original.
	briefContent = []byte("# Task: manifest-test\n\nCanonical brief.\n")
	os.WriteFile(filepath.Join(wt, ".soldier-md"), briefContent, 0644)

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, md), fakeTeardown{})
	if err == nil {
		t.Fatal("legacy .soldier-md without canonical brief evidence should block")
	}
}

func TestShipSafetyCheck_MissingManifestBlocks(t *testing.T) {
	tmp := t.TempDir()
	wt, md := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	// Remove the manifest file.
	os.Remove(filepath.Join(wt, ManifestName))

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, md), fakeTeardown{})
	if err == nil {
		t.Fatal("missing manifest should block")
	}
	if !strings.Contains(err.Error(), "launch artifact verification failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShipSafetyCheck_CorruptManifestBlocks(t *testing.T) {
	tmp := t.TempDir()
	wt, md := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	// Corrupt the manifest file.
	os.WriteFile(filepath.Join(wt, ManifestName), []byte("{invalid json}"), 0644)

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, md), fakeTeardown{})
	if err == nil {
		t.Fatal("corrupt manifest should block")
	}
	if !strings.Contains(err.Error(), "launch artifact verification failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShipSafetyCheck_UnknownManifestVersionBlocks(t *testing.T) {
	tmp := t.TempDir()
	wt, md := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	// Replace manifest with unknown version.
	content := `{"manifest_version":"soldier-manifest-v99","artifacts":[]}`
	os.WriteFile(filepath.Join(wt, ManifestName), []byte(content), 0644)

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, md), fakeTeardown{})
	if err == nil {
		t.Fatal("unknown manifest version should block")
	}
	if !strings.Contains(err.Error(), "launch artifact verification failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShipSafetyCheck_WrongManifestDigestBlocks(t *testing.T) {
	tmp := t.TempDir()
	wt, _ := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	// Use a wrong expected manifest digest in meta.
	wrongDigest := "0000000000000000000000000000000000000000000000000000000000000000"

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, wrongDigest), fakeTeardown{})
	if err == nil {
		t.Fatal("wrong manifest digest should block")
	}
	if !strings.Contains(err.Error(), "launch artifact verification failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShipSafetyCheck_IgnoredDigestMatch(t *testing.T) {
	tmp := t.TempDir()
	wt, md := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	// The manifest entry files are in .gitignore, so they're "ignored" by git.
	// VerifyLaunchArtifacts checks them directly (not through porcelain).
	// This test ensures the canonical case works when files are in .gitignore.
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, md), fakeTeardown{})
	if err != nil {
		t.Fatalf("ignored digest-match artifacts should pass: %v", err)
	}
}

func TestShipSafetyCheck_IgnoredDigestMismatch(t *testing.T) {
	tmp := t.TempDir()
	wt, md := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	// Modify a file that's in .gitignore (the brief).
	// Since it's in .gitignore, git status won't show it, but VerifyLaunchArtifacts
	// checks it directly.
	os.WriteFile(filepath.Join(wt, BriefName), []byte("modified ignored content"), 0644)

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, md), fakeTeardown{})
	if err == nil {
		t.Fatal("ignored digest-mismatch artifact should block")
	}
	if !strings.Contains(err.Error(), "launch artifact verification failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShipSafetyCheck_ManifestTrackedBlocks(t *testing.T) {
	tmp := t.TempDir()
	wt, md := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	gitEnv := append(os.Environ(),
		fmt.Sprintf("GIT_CEILING_DIRECTORIES=%s", wt),
	)

	// Commit the manifest file (making it tracked).
	cmd := exec.Command("git", "add", ManifestName)
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s", out)
	}
	cmd = exec.Command("git", "commit", "-m", "add manifest")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s", out)
	}

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, md), fakeTeardown{})
	if err == nil {
		t.Fatal("tracked manifest should block")
	}
	if !strings.Contains(err.Error(), "launch artifact verification failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShipSafetyCheck_ManifestModifiedBlocks(t *testing.T) {
	tmp := t.TempDir()
	wt, md := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	// Modify the manifest content (change the digest to trigger mismatch).
	// We need to modify the manifest file and then verify the original digest
	// no longer matches.
	manifestData, _ := os.ReadFile(filepath.Join(wt, ManifestName))
	modifiedData := strings.Replace(string(manifestData), "cleanable", "cleanable", 1) // no-op, actually change
	modifiedData = strings.Replace(modifiedData, "\"sha256\": \"", "\"sha256\": \"00", 1) // change digest
	os.WriteFile(filepath.Join(wt, ManifestName), []byte(modifiedData), 0644)

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, md), fakeTeardown{})
	if err == nil {
		t.Fatal("modified manifest should block")
	}
	if !strings.Contains(err.Error(), "launch artifact verification failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShipSafetyCheck_EmptyExpectedManifestSHA(t *testing.T) {
	tmp := t.TempDir()
	wt, _ := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	// Empty manifest SHA in meta falls back to computing from the worktree.
	// This is allowed for backward compatibility.
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, ""), fakeTeardown{})
	if err != nil {
		t.Fatalf("empty manifest SHA should fall back to worktree: %v", err)
	}
}

func TestShipSafetyCheck_NoLaunchManifestSHAInMeta(t *testing.T) {
	tmp := t.TempDir()
	wt, _ := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	// Meta without launch_manifest_sha256 falls back to computing from the worktree.
	// This is allowed for backward compatibility.
	meta := map[string]string{
		"worktree": wt,
		"kind":     "ship",
	}
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{})
	if err != nil {
		t.Fatalf("missing manifest SHA should fall back to worktree: %v", err)
	}
}

func TestShipSafetyCheck_ModifiedCanonicalBriefDuringLegacy(t *testing.T) {
	tmp := t.TempDir()
	wt, md := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	// Write legacy .soldier-md with the ORIGINAL brief content.
	originalBrief, _ := os.ReadFile(filepath.Join(wt, BriefName))
	os.WriteFile(filepath.Join(wt, ".soldier-md"), originalBrief, 0644)

	// Now modify the canonical brief (digest no longer matches manifest).
	os.WriteFile(filepath.Join(wt, BriefName), []byte("MODIFIED canonical brief"), 0644)

	// The canonical brief modification should be caught by VerifyLaunchArtifacts
	// before we even get to the legacy migration check.
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, md), fakeTeardown{})
	if err == nil {
		t.Fatal("modified canonical brief should block even with matching legacy file")
	}
	if !strings.Contains(err.Error(), "launch artifact verification failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShipSafetyCheck_UnlistedIgnoredFileBlocks(t *testing.T) {
	tmp := t.TempDir()
	wt, md := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)

	// Create an ignored file that is NOT in the manifest.
	// Add it to .gitignore first.
	gitignorePath := filepath.Join(wt, ".gitignore")
	os.WriteFile(gitignorePath, []byte("*.ignored\n"), 0644)

	// Create an ignored file.
	os.WriteFile(filepath.Join(wt, "custom.ignored"), []byte("ignored content\n"), 0644)

	// The ignored file is not in the manifest. With --ignored=matching, git status
	// will show it. It should block.
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, metaWithManifest(wt, md), fakeTeardown{})
	if err == nil {
		t.Fatal("unlisted ignored file should block")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("unexpected error: %v", err)
	}
}