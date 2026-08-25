//go:build integration

package fleet

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// setupGitRepo initializes a git repo in dir.
func setupGitRepo(t *testing.T, dir, remoteDir string) {
	t.Helper()

	// Block git from looking at parent directories
	gitEnv := append(os.Environ(),
		fmt.Sprintf("GIT_CEILING_DIRECTORIES=%s", dir),
	)

	// Init with consistent branch name (CI runners may default to master)
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = dir
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}

	// Configure
	for _, cfg := range []string{"user.email test@test.com", "user.name Test"} {
		parts := strings.Split(cfg, " ")
		c := exec.Command("git", append([]string{"config"}, parts...)...)
		c.Dir = dir
		c.Env = gitEnv
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git config %s: %s", cfg, out)
		}
	}

	// Initial commit
	readme := filepath.Join(dir, "README.md")
	os.WriteFile(readme, []byte("# test"), 0644)
	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = dir
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s", out)
	}
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = dir
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s", out)
	}

	// Detect the default branch name
	branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = dir
	branchCmd.Env = gitEnv
	branchOut, err := branchCmd.Output()
	if err != nil {
		t.Fatalf("detecting branch: %v", err)
	}
	defaultBranch := strings.TrimSpace(string(branchOut))

	// Set up remote if remoteDir provided
	if remoteDir != "" {
		// Init bare remote
		cmd = exec.Command("git", "init", "--bare", remoteDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init --bare: %s", out)
		}
		// Add remote
		cmd = exec.Command("git", "remote", "add", "origin", remoteDir)
		cmd.Dir = dir
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git remote add: %s", out)
		}
		// Push default branch
		cmd = exec.Command("git", "push", "-u", "origin", defaultBranch)
		cmd.Dir = dir
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git push: %s", out)
		}
	}
}

// setupRetirementTestManifest writes launch artifacts and manifest to the
// worktree. Returns the manifest digest for use in meta.
func setupRetirementTestManifest(t *testing.T, wt string) string {
	t.Helper()
	charter := DefaultCharter("retirement-test", "ship", "direct-PR")
	brief := []byte("# Retirement test brief\n")
	prompt := "prompt"
	os.WriteFile(filepath.Join(wt, CharterName), []byte(charter), 0644)
	os.WriteFile(filepath.Join(wt, BriefName), brief, 0644)
	os.WriteFile(filepath.Join(wt, PromptName), []byte(prompt), 0644)
	os.WriteFile(filepath.Join(wt, EnvelopeName), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(wt, LaunchScriptName), []byte("#!/bin/bash\n"), 0644)

	entries := []ManifestEntry{}
	for _, name := range []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName} {
		entry, err := ManifestEntryForFile(wt, name, DisposalPolicyCleanable)
		if err != nil {
			t.Fatalf("manifest entry for %s: %v", name, err)
		}
		entries = append(entries, entry)
	}
	manifest := BuildManifest(entries)
	digest, err := WriteManifest(wt, manifest)
	if err != nil {
		t.Fatalf("writing manifest: %v", err)
	}
	return digest
}

func TestShipSafetyCheck_CleanWithRemote(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktree")
	remote := filepath.Join(tmp, "remote.git")
	os.MkdirAll(wt, 0755)
	setupGitRepo(t, wt, remote)

	gitEnv := append(os.Environ(),
		fmt.Sprintf("GIT_CEILING_DIRECTORIES=%s", wt),
	)

	// Create a branch with upstream
	cmd := exec.Command("git", "checkout", "-b", "fm/test-branch")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %s", out)
	}

	// Push it
	cmd = exec.Command("git", "push", "-u", "origin", "fm/test-branch")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git push: %s", out)
	}

	// Set up manifest.
	md := setupRetirementTestManifest(t, wt)

	// Clean state should pass
	meta := map[string]string{
		"worktree":               wt,
		"kind":                   "ship",
		"launch_manifest_sha256": md,
	}
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{}, nil)
	if err != nil {
		t.Fatalf("shipSafetyCheck should pass for clean branch: %v", err)
	}
}

func TestShipSafetyCheck_OnlyKnownLaunchArtifactsDirty(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktree")
	remote := filepath.Join(tmp, "remote.git")
	os.MkdirAll(wt, 0755)
	setupGitRepo(t, wt, remote)

	gitEnv := append(os.Environ(),
		fmt.Sprintf("GIT_CEILING_DIRECTORIES=%s", wt),
	)

	// Create branch with upstream.
	cmd := exec.Command("git", "checkout", "-b", "fm/soldier-artifact-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %s", out)
	}
	cmd = exec.Command("git", "push", "-u", "origin", "fm/soldier-artifact-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git push: %s", out)
	}

	// Set up manifest (writes all launch artifacts).
	md := setupRetirementTestManifest(t, wt)

	meta := map[string]string{
		"worktree":               wt,
		"kind":                   "ship",
		"launch_manifest_sha256": md,
	}
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{}, nil)
	if err != nil {
		t.Fatalf("shipSafetyCheck should pass when only known launch artifacts are dirty: %v", err)
	}
}

func TestShipSafetyCheck_UnknownUntrackedFileDirty(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktree")
	remote := filepath.Join(tmp, "remote.git")
	os.MkdirAll(wt, 0755)
	setupGitRepo(t, wt, remote)

	gitEnv := append(os.Environ(),
		fmt.Sprintf("GIT_CEILING_DIRECTORIES=%s", wt),
	)

	// Create branch with upstream.
	cmd := exec.Command("git", "checkout", "-b", "fm/unknown-dirty-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %s", out)
	}
	cmd = exec.Command("git", "push", "-u", "origin", "fm/unknown-dirty-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git push: %s", out)
	}

	// Set up manifest (writes all launch artifacts).
	md := setupRetirementTestManifest(t, wt)

	// Write an unknown untracked file (should cause failure).
	os.WriteFile(filepath.Join(wt, "arbitrary.txt"), []byte("unknown\n"), 0644)

	meta := map[string]string{
		"worktree":               wt,
		"kind":                   "ship",
		"launch_manifest_sha256": md,
	}
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{}, nil)
	if err == nil {
		t.Fatal("shipSafetyCheck should fail when unknown untracked file exists alongside known artifacts")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShipSafetyCheck_MixedKnownAndUnknownDirty(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktree")
	remote := filepath.Join(tmp, "remote.git")
	os.MkdirAll(wt, 0755)
	setupGitRepo(t, wt, remote)

	gitEnv := append(os.Environ(),
		fmt.Sprintf("GIT_CEILING_DIRECTORIES=%s", wt),
	)

	// Create branch with upstream.
	cmd := exec.Command("git", "checkout", "-b", "fm/mixed-dirty-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %s", out)
	}
	cmd = exec.Command("git", "push", "-u", "origin", "fm/mixed-dirty-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git push: %s", out)
	}

	// Set up manifest (writes all launch artifacts).
	md := setupRetirementTestManifest(t, wt)

	// Write an unknown untracked file.
	os.WriteFile(filepath.Join(wt, "rogue.txt"), []byte("rogue\n"), 0644)

	meta := map[string]string{
		"worktree":               wt,
		"kind":                   "ship",
		"launch_manifest_sha256": md,
	}
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{}, nil)
	if err == nil {
		t.Fatal("shipSafetyCheck should fail when unknown untracked file coexists with known artifacts")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParsedPorcelainFilename(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"?? .soldier-charter.md", ".soldier-charter.md"},
		{" M main.go", "main.go"},
		{"A  newfile.txt", "newfile.txt"},
		{"?? \"file with spaces.go\"", "file with spaces.go"},
		{"XY", ""},
		{"", ""},
		{" M dir/nested/file.go", "dir/nested/file.go"},
	}
	for _, tc := range tests {
		got := parsePorcelainFilename(tc.line)
		if got != tc.want {
			t.Errorf("parsePorcelainFilename(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestShipSafetyCheck_OnlyLaunchScriptDirty(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktree")
	remote := filepath.Join(tmp, "remote.git")
	os.MkdirAll(wt, 0755)
	setupGitRepo(t, wt, remote)

	gitEnv := append(os.Environ(),
		fmt.Sprintf("GIT_CEILING_DIRECTORIES=%s", wt),
	)

	// Create branch with upstream.
	cmd := exec.Command("git", "checkout", "-b", "fm/launch-script-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %s", out)
	}
	cmd = exec.Command("git", "push", "-u", "origin", "fm/launch-script-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git push: %s", out)
	}

	// Set up manifest (writes all launch artifacts including launch script).
	md := setupRetirementTestManifest(t, wt)

	meta := map[string]string{
		"worktree":               wt,
		"kind":                   "ship",
		"launch_manifest_sha256": md,
	}
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{}, nil)
	if err != nil {
		t.Fatalf("shipSafetyCheck should pass when only launch artifacts are dirty: %v", err)
	}
}

func TestShipSafetyCheck_NoRemoteBranch(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktree")
	os.MkdirAll(wt, 0755)
	setupGitRepo(t, wt, "") // no remote

	meta := map[string]string{
		"worktree": wt,
		"kind":     "ship",
	}
	_, err := shipSafetyCheck(Options{}, meta, fakeTeardown{}, nil)
	if err == nil {
		t.Fatal("shipSafetyCheck should fail without remote")
	}
}

func TestShipSafetyCheck_NoWorktreeInMeta(t *testing.T) {
	_, err := shipSafetyCheck(Options{ID: "test"}, map[string]string{}, fakeTeardown{}, nil)
	if err == nil {
		t.Fatal("should fail when no worktree in meta")
	}
}

func TestScoutSafetyCheck_ReportExists(t *testing.T) {
	tmp := t.TempDir()

	// Create the report
	reportDir := filepath.Join(tmp, "data", "scout-1")
	os.MkdirAll(reportDir, 0755)
	os.WriteFile(filepath.Join(reportDir, "report.md"), []byte("findings"), 0644)

	err := scoutSafetyCheck(Options{ID: "scout-1", HomeDir: tmp}, nil)
	if err != nil {
		t.Fatalf("should pass when report exists: %v", err)
	}
}

func TestScoutSafetyCheck_NoReport(t *testing.T) {
	tmp := t.TempDir()

	err := scoutSafetyCheck(Options{ID: "scout-1", HomeDir: tmp}, nil)
	if err == nil {
		t.Fatal("should fail when no report.md")
	}
}

func TestRun_NoMeta(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("MUNSU_HOME", tmp)
	defer os.Unsetenv("MUNSU_HOME")

	_, err := RetireTask(Options{HomeDir: tmp, ID: "nonexistent"}, fakeTeardown{}, fakeRetirementJournals{}, canonicalMergeTestAuth(t, tmp, "nonexistent"))
	if err == nil {
		t.Fatal("should fail for nonexistent task")
	}
}

func TestRun_ForceSkipsSafety(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("MUNSU_HOME", tmp)
	defer os.Unsetenv("MUNSU_HOME")

	// Compose the canonical Authority and task before the projection write so
	// the home is initialized first.
	auth := canonicalMergeTestAuth(t, tmp, "nonexistent")

	// Create a minimal meta file
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	metaContent := "kind=scout\nbackend=tmux\nwindow=@1\n"
	os.WriteFile(filepath.Join(stateDir, "nonexistent.meta"), []byte(metaContent), 0644)

	// With --force, it should try to proceed (will fail at session/return steps but not at safety)
	result, err := RetireTask(Options{HomeDir: tmp, ID: "nonexistent", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth)
	if err != nil {
		t.Fatalf("with --force should not fail at safety: %v", err)
	}
	if len(result.Steps) == 0 {
		t.Error("expected some teardown steps")
	}
}

func TestRun_ForceScoutWithoutReport(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("MUNSU_HOME", tmp)
	defer os.Unsetenv("MUNSU_HOME")

	auth := canonicalMergeTestAuth(t, tmp, "scout-test")

	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	metaContent := "kind=scout\nbackend=tmux\nwindow=@1\n"
	os.WriteFile(filepath.Join(stateDir, "scout-test.meta"), []byte(metaContent), 0644)

	// Without --force, should fail
	_, err := RetireTask(Options{HomeDir: tmp, ID: "scout-test", Force: false}, fakeTeardown{}, fakeRetirementJournals{}, auth)
	if err == nil {
		t.Fatal("should fail for scout without report without --force")
	}

	// With --force, should proceed
	result, err := RetireTask(Options{HomeDir: tmp, ID: "scout-test", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth)
	if err != nil {
		t.Fatalf("with --force should proceed: %v", err)
	}
	if len(result.Steps) == 0 {
		t.Error("expected teardown steps")
	}
}

func TestRun_ForcePreservesReport(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("MUNSU_HOME", tmp)
	defer os.Unsetenv("MUNSU_HOME")

	auth := canonicalMergeTestAuth(t, tmp, "scout-report")

	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "scout-report.meta"), []byte("kind=scout\nbackend=tmux\nwindow=@1\n"), 0644)

	dataDir := filepath.Join(tmp, "data", "scout-report")
	os.MkdirAll(dataDir, 0755)
	reportPath := filepath.Join(dataDir, "report.md")
	os.WriteFile(reportPath, []byte("# findings\npartial work worth reading\n"), 0644)

	result, err := RetireTask(Options{HomeDir: tmp, ID: "scout-report", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth)
	if err != nil {
		t.Fatalf("forced teardown: %v", err)
	}
	archivedPath := filepath.Join(dataDir, "report-g1.md")
	body, err := os.ReadFile(archivedPath)
	if err != nil {
		t.Fatalf("--force must not delete the report: %v", err)
	}
	if !strings.Contains(string(body), "partial work worth reading") {
		t.Fatalf("archived report = %q, want the retired generation's findings", body)
	}
	if !hasStep(result.Steps, "report.md archived as report-g1.md") {
		t.Fatalf("steps = %v, want the report-archived step", result.Steps)
	}
	if !hasStep(result.Steps, "data dir kept for relaunch or session-start sweep") {
		t.Fatalf("steps = %v, want the data-dir-kept step", result.Steps)
	}

	// The report belongs to generation 1 and stops answering for any other:
	// scoutSafetyCheck reads report.md, and only the generation that writes
	// one has it there.
	if _, err := os.Stat(reportPath); !os.IsNotExist(err) {
		t.Fatalf("report.md must not survive under the name the next generation writes, stat err = %v", err)
	}
	if err := scoutSafetyCheck(Options{HomeDir: tmp, ID: "scout-report"}, map[string]string{"kind": "scout"}); err == nil {
		t.Fatal("scoutSafetyCheck must refuse a generation that wrote no report of its own")
	}

	// The retirement itself committed, so the report survives a teardown that
	// went all the way through cleanup — not merely one that stopped early.
	if !hasStep(result.Steps, "cleanup claim completed for generation 1") {
		t.Fatalf("steps = %v, want the cleanup claim to have completed", result.Steps)
	}
}

func TestRun_ForcePreservesBriefForSweep(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("MUNSU_HOME", tmp)
	defer os.Unsetenv("MUNSU_HOME")

	auth := canonicalMergeTestAuth(t, tmp, "stub-brief")

	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "stub-brief.meta"), []byte("kind=scout\nbackend=tmux\nwindow=@1\n"), 0644)

	dataDir := filepath.Join(tmp, "data", "stub-brief")
	os.MkdirAll(dataDir, 0755)
	os.WriteFile(filepath.Join(dataDir, "brief.md"), []byte("stub\n"), 0644)

	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(dataDir, old, old); err != nil {
		t.Fatalf("age data dir: %v", err)
	}
	if _, err := RetireTask(Options{HomeDir: tmp, ID: "stub-brief", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("forced teardown: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "brief.md")); err != nil {
		t.Fatalf("forced teardown must preserve the brief: %v", err)
	}
	info, err := os.Stat(dataDir)
	if err != nil {
		t.Fatalf("preserved data dir: %v", err)
	}
	if time.Since(info.ModTime()) > time.Minute {
		t.Fatalf("preserved data dir mtime = %v, want refreshed at teardown", info.ModTime())
	}
}

func hasStep(steps []string, want string) bool {
	for _, s := range steps {
		if s == want {
			return true
		}
	}
	return false
}

func TestRun_RemovesResidualArtifacts(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("MUNSU_HOME", tmp)
	defer os.Unsetenv("MUNSU_HOME")

	auth := canonicalMergeTestAuth(t, tmp, "test-residual")

	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Create meta file with harness=pi so adapter-driven artifacts are included
	metaContent := "kind=scout\nbackend=tmux\nwindow=@1\nharness=pi\n"
	os.WriteFile(filepath.Join(stateDir, "test-residual.meta"), []byte(metaContent), 0644)

	// Create residual artifacts: munsu-native (both old and new names) + pi adapter artifacts
	residuals := []string{
		"test-residual.status",
		"test-residual.check",      // new canonical name
		"test-residual.check.sh",   // legacy name (dual-read)
		"test-residual.turnend",    // new canonical name
		"test-residual.turn-ended", // legacy name (dual-read)
		"test-residual.pi-ext.ts",
	}
	for _, name := range residuals {
		os.WriteFile(filepath.Join(stateDir, name), []byte("stale"), 0644)
	}

	// Run teardown with --force to skip safety
	result, err := RetireTask(Options{HomeDir: tmp, ID: "test-residual", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth)
	if err != nil {
		t.Fatalf("teardown should not fail: %v", err)
	}

	// Verify residual files are removed
	for _, name := range residuals {
		path := filepath.Join(stateDir, name)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("residual %s should have been removed, but still exists", name)
		}
	}

	// Verify steps mention residual removal
	foundResidual := false
	for _, step := range result.Steps {
		if strings.Contains(step, "residual") {
			foundResidual = true
			break
		}
	}
	if !foundResidual {
		t.Errorf("expected teardown steps to mention residual removal, got: %v", result.Steps)
	}
}

func TestRun_BackwardCompatLegacyNames(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("MUNSU_HOME", tmp)
	defer os.Unsetenv("MUNSU_HOME")

	auth := canonicalMergeTestAuth(t, tmp, "legacy-test")

	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Create meta file (harness=pi to include adapter artifacts)
	metaContent := "kind=scout\nbackend=tmux\nwindow=@1\nharness=pi\n"
	os.WriteFile(filepath.Join(stateDir, "legacy-test.meta"), []byte(metaContent), 0644)

	// Munsu-native artifacts: new canonical names (post item-5 rename)
	munsuNames := []string{
		"legacy-test.status",
		"legacy-test.check",   // new canonical name
		"legacy-test.turnend", // new canonical name
	}
	// Legacy names still being cleaned up (dual-read window)
	legacyNames := []string{
		"legacy-test.check.sh",   // legacy name (deprecated)
		"legacy-test.turn-ended", // legacy name (deprecated)
	}
	// Harness-specific artifact
	harnessNames := []string{
		"legacy-test.pi-ext.ts",
	}

	allResiduals := append(append(munsuNames, legacyNames...), harnessNames...)
	for _, name := range allResiduals {
		os.WriteFile(filepath.Join(stateDir, name), []byte("stale"), 0644)
	}

	// Run teardown with --force
	result, err := RetireTask(Options{HomeDir: tmp, ID: "legacy-test", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth)
	if err != nil {
		t.Fatalf("teardown should not fail: %v", err)
	}

	// All residuals should be removed
	for _, name := range allResiduals {
		path := filepath.Join(stateDir, name)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("residual %s should have been removed, but still exists", name)
		}
	}

	// Verify steps mention residual removal
	foundResidual := false
	for _, step := range result.Steps {
		if strings.Contains(step, "residual") {
			foundResidual = true
			break
		}
	}
	if !foundResidual {
		t.Errorf("expected teardown steps to mention residual removal, got: %v", result.Steps)
	}
}
func TestScoutSafetyCheck_UnresolvedHolds(t *testing.T) {
	tmp := t.TempDir()

	// Create the report so report.md check passes.
	reportDir := filepath.Join(tmp, "data", "scout-1")
	os.MkdirAll(reportDir, 0755)
	os.WriteFile(filepath.Join(reportDir, "report.md"), []byte("findings"), 0644)

	// Create unresolved decision holds by appending the needs-decision status
	// projection the decision-hold lifecycle mirrors into.
	if err := mhome.AppendStatus(tmp, "scout-1", "needs-decision: Pick the UI framework [key=approach]"); err != nil {
		t.Fatal(err)
	}
	if err := mhome.AppendStatus(tmp, "scout-1", "needs-decision: Choose DB schema [key=db-schema]"); err != nil {
		t.Fatal(err)
	}

	// scoutSafetyCheck should fail listing unresolved keys.
	err := scoutSafetyCheck(Options{ID: "scout-1", HomeDir: tmp}, nil)
	if err == nil {
		t.Fatal("should fail when unresolved decision holds exist")
	}
	if !strings.Contains(err.Error(), "approach") || !strings.Contains(err.Error(), "db-schema") {
		t.Errorf("error should list unresolved keys, got: %v", err)
	}
	if !strings.Contains(err.Error(), "(use --force to override)") {
		t.Errorf("error should mention --force override, got: %v", err)
	}
}

func TestScoutSafetyCheck_NoUnresolvedHolds(t *testing.T) {
	tmp := t.TempDir()

	// Create the report so report.md check passes.
	reportDir := filepath.Join(tmp, "data", "scout-1")
	os.MkdirAll(reportDir, 0755)
	os.WriteFile(filepath.Join(reportDir, "report.md"), []byte("findings"), 0644)

	// Create a hold and resolve it through the status projection.
	if err := mhome.AppendStatus(tmp, "scout-1", "needs-decision: Pick the UI framework [key=approach]"); err != nil {
		t.Fatal(err)
	}
	if err := mhome.AppendStatus(tmp, "scout-1", "resolved: Choose React [key=approach]"); err != nil {
		t.Fatal(err)
	}

	// scoutSafetyCheck should pass since all holds are resolved.
	err := scoutSafetyCheck(Options{ID: "scout-1", HomeDir: tmp}, nil)
	if err != nil {
		t.Fatalf("should pass when all holds resolved: %v", err)
	}
}

func TestRun_ForceSkipsDecisionHoldCheck(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("MUNSU_HOME", tmp)
	defer os.Unsetenv("MUNSU_HOME")

	auth := canonicalMergeTestAuth(t, tmp, "scout-test")

	// Create meta file for a scout task.
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	metaContent := "kind=scout\nbackend=tmux\nwindow=@1\n"
	os.WriteFile(filepath.Join(stateDir, "scout-test.meta"), []byte(metaContent), 0644)

	// Create report.md so report check passes before decision hold check.
	reportDir := filepath.Join(tmp, "data", "scout-test")
	os.MkdirAll(reportDir, 0755)
	os.WriteFile(filepath.Join(reportDir, "report.md"), []byte("findings"), 0644)
	// Create unresolved decision holds via the status projection.
	if err := mhome.AppendStatus(tmp, "scout-test", "needs-decision: Pick the UI framework [key=approach]"); err != nil {
		t.Fatal(err)
	}

	// Without --force, should fail due to unresolved holds.
	_, err := RetireTask(Options{HomeDir: tmp, ID: "scout-test", Force: false}, fakeTeardown{}, fakeRetirementJournals{}, auth)
	if err == nil {
		t.Fatal("should fail for scout with unresolved holds without --force")
	}
	if !strings.Contains(err.Error(), "unresolved decision hold") {
		t.Errorf("error should mention unresolved decision holds, got: %v", err)
	}

	// With --force, should proceed past safety checks.
	result, err := RetireTask(Options{HomeDir: tmp, ID: "scout-test", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth)
	if err != nil {
		t.Fatalf("with --force should proceed: %v", err)
	}
	if len(result.Steps) == 0 {
		t.Error("expected teardown steps")
	}
	if len(result.Steps) == 0 {
		t.Error("expected teardown steps")
	}
}

// TestRun_ArchivingTheReportFailsClosed proves the archive is not advisory.
// Completing the cleanup claim is what makes a task reopenable, so a report
// still sitting at the name the next generation writes must never reach that
// commit: the retirement stops at the typed partial outcome instead.
func TestRun_ReportStatFailsClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("permission proof did not run: tests run as root, so a child stat refusal cannot be established")
	}
	tmp := t.TempDir()
	auth := canonicalMergeTestAuth(t, tmp, "report-stat")
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "report-stat.meta"), []byte("kind=scout\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(tmp, "data", "report-stat")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(dataDir, "report.md")
	if err := os.WriteFile(reportPath, []byte("# findings\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dataDir, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dataDir, 0755) })

	_, err := RetireTask(Options{HomeDir: tmp, ID: "report-stat", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth)
	var pending *RetirementCleanupPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("teardown error = %T %v, want typed RetirementCleanupPendingError", err, err)
	}
	if err := os.Chmod(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatalf("a stat refusal must leave the report where it was: %v", err)
	}
}

func TestRun_DataPathFileFailsClosed(t *testing.T) {
	tmp := t.TempDir()
	auth := canonicalMergeTestAuth(t, tmp, "data-file")
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "data-file.meta"), []byte("kind=scout\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(tmp, "data", "data-file")
	if err := os.MkdirAll(filepath.Dir(dataDir), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataDir, []byte("corrupt"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := RetireTask(Options{HomeDir: tmp, ID: "data-file", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth)
	var pending *RetirementCleanupPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("teardown error = %T %v, want typed RetirementCleanupPendingError", err, err)
	}
	body, readErr := os.ReadFile(dataDir)
	if readErr != nil || string(body) != "corrupt" {
		t.Fatalf("data path changed after refusal: body=%q err=%v", body, readErr)
	}
}

func TestRun_DataParentStatFailsClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("permission proof did not run: tests run as root, so a data-parent stat refusal cannot be established")
	}
	tmp := t.TempDir()
	auth := canonicalMergeTestAuth(t, tmp, "data-parent")
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "data-parent.meta"), []byte("kind=scout\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dataRoot := filepath.Join(tmp, "data")
	dataDir := filepath.Join(dataRoot, "data-parent")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "report.md"), []byte("findings"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dataRoot, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dataRoot, 0755) })

	_, err := RetireTask(Options{HomeDir: tmp, ID: "data-parent", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth)
	var pending *RetirementCleanupPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("teardown error = %T %v, want typed RetirementCleanupPendingError", err, err)
	}
	if err := os.Chmod(dataRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "report.md")); err != nil {
		t.Fatalf("report should remain after data-parent refusal: %v", err)
	}
}

func TestRun_ArchiveConflictFailsClosed(t *testing.T) {
	tmp := t.TempDir()
	auth := canonicalMergeTestAuth(t, tmp, "archive-conflict")
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "archive-conflict.meta"), []byte("kind=scout\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(tmp, "data", "archive-conflict")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(dataDir, "report.md")
	archivePath := filepath.Join(dataDir, "report-g1.md")
	if err := os.WriteFile(reportPath, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := RetireTask(Options{HomeDir: tmp, ID: "archive-conflict", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth)
	var pending *RetirementCleanupPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("teardown error = %T %v, want typed RetirementCleanupPendingError", err, err)
	}
	if !strings.Contains(err.Error(), reportPath) || !strings.Contains(err.Error(), archivePath) {
		t.Fatalf("conflict error = %v, want both report paths", err)
	}
	for path, want := range map[string]string{reportPath: "new", archivePath: "old"} {
		body, readErr := os.ReadFile(path)
		if readErr != nil || string(body) != want {
			t.Fatalf("%s changed after conflict: body=%q err=%v", path, body, readErr)
		}
	}
}

func TestRun_DanglingReportIsArchived(t *testing.T) {
	tmp := t.TempDir()
	auth := canonicalMergeTestAuth(t, tmp, "dangling-report")
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "dangling-report.meta"), []byte("kind=scout\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(tmp, "data", "dangling-report")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(dataDir, "report.md")
	archivePath := filepath.Join(dataDir, "report-g1.md")
	if err := os.Symlink("missing-target", reportPath); err != nil {
		t.Fatal(err)
	}

	if _, err := RetireTask(Options{HomeDir: tmp, ID: "dangling-report", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("forced teardown: %v", err)
	}
	if _, err := os.Lstat(reportPath); !os.IsNotExist(err) {
		t.Fatalf("report.md entry should be vacated, lstat err = %v", err)
	}
	if target, err := os.Readlink(archivePath); err != nil || target != "missing-target" {
		t.Fatalf("archived symlink target = %q, err = %v", target, err)
	}
}

func TestRun_ArchiveConflictLeavesReportUntouched(t *testing.T) {
	tmp := t.TempDir()
	auth := canonicalMergeTestAuth(t, tmp, "rename-failure")
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "rename-failure.meta"), []byte("kind=scout\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(tmp, "data", "rename-failure")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(dataDir, "report.md")
	archivePath := filepath.Join(dataDir, "report-g1.md")
	if err := os.WriteFile(reportPath, []byte("current"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath, []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := RetireTask(Options{HomeDir: tmp, ID: "rename-failure", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth)
	var pending *RetirementCleanupPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("teardown error = %T %v, want typed RetirementCleanupPendingError", err, err)
	}
	body, readErr := os.ReadFile(reportPath)
	if readErr != nil || string(body) != "current" {
		t.Fatalf("report changed: %q %v", body, readErr)
	}
	body, readErr = os.ReadFile(archivePath)
	if readErr != nil || string(body) != "existing" {
		t.Fatalf("archive changed: %q %v", body, readErr)
	}
}

func TestRun_AbortRefusesLiveEndpoint(t *testing.T) {
	tmp := t.TempDir()
	auth := mergeTestAuth(t, tmp, "abort-live")
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "abort-live.meta"), []byte("kind=ship\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(tmp, "data", "abort-live")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "report.md"), []byte("findings"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := RetireTask(Options{HomeDir: tmp, ID: "abort-live", Force: true}, fakeTeardown{alive: true, disposeErr: errors.New("stuck")}, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected cleanup to remain pending")
	}
	err := AbortRetirementCleanup(auth, tmp, fakeTeardown{alive: true}, mustTaskID(t, "abort-live"), 1)
	if err == nil || !strings.Contains(err.Error(), "not authoritatively absent") {
		t.Fatalf("abort error = %v, want live-endpoint refusal", err)
	}
	agg, err := auth.Get(mustTaskID(t, "abort-live"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != taskauthority.CleanupActive {
		t.Fatalf("cleanup claim = %+v, want active", agg.CleanupClaim)
	}
}

func TestRun_AbortRefusesReportReappearance(t *testing.T) {
	tmp := t.TempDir()
	auth := canonicalMergeTestAuth(t, tmp, "abort-reappears")
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(tmp, "data", "abort-reappears")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "report.md"), []byte("findings"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "abort-reappears.meta"), []byte("kind=scout\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dataDir, 0555); err != nil {
		t.Fatal(err)
	}
	if _, err := RetireTask(Options{HomeDir: tmp, ID: "abort-reappears", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected cleanup to remain pending")
	}
	if err := os.Chmod(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	claimID := mustTaskID(t, "abort-reappears")
	called := false
	err := abortRetirementCleanup(auth, tmp, fakeTeardown{}, claimID, 1, func() error {
		called = true
		return os.WriteFile(filepath.Join(dataDir, "report.md"), []byte("late"), 0644)
	})
	if err == nil || !strings.Contains(err.Error(), "reappeared") {
		t.Fatalf("abort error = %v, want report reappearance refusal", err)
	}
	if !called {
		t.Fatal("after-archive callback did not run")
	}
	agg, err := auth.Get(claimID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != taskauthority.CleanupActive {
		t.Fatalf("cleanup claim = %+v, want active", agg.CleanupClaim)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "report-g1.md")); err != nil {
		t.Fatalf("archived evidence missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "report.md")); err != nil {
		t.Fatalf("late report missing: %v", err)
	}
}

func TestRun_ArchivingTheReportFailsClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("permission proof did not run: tests run as root, so a directory that refuses a rename cannot be established")
	}
	tmp := t.TempDir()
	os.Setenv("MUNSU_HOME", tmp)
	defer os.Unsetenv("MUNSU_HOME")

	auth := canonicalMergeTestAuth(t, tmp, "unarchivable")

	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "unarchivable.meta"), []byte("kind=scout\nbackend=tmux\nwindow=@1\n"), 0644)

	dataDir := filepath.Join(tmp, "data", "unarchivable")
	os.MkdirAll(dataDir, 0755)
	reportPath := filepath.Join(dataDir, "report.md")
	os.WriteFile(reportPath, []byte("# findings\n"), 0644)
	if err := os.Chmod(dataDir, 0555); err != nil {
		t.Fatalf("permission proof did not run: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dataDir, 0755) })

	_, err := RetireTask(Options{HomeDir: tmp, ID: "unarchivable", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth)
	var pending *RetirementCleanupPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("teardown error = %T %v, want typed RetirementCleanupPendingError", err, err)
	}
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatalf("a refused archive must leave the report where it was: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dataDir, "report-g1.md")); !os.IsNotExist(err) {
		t.Fatalf("a refused archive must not leave a reservation, lstat err = %v", err)
	}
}

type failingRetirementJournals struct{}

func (failingRetirementJournals) VerifyRetirementContinuity(string, string) error { return nil }
func (failingRetirementJournals) PrepareForcedRetirementEvidence(string, string) ([]string, error) {
	return nil, nil
}
func (failingRetirementJournals) FinalizeRetirementJournals(string, string) ([]string, error) {
	return nil, errors.New("journal finalization failed")
}

func TestRun_RetryAfterJournalFailureKeepsMeta(t *testing.T) {
	tmp := t.TempDir()
	taskID := "retry-journal"
	auth := canonicalMergeTestAuth(t, tmp, taskID)
	metaPath := filepath.Join(tmp, "state", taskID+".meta")
	if err := os.MkdirAll(filepath.Dir(metaPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metaPath, []byte("kind=scout\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, failingRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup")
	}
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("meta removed: %v", err)
	}
	if _, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Fatalf("meta remains after retry: %v", err)
	}
	claim, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if claim.CleanupClaim == nil || claim.CleanupClaim.Status != taskauthority.CleanupCompleted {
		t.Fatalf("claim = %+v", claim.CleanupClaim)
	}
}

func TestRun_ReportReappearsBeforeCleanupCommit(t *testing.T) {
	tmp := t.TempDir()
	taskID := "report-reappears"
	auth := canonicalMergeTestAuth(t, tmp, taskID)
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, taskID+".meta"), []byte("kind=scout\\nbackend=tmux\\nwindow=@1\\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(tmp, "data", taskID)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "report.md"), []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	oldHook := afterReportArchive
	afterReportArchive = func(home, id string, _ taskauthority.Generation) error {
		return os.WriteFile(filepath.Join(home, "data", id, "report.md"), []byte("late"), 0644)
	}
	t.Cleanup(func() { afterReportArchive = oldHook })
	_, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth)
	var pending *RetirementCleanupPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("error = %T %v, want pending cleanup", err, err)
	}
	body, readErr := os.ReadFile(filepath.Join(dataDir, "report-g1.md"))
	if readErr != nil || string(body) != "original" {
		t.Fatalf("archived report = %q, %v", body, readErr)
	}
	body, readErr = os.ReadFile(filepath.Join(dataDir, "report.md"))
	if readErr != nil || string(body) != "late" {
		t.Fatalf("recreated report = %q, %v", body, readErr)
	}
	claim, getErr := auth.Get(mustTaskID(t, taskID))
	if getErr != nil {
		t.Fatal(getErr)
	}
	if claim.CleanupClaim == nil || claim.CleanupClaim.Status != taskauthority.CleanupActive {
		t.Fatalf("claim = %+v, want active", claim.CleanupClaim)
	}
}

func TestRun_RetryAfterArchiveFailureKeepsMeta(t *testing.T) {
	tmp := t.TempDir()
	taskID := "retry-meta"
	auth := canonicalMergeTestAuth(t, tmp, taskID)
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(stateDir, taskID+".meta")
	if err := os.WriteFile(metaPath, []byte("kind=scout\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(tmp, "data", taskID)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "report.md"), []byte("findings"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "report-g1.md"), []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}
	first := &recordingTeardown{alive: true}
	if _, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, first, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected cleanup failure")
	} else {
		var pending *RetirementCleanupPendingError
		if !errors.As(err, &pending) {
			t.Fatalf("error = %T %v, want pending cleanup", err, err)
		}
	}
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("meta removed after failed cleanup: %v", err)
	}
	claim, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if claim.CleanupClaim == nil || claim.CleanupClaim.Status != taskauthority.CleanupActive {
		t.Fatalf("claim = %+v, want active", claim.CleanupClaim)
	}
	if err := os.Remove(filepath.Join(dataDir, "report-g1.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, first, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Fatalf("meta remains after retry: %v", err)
	}
	claim, err = auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if claim.CleanupClaim == nil || claim.CleanupClaim.Status != taskauthority.CleanupCompleted {
		t.Fatalf("claim after retry = %+v, want completed", claim.CleanupClaim)
	}
	body, err := os.ReadFile(filepath.Join(dataDir, "report-g1.md"))
	if err != nil || string(body) != "findings" {
		t.Fatalf("archived report = %q, err=%v", body, err)
	}
}

func TestRun_RetryAfterUnknownArchiveStillRefuses(t *testing.T) {
	tmp := t.TempDir()
	taskID := "retry-unknown-archive"
	auth := canonicalMergeTestAuth(t, tmp, taskID)
	if err := os.WriteFile(filepath.Join(tmp, "state", taskID+".meta"), []byte("kind=ship\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(tmp, "data", taskID)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "report.md"), []byte("current"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "report-g1.md"), []byte("foreign"), 0644); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth); err == nil {
			t.Fatal("expected cleanup refusal")
		}
	}
	for name, want := range map[string]string{"report.md": "current", "report-g1.md": "foreign"} {
		body, err := os.ReadFile(filepath.Join(dataDir, name))
		if err != nil || string(body) != want {
			t.Fatalf("%s = %q, %v", name, body, err)
		}
	}
	claim, err := auth.Get(mustTaskID(t, taskID))
	if err != nil || claim.CleanupClaim == nil || claim.CleanupClaim.Status != taskauthority.CleanupActive {
		t.Fatalf("claim = %+v, err=%v, want active", claim.CleanupClaim, err)
	}
}

func TestRun_RetryAfterStragglerReportArchivesRecovery(t *testing.T) {
	tmp := t.TempDir()
	taskID := "retry-straggler"
	auth := canonicalMergeTestAuth(t, tmp, taskID)
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, taskID+".meta"), []byte("kind=ship\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(tmp, "data", taskID)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "report.md"), []byte("first"), 0644); err != nil {
		t.Fatal(err)
	}
	oldHook := afterReportArchive
	firstArchive := true
	afterReportArchive = func(home, id string, _ taskauthority.Generation) error {
		if !firstArchive {
			return nil
		}
		firstArchive = false
		return os.WriteFile(filepath.Join(home, "data", id, "report.md"), []byte("straggler"), 0644)
	}
	t.Cleanup(func() { afterReportArchive = oldHook })
	if _, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup")
	}
	if _, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("retry: %v", err)
	}
	claim, err := auth.Get(mustTaskID(t, taskID))
	if err != nil || claim.CleanupClaim == nil || claim.CleanupClaim.Status != taskauthority.CleanupCompleted {
		t.Fatalf("claim = %+v, err=%v, want completed", claim.CleanupClaim, err)
	}
	for name, want := range map[string]string{"report-g1.md": "first", "report-g1-2.md": "straggler"} {
		body, err := os.ReadFile(filepath.Join(dataDir, name))
		if err != nil || string(body) != want {
			t.Fatalf("%s = %q, %v", name, body, err)
		}
	}
}

func TestRun_AbortAfterStragglerReportArchivesRecovery(t *testing.T) {
	tmp := t.TempDir()
	taskID := "abort-straggler"
	auth := canonicalMergeTestAuth(t, tmp, taskID)
	if err := os.WriteFile(filepath.Join(tmp, "state", taskID+".meta"), []byte("kind=ship\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(tmp, "data", taskID)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "report.md"), []byte("first"), 0644); err != nil {
		t.Fatal(err)
	}
	oldHook := afterReportArchive
	firstArchive := true
	afterReportArchive = func(home, id string, _ taskauthority.Generation) error {
		if !firstArchive {
			return nil
		}
		firstArchive = false
		return os.WriteFile(filepath.Join(home, "data", id, "report.md"), []byte("straggler"), 0644)
	}
	t.Cleanup(func() { afterReportArchive = oldHook })
	if _, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup")
	}
	if err := AbortRetirementCleanup(auth, tmp, fakeTeardown{}, mustTaskID(t, taskID), 1); err != nil {
		t.Fatalf("abort: %v", err)
	}
	for name, want := range map[string]string{"report-g1.md": "first", "report-g1-2.md": "straggler"} {
		body, err := os.ReadFile(filepath.Join(dataDir, name))
		if err != nil || string(body) != want {
			t.Fatalf("%s = %q, %v", name, body, err)
		}
	}
	claim, err := auth.Get(mustTaskID(t, taskID))
	if err != nil || claim.CleanupClaim == nil || claim.CleanupClaim.Status != taskauthority.CleanupAborted {
		t.Fatalf("claim = %+v, err=%v, want aborted", claim.CleanupClaim, err)
	}
}

func TestRun_ProjectionFailureRetriesWhileRetired(t *testing.T) {
	tmp := t.TempDir()
	taskID := "projection-retry"
	auth := canonicalMergeTestAuth(t, tmp, taskID)
	if err := os.WriteFile(filepath.Join(tmp, "state", taskID+".meta"), []byte("kind=ship\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(tmp, "state", taskID+".status")
	if err := os.Mkdir(statusPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(statusPath, "child"), []byte("blocked"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth)
	var projectionErr *RetirementProjectionError
	if !errors.As(err, &projectionErr) {
		t.Fatalf("error = %T %v, want projection error", err, err)
	}
	claim, err := auth.Get(mustTaskID(t, taskID))
	if err != nil || claim.CleanupClaim == nil || claim.CleanupClaim.Status != taskauthority.CleanupCompleted {
		t.Fatalf("claim = %+v, err=%v, want completed", claim.CleanupClaim, err)
	}
	if err := os.Remove(filepath.Join(statusPath, "child")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(statusPath); err != nil {
		t.Fatal(err)
	}
	if _, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestRun_ProjectionFailureThenReopenIsSuperseded(t *testing.T) {
	tmp := t.TempDir()
	taskID := "projection-superseded"
	auth := canonicalMergeTestAuth(t, tmp, taskID)
	metaPath := filepath.Join(tmp, "state", taskID+".meta")
	statusPath := filepath.Join(tmp, "state", taskID+".status")
	if err := os.WriteFile(metaPath, []byte("kind=ship\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(statusPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(statusPath, "child"), []byte("blocked"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth)
	var projectionErr *RetirementProjectionError
	if !errors.As(err, &projectionErr) {
		t.Fatalf("error = %T %v, want projection error", err, err)
	}
	if err := os.Remove(filepath.Join(statusPath, "child")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(statusPath); err != nil {
		t.Fatal(err)
	}
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	reopen := taskauthority.CanonicalReopenRequest{HomeID: auth.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), Reason: "reopen"}
	opID, err := domain.NewOperationID("reopen-projection-superseded")
	if err != nil {
		t.Fatal(err)
	}
	op, err := domain.NewOperation(opID, reopen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Reopen(op, reopen); err != nil {
		t.Fatal(err)
	}
	metaV2 := []byte("generation=2 meta\n")
	statusV2 := []byte("generation=2 status\n")
	if err := os.WriteFile(metaPath, metaV2, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusPath, statusV2, 0644); err != nil {
		t.Fatal(err)
	}
	result, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth)
	if err != nil {
		t.Fatalf("superseded retry: %v", err)
	}
	found := false
	for _, step := range result.Steps {
		if strings.Contains(step, "superseded") {
			found = true
		}
	}
	if !found {
		t.Fatalf("steps = %v, want superseded outcome", result.Steps)
	}
	gotMeta, _ := os.ReadFile(metaPath)
	gotStatus, _ := os.ReadFile(statusPath)
	if string(gotMeta) != string(metaV2) || string(gotStatus) != string(statusV2) {
		t.Fatalf("current projections changed: meta=%q status=%q", gotMeta, gotStatus)
	}
}

func TestRun_AbortRefreshesBriefOnlyDirectory(t *testing.T) {
	tmp := t.TempDir()
	taskID := "abort-brief-only"
	auth := canonicalMergeTestAuth(t, tmp, taskID)
	if err := os.WriteFile(filepath.Join(tmp, "state", taskID+".meta"), []byte("kind=ship\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(tmp, "data", taskID)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "brief.md"), []byte("brief"), 0644); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1, 0)
	if err := os.Chtimes(dataDir, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "state", taskID+".status"), []byte("status"), 0644); err != nil {
		t.Fatal(err)
	}
	oldHook := afterReportArchive
	afterReportArchive = func(string, string, taskauthority.Generation) error { return errors.New("blocked") }
	t.Cleanup(func() { afterReportArchive = oldHook })
	if _, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup")
	}
	if err := AbortRetirementCleanup(auth, tmp, fakeTeardown{}, mustTaskID(t, taskID), 1); err != nil {
		t.Fatalf("abort: %v", err)
	}
	info, err := os.Stat(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().After(old) {
		t.Fatalf("directory mtime = %v, want refreshed", info.ModTime())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "brief.md")); err != nil {
		t.Fatalf("brief removed: %v", err)
	}
}

func TestRun_AbortRefusesUnarchivedReport(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("permission proof did not run: tests run as root, so abort archival refusal cannot be established")
	}
	tmp := t.TempDir()
	auth := canonicalMergeTestAuth(t, tmp, "abort-report")
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "abort-report.meta"), []byte("kind=scout\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(tmp, "data", "abort-report")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(dataDir, "report.md")
	if err := os.WriteFile(reportPath, []byte("findings"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dataDir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dataDir, 0755) })
	if _, err := RetireTask(Options{HomeDir: tmp, ID: "abort-report", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected teardown cleanup to remain pending")
	}
	if err := AbortRetirementCleanup(auth, tmp, fakeTeardown{}, mustTaskID(t, "abort-report"), 1); err == nil {
		t.Fatal("abort should refuse while report evacuation is blocked")
	}
	agg, err := auth.Get(mustTaskID(t, "abort-report"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != taskauthority.CleanupActive {
		t.Fatalf("cleanup claim = %+v, want active", agg.CleanupClaim)
	}
	if err := os.Chmod(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := AbortRetirementCleanup(auth, tmp, fakeTeardown{}, mustTaskID(t, "abort-report"), 1); err != nil {
		t.Fatalf("abort after restoring access: %v", err)
	}
	if _, err := os.Lstat(reportPath); !os.IsNotExist(err) {
		t.Fatalf("report.md should be evacuated, lstat err = %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dataDir, "report-g1.md"))
	if err != nil || string(body) != "findings" {
		t.Fatalf("archived report = %q, err = %v", body, err)
	}
	agg, err = auth.Get(mustTaskID(t, "abort-report"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != taskauthority.CleanupAborted {
		t.Fatalf("cleanup claim = %+v, want aborted", agg.CleanupClaim)
	}
}
