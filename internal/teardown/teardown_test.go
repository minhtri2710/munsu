package teardown

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupGitRepo initializes a git repo in dir.
func setupGitRepo(t *testing.T, dir, remoteDir string) {
	t.Helper()

	// Block git from looking at parent directories
	gitEnv := append(os.Environ(),
		fmt.Sprintf("GIT_CEILING_DIRECTORIES=%s", dir),
	)

	// Init
	cmd := exec.Command("git", "init")
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

	// Clean state should pass
	meta := map[string]string{
		"worktree": wt,
		"kind":     "ship",
	}
	err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta)
	if err != nil {
		t.Fatalf("shipSafetyCheck should pass for clean branch: %v", err)
	}
}

func TestShipSafetyCheck_Dirty(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktree")
	remote := filepath.Join(tmp, "remote.git")
	os.MkdirAll(wt, 0755)
	setupGitRepo(t, wt, remote)

	// Create a dirty file
	os.WriteFile(filepath.Join(wt, "dirty.txt"), []byte("changes"), 0644)

	meta := map[string]string{
		"worktree": wt,
		"kind":     "ship",
	}
	err := shipSafetyCheck(Options{}, meta)
	if err == nil {
		t.Fatal("shipSafetyCheck should fail for dirty worktree")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("unexpected error: %v", err)
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
	err := shipSafetyCheck(Options{}, meta)
	if err == nil {
		t.Fatal("shipSafetyCheck should fail without remote")
	}
}

func TestShipSafetyCheck_NoWorktreeInMeta(t *testing.T) {
	err := shipSafetyCheck(Options{ID: "test"}, map[string]string{})
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

	_, err := Run(Options{HomeDir: tmp, ID: "nonexistent"})
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
	metaContent := "kind=scout\nwindow=@1\n"
	os.WriteFile(filepath.Join(stateDir, "nonexistent.meta"), []byte(metaContent), 0644)

	// With --force, it should try to proceed (will fail at session/return steps but not at safety)
	result, err := Run(Options{HomeDir: tmp, ID: "nonexistent", Force: true})
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
	metaContent := "kind=scout\nwindow=@1\n"
	os.WriteFile(filepath.Join(stateDir, "scout-test.meta"), []byte(metaContent), 0644)

	// Without --force, should fail
	_, err := Run(Options{HomeDir: tmp, ID: "scout-test", Force: false})
	if err == nil {
		t.Fatal("should fail for scout without report without --force")
	}

	// With --force, should proceed
	result, err := Run(Options{HomeDir: tmp, ID: "scout-test", Force: true})
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
	metaContent := "kind=scout\nwindow=@1\nharness=pi\n"
	os.WriteFile(filepath.Join(stateDir, "test-residual.meta"), []byte(metaContent), 0644)

	// Create residual artifacts: munsu-native + pi adapter artifacts
	residuals := []string{
		"test-residual.status",
		"test-residual.check.sh",
		"test-residual.turn-ended",
		"test-residual.pi-ext.ts",
	}
	for _, name := range residuals {
		os.WriteFile(filepath.Join(stateDir, name), []byte("stale"), 0644)
	}

	// Run teardown with --force to skip safety
	result, err := Run(Options{HomeDir: tmp, ID: "test-residual", Force: true})
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
