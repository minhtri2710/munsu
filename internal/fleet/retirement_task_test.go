//go:build integration

package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
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
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{})
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
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{})
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
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{})
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
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{})
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
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta, fakeTeardown{})
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
	_, err := shipSafetyCheck(Options{}, meta, fakeTeardown{})
	if err == nil {
		t.Fatal("shipSafetyCheck should fail without remote")
	}
}

func TestShipSafetyCheck_NoWorktreeInMeta(t *testing.T) {
	_, err := shipSafetyCheck(Options{ID: "test"}, map[string]string{}, fakeTeardown{})
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

	_, err := RetireTask(Options{HomeDir: tmp, ID: "nonexistent"}, fakeTeardown{}, fakeRetirementJournals{})
	if err == nil {
		t.Fatal("should fail for nonexistent task")
	}
}

func TestRun_ForceSkipsSafety(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("MUNSU_HOME", tmp)
	defer os.Unsetenv("MUNSU_HOME")

	// Create a minimal meta file
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	metaContent := "kind=scout\nbackend=tmux\nwindow=@1\n"
	os.WriteFile(filepath.Join(stateDir, "nonexistent.meta"), []byte(metaContent), 0644)

	// With --force, it should try to proceed (will fail at session/return steps but not at safety)
	result, err := RetireTask(Options{HomeDir: tmp, ID: "nonexistent", Force: true}, fakeTeardown{}, fakeRetirementJournals{})
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

	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	metaContent := "kind=scout\nbackend=tmux\nwindow=@1\n"
	os.WriteFile(filepath.Join(stateDir, "scout-test.meta"), []byte(metaContent), 0644)

	// Without --force, should fail
	_, err := RetireTask(Options{HomeDir: tmp, ID: "scout-test", Force: false}, fakeTeardown{}, fakeRetirementJournals{})
	if err == nil {
		t.Fatal("should fail for scout without report without --force")
	}

	// With --force, should proceed
	result, err := RetireTask(Options{HomeDir: tmp, ID: "scout-test", Force: true}, fakeTeardown{}, fakeRetirementJournals{})
	if err != nil {
		t.Fatalf("with --force should proceed: %v", err)
	}
	if len(result.Steps) == 0 {
		t.Error("expected teardown steps")
	}
}

func TestRun_RemovesResidualArtifacts(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("MUNSU_HOME", tmp)
	defer os.Unsetenv("MUNSU_HOME")

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
	result, err := RetireTask(Options{HomeDir: tmp, ID: "test-residual", Force: true}, fakeTeardown{}, fakeRetirementJournals{})
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
	result, err := RetireTask(Options{HomeDir: tmp, ID: "legacy-test", Force: true}, fakeTeardown{}, fakeRetirementJournals{})
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
	_, err := RetireTask(Options{HomeDir: tmp, ID: "scout-test", Force: false}, fakeTeardown{}, fakeRetirementJournals{})
	if err == nil {
		t.Fatal("should fail for scout with unresolved holds without --force")
	}
	if !strings.Contains(err.Error(), "unresolved decision hold") {
		t.Errorf("error should mention unresolved decision holds, got: %v", err)
	}

	// With --force, should proceed past safety checks.
	result, err := RetireTask(Options{HomeDir: tmp, ID: "scout-test", Force: true}, fakeTeardown{}, fakeRetirementJournals{})
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
