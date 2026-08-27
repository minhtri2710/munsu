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

	// Create the generation's report, named as the write path emits it.
	reportDir := filepath.Join(tmp, "data", "scout-1")
	os.MkdirAll(reportDir, 0755)
	os.WriteFile(filepath.Join(reportDir, ReportName(1)), []byte("findings"), 0644)

	err := scoutSafetyCheck(Options{ID: "scout-1", HomeDir: tmp}, nil, 1)
	if err != nil {
		t.Fatalf("should pass when the generation's report exists: %v", err)
	}
}

func TestScoutSafetyCheck_NoReport(t *testing.T) {
	tmp := t.TempDir()

	err := scoutSafetyCheck(Options{ID: "scout-1", HomeDir: tmp}, nil, 1)
	if err == nil {
		t.Fatal("should fail when the generation wrote no report")
	}
}

// TestScoutSafetyCheckRejectsPriorGenerationReport is the lane claim: the
// safety check resolves the report BY GENERATION. A report written by a prior
// generation never satisfies a later generation's check, and a generation's
// own report satisfies exactly its own check.
func TestScoutSafetyCheckRejectsPriorGenerationReport(t *testing.T) {
	tmp := t.TempDir()
	reportDir := filepath.Join(tmp, "data", "scout-gen")
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, ReportName(1)), []byte("generation 1 findings"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := Options{ID: "scout-gen", HomeDir: tmp}
	if err := scoutSafetyCheck(opts, nil, 1); err != nil {
		t.Fatalf("generation 1's own report must answer for generation 1: %v", err)
	}
	err := scoutSafetyCheck(opts, nil, 2)
	if err == nil {
		t.Fatal("a prior generation's report must not answer for generation 2")
	}
	if !strings.Contains(err.Error(), ReportName(2)) {
		t.Fatalf("generation 2 refusal = %v, want it to resolve %s", err, ReportName(2))
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

func TestRun_SafetyCheckGenerationFailsClosedWithoutAuthority(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("MUNSU_HOME", tmp)
	defer os.Unsetenv("MUNSU_HOME")

	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "no-authority.meta"), []byte("kind=scout\nbackend=tmux\nwindow=@1\n"), 0644)

	// The scout safety check answers for a canonical generation, so a nil
	// authority fails closed before any report is resolved.
	_, err := RetireTask(Options{HomeDir: tmp, ID: "no-authority"}, fakeTeardown{}, fakeRetirementJournals{}, nil)
	if err == nil || !strings.Contains(err.Error(), "composed task authority") {
		t.Fatalf("error = %v, want the composed-authority refusal", err)
	}
}

// TestRun_PinnedTeardownConflictPreemptsReportCheck proves the generation
// binding is validated BEFORE any report is resolved: a teardown pinned to a
// generation the task has not reached fails closed with the typed conflict —
// even with no report on disk at all — instead of certifying or refusing on
// some other generation's report.
func TestRun_PinnedTeardownConflictPreemptsReportCheck(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("MUNSU_HOME", tmp)
	defer os.Unsetenv("MUNSU_HOME")

	auth := canonicalMergeTestAuth(t, tmp, "pinned-conflict")
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "pinned-conflict.meta"), []byte("kind=scout\nbackend=tmux\nwindow=@1\n"), 0644)

	target := taskauthority.Generation(2)
	_, err := RetireTask(Options{HomeDir: tmp, ID: "pinned-conflict", ExpectedGeneration: &target}, fakeTeardown{}, fakeRetirementJournals{}, auth)
	var conflict *RetirementTargetConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %T %v, want typed RetirementTargetConflictError", err, err)
	}
	if conflict.Target != 2 || conflict.Current != 1 {
		t.Fatalf("conflict = %+v, want target 2 vs current 1", conflict)
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
	// The soldier writes the report generation-named, exactly as the brief
	// instructs. There is no unversioned name, so teardown has nothing to
	// move: the report is retained in place as generation 1's evidence.
	reportPath := ReportPath(tmp, "scout-report", 1)
	os.WriteFile(reportPath, []byte("# findings\npartial work worth reading\n"), 0644)

	result, err := RetireTask(Options{HomeDir: tmp, ID: "scout-report", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth)
	if err != nil {
		t.Fatalf("forced teardown: %v", err)
	}
	body, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("--force must not delete the report: %v", err)
	}
	if !strings.Contains(string(body), "partial work worth reading") {
		t.Fatalf("retained report = %q, want the retired generation's findings", body)
	}
	for _, step := range result.Steps {
		if strings.Contains(step, "archived") {
			t.Fatalf("no archival step may exist, got %v", result.Steps)
		}
	}
	if !hasStep(result.Steps, "data dir kept for relaunch or session-start sweep") {
		t.Fatalf("steps = %v, want the data-dir-kept step", result.Steps)
	}

	// The report belongs to generation 1 and stops answering for any other:
	// a later generation's check resolves only its own generation-named
	// report, and its absence from that name is what refuses the check.
	if err := scoutSafetyCheck(Options{HomeDir: tmp, ID: "scout-report"}, map[string]string{"kind": "scout"}, 1); err != nil {
		t.Fatalf("generation 1's own report must answer for generation 1: %v", err)
	}
	if err := scoutSafetyCheck(Options{HomeDir: tmp, ID: "scout-report"}, map[string]string{"kind": "scout"}, 2); err == nil {
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

	// Create the generation's report so the generation-scoped check passes.
	reportDir := filepath.Join(tmp, "data", "scout-1")
	os.MkdirAll(reportDir, 0755)
	os.WriteFile(filepath.Join(reportDir, ReportName(1)), []byte("findings"), 0644)

	// Create unresolved decision holds by appending the needs-decision status
	// projection the decision-hold lifecycle mirrors into.
	if err := mhome.AppendStatus(tmp, "scout-1", "needs-decision: Pick the UI framework [key=approach]"); err != nil {
		t.Fatal(err)
	}
	if err := mhome.AppendStatus(tmp, "scout-1", "needs-decision: Choose DB schema [key=db-schema]"); err != nil {
		t.Fatal(err)
	}

	// scoutSafetyCheck should fail listing unresolved keys.
	err := scoutSafetyCheck(Options{ID: "scout-1", HomeDir: tmp}, nil, 1)
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

	// Create the generation's report so the generation-scoped check passes.
	reportDir := filepath.Join(tmp, "data", "scout-1")
	os.MkdirAll(reportDir, 0755)
	os.WriteFile(filepath.Join(reportDir, ReportName(1)), []byte("findings"), 0644)

	// Create a hold and resolve it through the status projection.
	if err := mhome.AppendStatus(tmp, "scout-1", "needs-decision: Pick the UI framework [key=approach]"); err != nil {
		t.Fatal(err)
	}
	if err := mhome.AppendStatus(tmp, "scout-1", "resolved: Choose React [key=approach]"); err != nil {
		t.Fatal(err)
	}

	// scoutSafetyCheck should pass since all holds are resolved.
	err := scoutSafetyCheck(Options{ID: "scout-1", HomeDir: tmp}, nil, 1)
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

	// Create the generation's report so the report check passes before the
	// decision hold check.
	reportDir := filepath.Join(tmp, "data", "scout-test")
	os.MkdirAll(reportDir, 0755)
	os.WriteFile(filepath.Join(reportDir, ReportName(1)), []byte("findings"), 0644)
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
	if err := os.WriteFile(filepath.Join(dataDir, ReportName(1)), []byte("findings"), 0644); err != nil {
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
	if _, err := os.Stat(filepath.Join(dataDir, ReportName(1))); err != nil {
		t.Fatalf("report should remain after data-parent refusal: %v", err)
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
	_, err = RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth)
	// The prior generation's claim is terminal and the task reopened, so this
	// unpinned continuation is stale (BEO-16/P1a) and must fail closed. The
	// point of this case is that it removes nothing from generation 2.
	var staleErr *RetirementStaleTeardownError
	if !errors.As(err, &staleErr) {
		t.Fatalf("superseded retry error = %T %v, want stale teardown error", err, err)
	}
	if staleErr.PriorGeneration != 1 || staleErr.CurrentGeneration != 2 || staleErr.TerminalStatus != "completed" {
		t.Fatalf("stale error = %+v, want prior 1 / current 2 / completed", staleErr)
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
	auth := mergeTestAuth(t, tmp, taskID)
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
	// Force cleanup to stay pending: the bound endpoint refuses disposal, so
	// the claim is left active for the operator's abort.
	if _, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, fakeTeardown{alive: true, disposeErr: errors.New("stuck")}, fakeRetirementJournals{}, auth); err == nil {
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
