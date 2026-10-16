//go:build integration

package fleet

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/harness"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/testutil"
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

func testWorktreeBinding(path, digest string) *taskauthority.WorktreeBinding {
	return &taskauthority.WorktreeBinding{
		Path: path,
		LaunchManifest: &taskauthority.LaunchManifestEvidence{
			OperationID: "manifest-op", LaunchID: "launch-test", ManifestSHA256: digest, RecordedAt: 1,
		},
	}
}

// setupRetirementTestManifest writes launch artifacts and manifest to the
// worktree. Returns the digest for an explicit canonical binding fixture.
func setupRetirementTestManifest(t *testing.T, wt string) string {
	t.Helper()
	taskID, launchID, epFence := "retirement-test", "launch-retirement-test", "endpoint-fence-test"
	charter := DefaultCharter("retirement-test", "ship", "direct-PR", "")
	brief := []byte("# Retirement test brief\n")
	prompt := "prompt"
	os.WriteFile(filepath.Join(wt, CharterName), []byte(charter), 0644)
	os.WriteFile(filepath.Join(wt, BriefName), brief, 0644)
	os.WriteFile(filepath.Join(wt, PromptName), []byte(prompt), 0644)
	os.WriteFile(filepath.Join(wt, EnvelopeName), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(wt, LaunchScriptName), []byte("#!/bin/bash\n"), 0644)

	entries := []ManifestEntry{}
	for _, name := range CoreLaunchArtifactNames {
		entries = append(entries, manifestEntryForTestFile(t, wt, name, DisposalPolicyCleanable))
	}
	entries = append(entries, addDeferredGuardFixture(t, wt, taskID, launchID, "1", epFence))
	manifest := BuildManifest(entries)
	data, digest, err := MarshalManifest(manifest)
	if err != nil {
		t.Fatalf("marshaling manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt, ManifestName), data, 0644); err != nil {
		t.Fatalf("writing manifest fixture: %v", err)
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

	// Create a pushed branch
	cmd := exec.Command("git", "checkout", "-b", "fm/test-branch")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %s", out)
	}

	// Push it
	cmd = exec.Command("git", "push", "origin", "fm/test-branch")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git push: %s", out)
	}

	// Set up manifest.
	md := setupRetirementTestManifest(t, wt)

	// Clean state should pass
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, nil, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
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

	// Create a pushed branch.
	cmd := exec.Command("git", "checkout", "-b", "fm/soldier-artifact-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %s", out)
	}
	cmd = exec.Command("git", "push", "origin", "fm/soldier-artifact-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git push: %s", out)
	}

	// Set up manifest (writes all launch artifacts).
	md := setupRetirementTestManifest(t, wt)

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, nil, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
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

	// Create a pushed branch.
	cmd := exec.Command("git", "checkout", "-b", "fm/unknown-dirty-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %s", out)
	}
	cmd = exec.Command("git", "push", "origin", "fm/unknown-dirty-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git push: %s", out)
	}

	// Set up manifest (writes all launch artifacts).
	md := setupRetirementTestManifest(t, wt)

	// Write an unknown untracked file (should cause failure).
	os.WriteFile(filepath.Join(wt, "arbitrary.txt"), []byte("unknown\n"), 0644)

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, nil, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
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

	// Create a pushed branch.
	cmd := exec.Command("git", "checkout", "-b", "fm/mixed-dirty-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %s", out)
	}
	cmd = exec.Command("git", "push", "origin", "fm/mixed-dirty-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git push: %s", out)
	}

	// Set up manifest (writes all launch artifacts).
	md := setupRetirementTestManifest(t, wt)

	// Write an unknown untracked file.
	os.WriteFile(filepath.Join(wt, "rogue.txt"), []byte("rogue\n"), 0644)

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, nil, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
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

	// Create a pushed branch.
	cmd := exec.Command("git", "checkout", "-b", "fm/launch-script-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %s", out)
	}
	cmd = exec.Command("git", "push", "origin", "fm/launch-script-test")
	cmd.Dir = wt
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git push: %s", out)
	}

	// Set up manifest (writes all launch artifacts including launch script).
	md := setupRetirementTestManifest(t, wt)

	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, nil, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
	if err != nil {
		t.Fatalf("shipSafetyCheck should pass when only launch artifacts are dirty: %v", err)
	}
}

func TestShipSafetyCheck_NoRemoteBranch(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktree")
	os.MkdirAll(wt, 0755)
	setupGitRepo(t, wt, "") // no remote

	md := setupRetirementTestManifest(t, wt)
	_, err := shipSafetyCheck(Options{}, nil, fakeTeardown{}, nil, testWorktreeBinding(wt, md), false)
	if err == nil {
		t.Fatal("shipSafetyCheck should fail without remote")
	}
}

func TestShipSafetyCheck_NoWorktreeInMeta(t *testing.T) {
	_, err := shipSafetyCheck(Options{ID: "test"}, nil, fakeTeardown{}, nil, nil, false)
	if err == nil || !strings.Contains(err.Error(), "no canonical worktree path") {
		t.Fatalf("error = %v, want canonical worktree refusal", err)
	}
}

func TestScoutSafetyCheck_ReportExists(t *testing.T) {
	tmp := t.TempDir()

	// Create the generation's report, named as the write path emits it.
	reportDir := filepath.Join(tmp, "data", "scout-1")
	os.MkdirAll(reportDir, 0755)
	os.WriteFile(filepath.Join(reportDir, ReportName(1)), []byte("findings"), 0644)

	err := scoutSafetyCheck(Options{ID: "scout-1", HomeDir: tmp}, 1)
	if err != nil {
		t.Fatalf("should pass when the generation's report exists: %v", err)
	}
}

func TestScoutSafetyCheck_NoReport(t *testing.T) {
	tmp := t.TempDir()

	err := scoutSafetyCheck(Options{ID: "scout-1", HomeDir: tmp}, 1)
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
	if err := scoutSafetyCheck(opts, 1); err != nil {
		t.Fatalf("generation 1's own report must answer for generation 1: %v", err)
	}
	err := scoutSafetyCheck(opts, 2)
	if err == nil {
		t.Fatal("a prior generation's report must not answer for generation 2")
	}
	if !strings.Contains(err.Error(), ReportName(2)) {
		t.Fatalf("generation 2 refusal = %v, want it to resolve %s", err, ReportName(2))
	}
}

// TestRun_NeverLaunchedIsRefused refuses a generation that never began a
// launch and holds no resource; --force does not widen that.
func TestRun_NeverLaunchedIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name    string
		launch  bool
		refused bool
	}{
		{name: "never-launched task is refused", refused: true},
		{name: "launched but unbound task retires", launch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				homeDir, id string
				auth        *taskauthority.Canonical
			)
			if tc.launch {
				f := newLaunchFixture(t, "no-meta-launched")
				if err := runLaunchPhases(f, "verify"); !errors.Is(err, errCrashSimulated) {
					t.Fatalf("launch phases = %v, want simulated pre-projection crash", err)
				}
				homeDir, id, auth = f.homeDir, f.taskID, f.auth
			} else {
				homeDir, id = t.TempDir(), "no-meta-queued"
				auth = canonicalMergeTestAuth(t, homeDir, id)
			}
			_, err := RetireTask(Options{HomeDir: homeDir, ID: id, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth)
			if refused := err != nil && strings.Contains(err.Error(), "teardown no-meta-queued: task never launched, nothing to retire"); refused != tc.refused {
				t.Fatalf("RetireTask error = %v, want refused=%v", err, tc.refused)
			}
			agg, gerr := auth.Get(mustTaskID(t, id))
			if gerr != nil {
				t.Fatal(gerr)
			}
			if retired := agg.Phase == taskauthority.PhaseRetired; retired == tc.refused {
				t.Fatalf("phase = %q, want retired=%v", agg.Phase, !tc.refused)
			}
		})
	}
}

func TestRun_SafetyCheckGenerationFailsClosedWithoutAuthority(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("MUNSU_HOME", tmp)
	defer os.Unsetenv("MUNSU_HOME")

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

	auth := canonicalScoutTestAuth(t, tmp, "pinned-conflict")
	seedWorktreeEvidence(t, auth, "pinned-conflict", t.TempDir(), "lease-wt", "fence-wt")

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

	auth := canonicalScoutTestAuth(t, tmp, "nonexistent")
	seedWorktreeEvidence(t, auth, "nonexistent", t.TempDir(), "lease-wt", "fence-wt")

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

	auth := canonicalScoutTestAuth(t, tmp, "scout-test")
	seedWorktreeEvidence(t, auth, "scout-test", t.TempDir(), "lease-wt", "fence-wt")

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

	auth := canonicalScoutTestAuth(t, tmp, "scout-report")

	seedWorktreeEvidence(t, auth, "scout-report", t.TempDir(), "lease-wt", "fence-wt")

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
	if err := scoutSafetyCheck(Options{HomeDir: tmp, ID: "scout-report"}, 1); err != nil {
		t.Fatalf("generation 1's own report must answer for generation 1: %v", err)
	}
	if err := scoutSafetyCheck(Options{HomeDir: tmp, ID: "scout-report"}, 2); err == nil {
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

	auth := canonicalScoutTestAuth(t, tmp, "stub-brief")

	seedWorktreeEvidence(t, auth, "stub-brief", t.TempDir(), "lease-wt", "fence-wt")

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

	auth := canonicalScoutTestAuth(t, tmp, "test-residual")
	// The canonical launch names the pi harness, so its adapter artifacts are
	// included; the meta names none.
	begin := taskauthority.CanonicalBeginSpawnRequest{
		HomeID: auth.HomeID(), TaskID: mustTaskID(t, "test-residual"), Precondition: domain.Of(1, 1),
		SnapshotDigest: strings.Repeat("a", 64), Backend: "tmux", Harness: harness.Pi, Kind: taskauthority.KindScout,
		Project: "proj", LaunchID: "launch-test-residual", WorktreeReservationID: "wt-res-1", WorktreeFenceToken: "wt-fence-1",
		EndpointReservationID: "ep-res-1", EndpointFenceToken: "ep-fence-1", SeatPathDirs: []string{"/usr/bin"},
		EndpointIncarnation: "inc-test-residual", SeatEnv: taskauthority.SeatEnv{TempDir: t.TempDir(), UserHome: tmp}, Reason: "spawn",
	}
	if _, err := auth.BeginSpawn(mustFleetOperation(t, "op-begin-test-residual", begin), begin); err != nil {
		t.Fatalf("BeginSpawn: %v", err)
	}

	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Create residual artifacts: munsu-native + pi adapter artifacts, the
	// per-task pi agent dir among them (a directory with content).
	residuals := []string{
		"test-residual.status",
		"test-residual.turnend",
		"test-residual.pi-ext.ts",
		"test-residual." + harness.PiAgentDirSuffix,
	}
	for _, name := range residuals {
		os.WriteFile(filepath.Join(stateDir, name), []byte("stale"), 0644)
	}
	agentDir := filepath.Join(stateDir, "test-residual."+harness.PiAgentDirSuffix)
	os.Remove(agentDir)
	if err := os.MkdirAll(filepath.Join(agentDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(agentDir, "sub", "f"), []byte("stale"), 0o644)

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

func TestCleanupResidualArtifactPaths(t *testing.T) {
	tmp := t.TempDir()
	stateDir := mhome.StateDir(tmp)
	for _, tc := range []struct {
		name        string
		kind        string
		harnessName string
		want        []string
	}{
		{"ship with pi", taskauthority.KindShip, harness.Pi, []string{
			filepath.Join(stateDir, "T-1.status"), filepath.Join(stateDir, "T-1.turnend"), filepath.Join(stateDir, "T-1.report-cursor"),
			filepath.Join(stateDir, "T-1.pi-ext.ts"), filepath.Join(stateDir, "T-1."+harness.PiAgentDirSuffix),
		}},
		{"review also removes its launch home", taskauthority.KindReview, harness.Pi, []string{
			filepath.Join(stateDir, "T-1.status"), reviewHomeDir(tmp, "T-1"), filepath.Join(stateDir, "T-1.turnend"), filepath.Join(stateDir, "T-1.report-cursor"),
			filepath.Join(stateDir, "T-1.pi-ext.ts"), filepath.Join(stateDir, "T-1."+harness.PiAgentDirSuffix),
		}},
		{"unknown harness has only the native artifacts", taskauthority.KindShip, "", []string{
			filepath.Join(stateDir, "T-1.status"), filepath.Join(stateDir, "T-1.turnend"), filepath.Join(stateDir, "T-1.report-cursor"),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cleanupResidualArtifactPaths(tmp, "T-1", tc.kind, tc.harnessName)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("cleanupResidualArtifactPaths = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	if _, err := cleanupResidualArtifactPaths(tmp, "../escape", taskauthority.KindShip, ""); err == nil {
		t.Fatal("cleanupResidualArtifactPaths accepted an id that escapes the state dir")
	}
}

func scoutHoldsBlock(t *testing.T) {
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
	err := scoutSafetyCheck(Options{ID: "scout-1", HomeDir: tmp}, 1)
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

func scoutHoldsAllow(t *testing.T) {
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
	err := scoutSafetyCheck(Options{ID: "scout-1", HomeDir: tmp}, 1)
	if err != nil {
		t.Fatalf("should pass when all holds resolved: %v", err)
	}
}

func TestRun_ForceSkipsDecisionHoldCheck(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("MUNSU_HOME", tmp)
	defer os.Unsetenv("MUNSU_HOME")

	auth := canonicalScoutTestAuth(t, tmp, "scout-test")

	seedWorktreeEvidence(t, auth, "scout-test", t.TempDir(), "lease-wt", "fence-wt")

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
	auth := canonicalScoutTestAuth(t, tmp, "data-file")
	seedWorktreeEvidence(t, auth, "data-file", t.TempDir(), "lease-wt", "fence-wt")
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
	auth := canonicalScoutTestAuth(t, tmp, "data-parent")
	seedWorktreeEvidence(t, auth, "data-parent", t.TempDir(), "lease-wt", "fence-wt")
	dataRoot := filepath.Join(tmp, "data")
	dataDir := filepath.Join(dataRoot, "data-parent")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, ReportName(1)), []byte("findings"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Run("stat refusal fails closed", func(t *testing.T) {
		testutil.MakePathUnreadable(t, dataRoot)

		_, err := RetireTask(Options{HomeDir: tmp, ID: "data-parent", Force: true}, fakeTeardown{}, fakeRetirementJournals{}, auth)
		var pending *RetirementCleanupPendingError
		if !errors.As(err, &pending) {
			t.Fatalf("teardown error = %T %v, want typed RetirementCleanupPendingError", err, err)
		}
	})

	if _, err := os.Stat(filepath.Join(dataDir, ReportName(1))); err != nil {
		t.Fatalf("report should remain after data-parent refusal: %v", err)
	}
}

// abortPendingCleanup leaves cleanup of a forced teardown pending (dispose
// fails), so the task holds an active cleanup claim for generation 1.
func abortPendingCleanup(t *testing.T) (*taskauthority.Canonical, string) {
	t.Helper()
	tmp := t.TempDir()
	auth := mergeTestAuth(t, tmp, "abort-live")
	if _, err := RetireTask(Options{HomeDir: tmp, ID: "abort-live", Force: true}, fakeTeardown{alive: true, disposeErr: errors.New("stuck")}, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected cleanup to remain pending")
	}
	return auth, tmp
}

func abortLiveEndpointBlock(t *testing.T) {
	auth, tmp := abortPendingCleanup(t)
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

func abortAbsentEndpointAllow(t *testing.T) {
	auth, tmp := abortPendingCleanup(t)
	if err := AbortRetirementCleanup(auth, tmp, fakeTeardown{}, mustTaskID(t, "abort-live"), 1); err != nil {
		t.Fatalf("abort with the endpoint proven absent: %v", err)
	}
	agg, err := auth.Get(mustTaskID(t, "abort-live"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != taskauthority.CleanupAborted {
		t.Fatalf("cleanup claim = %+v, want aborted", agg.CleanupClaim)
	}
}

type failingRetirementJournals struct{}

func (failingRetirementJournals) VerifyRetirementContinuity(string, string, string) error { return nil }
func (failingRetirementJournals) PrepareForcedRetirementEvidence(string, string) ([]string, error) {
	return nil, nil
}
func (failingRetirementJournals) FinalizeRetirementJournals(string, string) ([]string, error) {
	return nil, errors.New("journal finalization failed")
}

func TestRun_RetryAfterJournalFailureCompletes(t *testing.T) {
	tmp := t.TempDir()
	taskID := "retry-journal"
	auth := canonicalScoutTestAuth(t, tmp, taskID)
	seedWorktreeEvidence(t, auth, taskID, t.TempDir(), "lease-wt", "fence-wt")
	if _, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, failingRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup")
	}
	if _, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("retry: %v", err)
	}
	claim, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if claim.CleanupClaim == nil || claim.CleanupClaim.Status != taskauthority.CleanupCompleted {
		t.Fatalf("claim = %+v", claim.CleanupClaim)
	}
}

// blockResidualRemoval leaves a residual the projection cleanup cannot remove: a
// "<id>.turnend" directory whose own mode forbids unlinking its content. The
// returned func lifts the block so the retry can succeed.
func blockResidualRemoval(t *testing.T, stateDir, taskID string) (lift func()) {
	t.Helper()
	blocked := filepath.Join(stateDir, taskID+".turnend")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "child"), []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0o500); err != nil {
		t.Fatal(err)
	}
	lift = func() {
		if err := os.Chmod(blocked, 0o755); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	t.Cleanup(lift)
	return lift
}

func TestRun_ProjectionFailureRetriesWhileRetired(t *testing.T) {
	tmp := t.TempDir()
	taskID := "projection-retry"
	auth := canonicalMergeTestAuth(t, tmp, taskID)
	seedWorktreeEvidence(t, auth, taskID, t.TempDir(), "lease-wt", "fence-wt")
	lift := blockResidualRemoval(t, filepath.Join(tmp, "state"), taskID)
	_, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth)
	var projectionErr *RetirementProjectionError
	if !errors.As(err, &projectionErr) {
		t.Fatalf("error = %T %v, want projection error", err, err)
	}
	claim, err := auth.Get(mustTaskID(t, taskID))
	if err != nil || claim.CleanupClaim == nil || claim.CleanupClaim.Status != taskauthority.CleanupCompleted {
		t.Fatalf("claim = %+v, err=%v, want completed", claim.CleanupClaim, err)
	}
	lift()
	if _, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestRun_WakePruneFailureIsRetried(t *testing.T) {
	tmp := t.TempDir()
	taskID := "prune-retry"
	auth := canonicalMergeTestAuth(t, tmp, taskID)
	seedWorktreeEvidence(t, auth, taskID, t.TempDir(), "lease-wt", "fence-wt")
	marker := mhome.WatcherSeenMarkerPath(tmp, taskID)
	if err := os.MkdirAll(filepath.Join(marker, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth)
	var projectionErr *RetirementProjectionError
	if !errors.As(err, &projectionErr) {
		t.Fatalf("error = %T %v, want projection error", err, err)
	}
	// The claim is now completed, so this retry runs the completed-claim path.
	_, err = RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth)
	if !errors.As(err, &projectionErr) {
		t.Fatalf("completed-claim retry error = %T %v, want projection error", err, err)
	}
	if err := os.RemoveAll(marker); err != nil {
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
	statusPath := filepath.Join(tmp, "state", taskID+".status")
	seedWorktreeEvidence(t, auth, taskID, t.TempDir(), "lease-wt", "fence-wt")
	lift := blockResidualRemoval(t, filepath.Join(tmp, "state"), taskID)
	_, err := RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth)
	var projectionErr *RetirementProjectionError
	if !errors.As(err, &projectionErr) {
		t.Fatalf("error = %T %v, want projection error", err, err)
	}
	lift()
	if err := os.RemoveAll(filepath.Join(tmp, "state", taskID+".turnend")); err != nil {
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
	statusV2 := []byte("generation=2 status\n")
	if err := os.WriteFile(statusPath, statusV2, 0644); err != nil {
		t.Fatal(err)
	}
	_, err = RetireTask(Options{HomeDir: tmp, ID: taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth)
	// The prior generation's claim is terminal and the task reopened, so this
	// unpinned continuation is stale and must fail closed. The
	// point of this case is that it removes nothing from generation 2.
	var staleErr *RetirementStaleTeardownError
	if !errors.As(err, &staleErr) {
		t.Fatalf("superseded retry error = %T %v, want stale teardown error", err, err)
	}
	if staleErr.PriorGeneration != 1 || staleErr.CurrentGeneration != 2 || staleErr.TerminalStatus != "completed" {
		t.Fatalf("stale error = %+v, want prior 1 / current 2 / completed", staleErr)
	}
	if gotStatus, _ := os.ReadFile(statusPath); string(gotStatus) != string(statusV2) {
		t.Fatalf("current status projection changed: %q", gotStatus)
	}
}

func TestRun_AbortRefreshesBriefOnlyDirectory(t *testing.T) {
	tmp := t.TempDir()
	taskID := "abort-brief-only"
	auth := mergeTestAuth(t, tmp, taskID)
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

func TestCheckRemoteBranchPushedHeadProof(t *testing.T) {
	gitIn := func(t *testing.T, dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(t *testing.T, dir, name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, "add", name)
		gitIn(t, dir, "commit", "-m", name)
	}
	const branch = "fm/proof"

	cases := []struct {
		name    string
		arrange func(t *testing.T, wt, remote string)
		want    string // empty means pass
	}{
		{"no upstream, head on origin", func(t *testing.T, wt, _ string) {
			gitIn(t, wt, "push", "origin", branch)
		}, ""},
		{"upstream set, head unpushed", func(t *testing.T, wt, _ string) {
			gitIn(t, wt, "push", "-u", "origin", branch)
			commit(t, wt, "unpushed")
		}, "diverged or is missing this head"},
		{"local behind origin", func(t *testing.T, wt, remote string) {
			gitIn(t, wt, "push", "origin", branch)
			commit(t, wt, "ahead")
			// pushing by URL leaves refs/remotes/origin stale, so a leaky fetch shows
			gitIn(t, wt, "push", remote, branch)
			gitIn(t, wt, "reset", "--hard", "HEAD~1")
		}, ""},
		{"diverged", func(t *testing.T, wt, _ string) {
			gitIn(t, wt, "push", "origin", branch)
			gitIn(t, wt, "commit", "--amend", "-m", "rebased")
		}, "diverged"},
		{"remote branch missing", func(t *testing.T, _, _ string) {}, "remote branch is missing"},
		{"remote ref only matches the tail pattern", func(t *testing.T, wt, _ string) {
			gitIn(t, wt, "push", "origin", "HEAD:refs/foo/refs/heads/"+branch)
		}, "remote branch is missing"},
		{"proof fetch fails", func(t *testing.T, wt, _ string) {
			gitIn(t, wt, "push", "origin", branch)
			if err := os.WriteFile(filepath.Join(wt, ".git", "refs", "munsu"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}, "but fetching it failed"},
		{"origin unreachable", func(t *testing.T, wt, remote string) {
			gitIn(t, wt, "remote", "set-url", "origin", remote+"-gone")
		}, "origin is unreachable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			wt := filepath.Join(tmp, "worktree")
			remote := filepath.Join(tmp, "remote.git")
			os.MkdirAll(wt, 0o755)
			setupGitRepo(t, wt, remote)
			gitIn(t, wt, "checkout", "-b", branch)
			commit(t, wt, "work")
			tc.arrange(t, wt, remote)

			remotesBefore := gitIn(t, wt, "for-each-ref", "refs/remotes/")
			fetchHead := filepath.Join(wt, ".git", "FETCH_HEAD")
			_, statErr := os.Stat(fetchHead)
			hadFetchHead := statErr == nil

			err := checkRemoteBranch(wt)
			if after := gitIn(t, wt, "for-each-ref", "refs/remotes/"); after != remotesBefore {
				t.Errorf("proof changed refs/remotes:\nbefore %s\nafter  %s", remotesBefore, after)
			}
			if _, statErr := os.Stat(fetchHead); statErr == nil && !hadFetchHead {
				t.Errorf("proof wrote FETCH_HEAD")
			}
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("checkRemoteBranch = %v, want pass", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("checkRemoteBranch = %v, want refusal containing %q", err, tc.want)
			}
			if err != nil {
				for _, w := range []string{branch, gitIn(t, wt, "rev-parse", "HEAD"), "use --force to override"} {
					if !strings.Contains(err.Error(), w) {
						t.Errorf("refusal %q lacks %q", err, w)
					}
				}
				if strings.Contains(err.Error(), "exit status") {
					t.Errorf("refusal leaks raw exit status: %v", err)
				}
			}
			if refs := gitIn(t, wt, "for-each-ref", "refs/munsu/"); refs != "" {
				t.Errorf("temporary proof ref left behind: %s", refs)
			}
		})
	}
}
