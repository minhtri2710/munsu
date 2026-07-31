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

// =============================================================================
// Manifest-based retirement verification tests
// =============================================================================

// setupWorktreeWithManifest creates a git repo with a remote and writes
// canonical launch artifacts including the manifest. Returns the worktree path.
func setupWorktreeWithManifest(t *testing.T, wt, remote string, briefContent []byte) {
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

	// Build manifest from actual file digests.
	entries := []ManifestEntry{}
	for _, name := range []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName} {
		entry, err := ManifestEntryForFile(wt, name, DisposalPolicyCleanable)
		if err != nil {
			t.Fatalf("manifest entry for %s: %v", name, err)
		}
		entries = append(entries, entry)
	}
	manifest := BuildManifest(entries)
	if err := WriteManifest(wt, manifest); err != nil {
		t.Fatalf("writing manifest: %v", err)
	}
}

func TestShipSafetyCheck_CanonicalManifest(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktree")
	remote := filepath.Join(tmp, "remote.git")

	briefContent := []byte("# Task: canonical\n\nCanonical brief.\n")
	setupWorktreeWithManifest(t, wt, remote, briefContent)

	meta := map[string]string{
		"worktree": wt,
		"kind":     "ship",
	}
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{})
	if err != nil {
		t.Fatalf("canonical manifest should pass: %v", err)
	}
}

func TestShipSafetyCheck_ModifiedManifestArtifact(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktree")
	remote := filepath.Join(tmp, "remote.git")

	briefContent := []byte("# Task: modified\n\nOriginal brief.\n")
	setupWorktreeWithManifest(t, wt, remote, briefContent)

	// Modify the brief file after the manifest was written.
	modifiedBrief := []byte("# Task: modified\n\nMODIFIED brief — digest mismatch.\n")
	os.WriteFile(filepath.Join(wt, BriefName), modifiedBrief, 0644)

	meta := map[string]string{
		"worktree": wt,
		"kind":     "ship",
	}
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{})
	if err == nil {
		t.Fatal("modified manifest artifact should block")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShipSafetyCheck_TrackedFileBlocks(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktree")
	remote := filepath.Join(tmp, "remote.git")

	briefContent := []byte("# Task: tracked\n\nBrief.\n")
	setupWorktreeWithManifest(t, wt, remote, briefContent)

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

	meta := map[string]string{
		"worktree": wt,
		"kind":     "ship",
	}
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{})
	if err == nil {
		t.Fatal("tracked modified file should block")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShipSafetyCheck_UnlistedFileBlocks(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktree")
	remote := filepath.Join(tmp, "remote.git")

	briefContent := []byte("# Task: unlisted\n\nBrief.\n")
	setupWorktreeWithManifest(t, wt, remote, briefContent)

	// Write an untracked file not in the manifest.
	os.WriteFile(filepath.Join(wt, "rogue.txt"), []byte("not a launch artifact\n"), 0644)

	meta := map[string]string{
		"worktree": wt,
		"kind":     "ship",
	}
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{})
	if err == nil {
		t.Fatal("unlisted untracked file should block")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShipSafetyCheck_LegacyMatchCleaned(t *testing.T) {
	if !LegacyBriefMigrationEnabled {
		t.Skip("legacy brief migration not enabled")
	}

	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktree")
	remote := filepath.Join(tmp, "remote.git")

	briefContent := []byte("# Task: legacy-match\n\nBrief content.\n")
	setupWorktreeWithManifest(t, wt, remote, briefContent)

	// Write legacy .soldier-md with content matching the canonical brief.
	os.WriteFile(filepath.Join(wt, ".soldier-md"), briefContent, 0644)

	meta := map[string]string{
		"worktree": wt,
		"kind":     "ship",
	}
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{})
	if err != nil {
		t.Fatalf("legacy .soldier-md matching brief digest should pass: %v", err)
	}
}

func TestShipSafetyCheck_LegacyMismatchBlocks(t *testing.T) {
	if !LegacyBriefMigrationEnabled {
		t.Skip("legacy brief migration not enabled")
	}

	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktree")
	remote := filepath.Join(tmp, "remote.git")

	briefContent := []byte("# Task: legacy-mismatch\n\nCanonical brief.\n")
	setupWorktreeWithManifest(t, wt, remote, briefContent)

	// Write legacy .soldier-md with DIFFERENT content.
	os.WriteFile(filepath.Join(wt, ".soldier-md"), []byte("# DIFFERENT brief\n\nNot matching.\n"), 0644)

	meta := map[string]string{
		"worktree": wt,
		"kind":     "ship",
	}
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{})
	if err == nil {
		t.Fatal("legacy .soldier-md not matching brief digest should block")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShipSafetyCheck_LegacyWithoutCanonicalBriefBlocks(t *testing.T) {
	if !LegacyBriefMigrationEnabled {
		t.Skip("legacy brief migration not enabled")
	}

	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktree")
	remote := filepath.Join(tmp, "remote.git")

	briefContent := []byte("# Task: legacy-no-canon\n\nBrief.\n")
	setupWorktreeWithManifest(t, wt, remote, briefContent)

	// Remove the canonical brief so there's no evidence to compare against.
	os.Remove(filepath.Join(wt, BriefName))

	// Write legacy .soldier-md.
	os.WriteFile(filepath.Join(wt, ".soldier-md"), briefContent, 0644)

	meta := map[string]string{
		"worktree": wt,
		"kind":     "ship",
	}
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{})
	if err == nil {
		t.Fatal("legacy .soldier-md without canonical brief evidence should block")
	}
}