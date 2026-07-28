package teardown

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/classify"
	"github.com/minhtri2710/munsu/internal/decisionhold"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/soldier"
	"github.com/minhtri2710/munsu/internal/turnend"
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
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta)
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

	// Write known launch artifacts (untracked).
	for _, name := range soldier.LaunchArtifactNames() {
		os.WriteFile(filepath.Join(wt, name), []byte("test content\n"), 0644)
	}

	meta := map[string]string{
		"worktree": wt,
		"kind":     "ship",
	}
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta)
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

	// Write a known launch artifact (should be fine alone).
	os.WriteFile(filepath.Join(wt, ".soldier-charter.md"), []byte("test\n"), 0644)

	// Write an unknown untracked file (should cause failure).
	os.WriteFile(filepath.Join(wt, "arbitrary.txt"), []byte("unknown\n"), 0644)

	meta := map[string]string{
		"worktree": wt,
		"kind":     "ship",
	}
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta)
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

	// Write all known launch artifacts.
	for _, name := range soldier.LaunchArtifactNames() {
		os.WriteFile(filepath.Join(wt, name), []byte("test\n"), 0644)
	}
	// Write an unknown untracked file.
	os.WriteFile(filepath.Join(wt, "rogue.txt"), []byte("rogue\n"), 0644)

	meta := map[string]string{
		"worktree": wt,
		"kind":     "ship",
	}
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta)
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

	// Write only the launch script (a member of the known set).
	os.WriteFile(filepath.Join(wt, ".soldier-launch.sh"), []byte("#!/bin/bash\nexec pi\n"), 0755)

	meta := map[string]string{
		"worktree": wt,
		"kind":     "ship",
	}
	_, err := shipSafetyCheck(Options{ID: "test", HomeDir: tmp}, meta)
	if err != nil {
		t.Fatalf("shipSafetyCheck should pass when only .soldier-launch.sh is dirty: %v", err)
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
	_, err := shipSafetyCheck(Options{}, meta)
	if err == nil {
		t.Fatal("shipSafetyCheck should fail without remote")
	}
}

func TestShipSafetyCheck_NoWorktreeInMeta(t *testing.T) {
	_, err := shipSafetyCheck(Options{ID: "test"}, map[string]string{})
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

	_, err := RunWithBackend(Options{HomeDir: tmp, ID: "nonexistent"}, fakeTeardown{})
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
	result, err := RunWithBackend(Options{HomeDir: tmp, ID: "nonexistent", Force: true}, fakeTeardown{})
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
	_, err := RunWithBackend(Options{HomeDir: tmp, ID: "scout-test", Force: false}, fakeTeardown{})
	if err == nil {
		t.Fatal("should fail for scout without report without --force")
	}

	// With --force, should proceed
	result, err := RunWithBackend(Options{HomeDir: tmp, ID: "scout-test", Force: true}, fakeTeardown{})
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
	result, err := RunWithBackend(Options{HomeDir: tmp, ID: "test-residual", Force: true}, fakeTeardown{})
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
	result, err := RunWithBackend(Options{HomeDir: tmp, ID: "legacy-test", Force: true}, fakeTeardown{})
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

	// Create unresolved decision holds.
	_, err := decisionhold.Create(tmp, "scout-1", "approach", "Pick the UI framework")
	if err != nil {
		t.Fatal(err)
	}
	_, err = decisionhold.Create(tmp, "scout-1", "db-schema", "Choose DB schema")
	if err != nil {
		t.Fatal(err)
	}

	// scoutSafetyCheck should fail listing unresolved keys.
	err = scoutSafetyCheck(Options{ID: "scout-1", HomeDir: tmp}, nil)
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

	// Create a hold and resolve it.
	_, err := decisionhold.Create(tmp, "scout-1", "approach", "Pick the UI framework")
	if err != nil {
		t.Fatal(err)
	}
	err = decisionhold.Resolve(tmp, "scout-1", "approach", "Choose React", nil)
	if err != nil {
		t.Fatal(err)
	}

	// scoutSafetyCheck should pass since all holds are resolved.
	err = scoutSafetyCheck(Options{ID: "scout-1", HomeDir: tmp}, nil)
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
	// Create unresolved decision holds.
	_, err := decisionhold.Create(tmp, "scout-test", "approach", "Pick the UI framework")
	if err != nil {
		t.Fatal(err)
	}

	// Without --force, should fail due to unresolved holds.
	_, err = RunWithBackend(Options{HomeDir: tmp, ID: "scout-test", Force: false}, fakeTeardown{})
	if err == nil {
		t.Fatal("should fail for scout with unresolved holds without --force")
	}
	if !strings.Contains(err.Error(), "unresolved decision hold") {
		t.Errorf("error should mention unresolved decision holds, got: %v", err)
	}

	// With --force, should proceed past safety checks.
	result, err := RunWithBackend(Options{HomeDir: tmp, ID: "scout-test", Force: true}, fakeTeardown{})
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

func TestCloseTerminalPhases_ClosesOpenKeyedPhases(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Create a status file with open keyed phases
	statusPath := filepath.Join(stateDir, "test-task.status")
	statusContent := `working [key=phase1]: Phase 1 started
working [key=phase2]: Phase 2 started
done [key=phase1]: Phase 1 completed
working [key=phase3]: Phase 3 in progress`
	if err := os.WriteFile(statusPath, []byte(statusContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Verify open phases before close
	openActs := classify.OpenActivities(statusPath)
	if len(openActs) != 2 {
		t.Fatalf("expected 2 open phases, got %d", len(openActs))
	}

	// Create a minimal meta file so Run can read it
	metaContent := "kind=ship\nbackend=tmux\nwindow=@1\n"
	if err := os.WriteFile(filepath.Join(stateDir, "test-task.meta"), []byte(metaContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Run teardown with --force to skip safety checks and reach cleanup
	result, err := RunWithBackend(Options{HomeDir: tmp, ID: "test-task", Force: true}, fakeTeardown{})
	if err != nil {
		t.Fatalf("teardown should not fail: %v", err)
	}

	// Verify steps mention phase closing
	foundClose := false
	for _, step := range result.Steps {
		if strings.Contains(step, "closed keyed phase") {
			foundClose = true
			break
		}
	}
	if !foundClose {
		t.Errorf("expected teardown steps to mention keyed phase closing, got: %v", result.Steps)
	}
}

func TestCloseTerminalPhases_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Create a status file where all phases are already closed
	statusPath := filepath.Join(stateDir, "test-task.status")
	statusContent := `working [key=phase1]: Phase 1 started
done [key=phase1]: Phase 1 completed
working [key=phase2]: Phase 2 started
resolved [key=phase2]: Phase 2 done`
	if err := os.WriteFile(statusPath, []byte(statusContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Verify no open phases before close (already closed)
	openActs := classify.OpenActivities(statusPath)
	if len(openActs) != 0 {
		t.Fatalf("expected 0 open phases, got %d", len(openActs))
	}

	// Create minimal meta
	metaContent := "kind=ship\nbackend=tmux\nwindow=@1\n"
	if err := os.WriteFile(filepath.Join(stateDir, "test-task.meta"), []byte(metaContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Run teardown -- should not emit any close events
	result, err := RunWithBackend(Options{HomeDir: tmp, ID: "test-task", Force: true}, fakeTeardown{})
	if err != nil {
		t.Fatalf("teardown should not fail: %v", err)
	}

	// Verify no "closed keyed phase" steps appeared (idempotent = no-op)
	for _, step := range result.Steps {
		if strings.Contains(step, "closed keyed phase") {
			t.Errorf("unexpected phase close for already-closed phases: %s", step)
		}
	}
}

func TestCloseTerminalPhases_NoStatusFile(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Create minimal meta but NO status file
	metaContent := "kind=ship\nbackend=tmux\nwindow=@1\n"
	if err := os.WriteFile(filepath.Join(stateDir, "test-task.meta"), []byte(metaContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Run teardown -- should not error on missing status file
	result, err := RunWithBackend(Options{HomeDir: tmp, ID: "test-task", Force: true}, fakeTeardown{})
	if err != nil {
		t.Fatalf("teardown should not fail: %v", err)
	}

	// Verify no close steps (no status file to read)
	for _, step := range result.Steps {
		if strings.Contains(step, "closed keyed phase") {
			t.Errorf("unexpected phase close with no status file: %s", step)
		}
	}
}

func TestUplinkCheck_MailboxOnlyKeyedOpenBlocks(t *testing.T) {
	home, receiver := t.TempDir(), t.TempDir()
	_, err := orchestrator.Report(orchestrator.ReportRequest{SenderHome: home, ReceiverHome: receiver, SenderRank: orchestrator.RankSoldier, SenderIdentity: "soldier", ReceiverRank: orchestrator.RankCaptain, ReceiverID: "captain", TaskID: "task:1", Key: "release", State: "done", Message: "complete"})
	if err != nil {
		t.Fatal(err)
	}
	if err := uplinkCheck(Options{HomeDir: home, ID: "task:1"}); err == nil {
		t.Fatal("keyed open report must block")
	}
}

func TestUplinkCheck_MailboxOnlyPendingWithoutOpenEvidenceBlocks(t *testing.T) {
	home := t.TempDir()
	env := &orchestrator.Envelope{Kind: "uplink-report", SenderRank: orchestrator.RankSoldier, SenderIdentity: "soldier", ReceiverRank: orchestrator.RankCaptain, ReceiverID: "captain", TaskID: "task:partial", Key: "x", Payload: "done"}
	if err := orchestrator.NewStore(home).WriteEnvelope(env); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.NewStore(home).WritePending(env); err != nil {
		t.Fatal(err)
	}
	if err := uplinkCheck(Options{HomeDir: home, ID: "task:partial"}); err == nil {
		t.Fatal("pending-only crash state must block")
	}
}

func TestUplinkCheck_WrongAckBlocksExactAckOpens(t *testing.T) {
	home, receiver := t.TempDir(), t.TempDir()
	result, err := orchestrator.Report(orchestrator.ReportRequest{SenderHome: home, ReceiverHome: receiver, SenderRank: orchestrator.RankSoldier, SenderIdentity: "soldier", ReceiverRank: orchestrator.RankCaptain, ReceiverID: "captain", TaskID: "task:ack", Key: "default", State: "done", Message: "complete"})
	if err != nil {
		t.Fatal(err)
	}
	env, _ := orchestrator.NewStore(receiver).ReadEnvelope("soldier", result.MessageID)
	wrong := &orchestrator.ProcessingAck{MessageID: env.MessageID, SenderRank: env.SenderRank, SenderIdentity: env.SenderIdentity, ReceiverRank: env.ReceiverRank, ReceiverID: env.ReceiverID, TaskID: env.TaskID, Key: env.Key, PayloadHash: orchestrator.PayloadHashHex("wrong"), ProcessedAt: time.Now().UnixNano(), Outcome: orchestrator.OutcomeAccepted}
	if err := orchestrator.NewStore(receiver).WriteAck(wrong); err != nil {
		t.Fatal(err)
	}
	if _, err := orchestrator.Recover(orchestrator.RecoverRequest{SenderHome: home, ReceiverHome: receiver, SenderIdentity: "soldier"}); err == nil {
		t.Fatal("wrong ack should fail")
	}
	if err := uplinkCheck(Options{HomeDir: home, ID: "task:ack"}); err == nil {
		t.Fatal("wrong ack must block")
	}
	os.Remove(filepath.Join(receiver, "state", orchestrator.InboxDir, "soldier", result.MessageID+".ack"))
	exact := *wrong
	exact.PayloadHash = env.PayloadHash
	if err := orchestrator.NewStore(receiver).WriteAck(&exact); err != nil {
		t.Fatal(err)
	}
	if _, err := orchestrator.Recover(orchestrator.RecoverRequest{SenderHome: home, ReceiverHome: receiver, SenderIdentity: "soldier"}); err != nil {
		t.Fatal(err)
	}
	if err := uplinkCheck(Options{HomeDir: home, ID: "task:ack"}); err != nil {
		t.Fatalf("exact ack should open teardown: %v", err)
	}
}

func TestUplinkCheck_NoMaterialStatusPasses(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	id := "test-task"
	metaContent := "kind=ship\nbackend=tmux\nwindow=@1\n"
	os.WriteFile(filepath.Join(stateDir, id+".meta"), []byte(metaContent), 0644)

	// No status file, no per-task obligations → should pass
	err := uplinkCheck(Options{HomeDir: home, ID: id})
	if err != nil {
		t.Fatalf("uplinkCheck should pass without status file: %v", err)
	}

	// Non-material status → should pass even with per-task obligations
	// Must init per-task obligations first for uplinkCheck to see them
	if err := turnend.InitTaskObligations(home, id, "uplink"); err != nil {
		t.Fatalf("init obligations: %v", err)
	}
	os.WriteFile(filepath.Join(stateDir, id+".status"), []byte("working: in progress\n"), 0644)
	err = uplinkCheck(Options{HomeDir: home, ID: id})
	if err != nil {
		t.Fatalf("uplinkCheck should pass with non-material status: %v", err)
	}
}

func TestUplinkCheck_MaterialStatusWithoutReportRelayFails(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	id := "test-task"
	metaContent := "kind=ship\nbackend=tmux\nwindow=@1\n"
	os.WriteFile(filepath.Join(stateDir, id+".meta"), []byte(metaContent), 0644)

	// Init per-task obligations so uplinkCheck checks this task
	if err := turnend.InitTaskObligations(home, id, "uplink"); err != nil {
		t.Fatalf("init obligations: %v", err)
	}

	// Write material status
	os.WriteFile(filepath.Join(stateDir, id+".status"), []byte("done: task complete\n"), 0644)

	// uplinkCheck should fail: material status + open ReportRelay
	err := uplinkCheck(Options{HomeDir: home, ID: id})
	if err == nil {
		t.Fatal("uplinkCheck should fail with material status and open ReportRelay")
	}
	if !strings.Contains(err.Error(), "report-relay") {
		t.Errorf("error should mention report-relay: %v", err)
	}
}

func TestUplinkCheck_MaterialStatusWithCompletedReportRelayPasses(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	id := "test-task"
	metaContent := "kind=ship\nbackend=tmux\nwindow=@1\n"
	os.WriteFile(filepath.Join(stateDir, id+".meta"), []byte(metaContent), 0644)

	// Init per-task obligations
	if err := turnend.InitTaskObligations(home, id, "uplink"); err != nil {
		t.Fatalf("init obligations: %v", err)
	}

	// Write material status
	os.WriteFile(filepath.Join(stateDir, id+".status"), []byte("done: task complete\n"), 0644)

	// Complete the per-task ReportRelay obligation
	found, err := turnend.CompleteTaskObligation(home, id, turnend.ReportRelay)
	if err != nil {
		t.Fatalf("CompleteTaskObligation error: %v", err)
	}
	if !found {
		t.Fatal("expected to find ReportRelay to complete")
	}

	// Now uplinkCheck should pass: material status but ReportRelay is closed
	err = uplinkCheck(Options{HomeDir: home, ID: id})
	if err != nil {
		t.Fatalf("uplinkCheck should pass after ReportRelay completed: %v", err)
	}
}

func TestRun_TeardownFailsOnOpenReportRelayWithMaterialStatus(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	// Create report dir so scout safety check passes
	reportDir := filepath.Join(home, "data", "test-uplink-fail")
	os.MkdirAll(reportDir, 0755)
	os.WriteFile(filepath.Join(reportDir, "report.md"), []byte("findings\n"), 0644)

	id := "test-uplink-fail"
	metaContent := "kind=scout\nbackend=tmux\nwindow=@1\n"
	os.WriteFile(filepath.Join(stateDir, id+".meta"), []byte(metaContent), 0644)

	// Init per-task obligations
	if err := turnend.InitTaskObligations(home, id, "uplink"); err != nil {
		t.Fatalf("init obligations: %v", err)
	}

	// Write material done status.
	os.WriteFile(filepath.Join(stateDir, id+".status"), []byte("done: task complete\n"), 0644)

	// Teardown should fail because uplink is not acknowledged
	_, err := RunWithBackend(Options{HomeDir: home, ID: id, Force: false}, fakeTeardown{})
	if err == nil {
		t.Fatal("teardown should fail with material status and open ReportRelay")
	}
	if !strings.Contains(err.Error(), "report-relay") {
		t.Errorf("error should mention report-relay: %v", err)
	}
}

func TestRun_TeardownForcePreservesEvidence(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	receiptsDir := filepath.Join(home, "state", ".terminal-receipts")
	os.MkdirAll(stateDir, 0755)
	os.MkdirAll(receiptsDir, 0755)

	id := "test-uplink-force"
	metaContent := "kind=scout\nbackend=tmux\nwindow=@1\n"
	os.WriteFile(filepath.Join(stateDir, id+".meta"), []byte(metaContent), 0644)

	// Write material status
	os.WriteFile(filepath.Join(stateDir, id+".status"), []byte("done: task complete\n"), 0644)
	// Write a receipt file
	os.WriteFile(filepath.Join(receiptsDir, id+".orchestrator.receipt"), []byte("state=done\n"), 0644)

	// With --force, teardown should proceed but preserve evidence
	result, err := RunWithBackend(Options{HomeDir: home, ID: id, Force: true}, fakeTeardown{})
	if err != nil {
		t.Fatalf("with --force should preserve evidence: %v", err)
	}

	// Verify evidence was preserved to .backup/
	backupPath := filepath.Join(stateDir, ".backup", id, id+".status")
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("evidence should be preserved at %s: %v", backupPath, err)
	}
	// Verify receipt was also preserved
	backupReceipt := filepath.Join(stateDir, ".backup", id, id+".orchestrator.receipt")
	if _, err := os.Stat(backupReceipt); err != nil {
		t.Fatalf("receipt evidence should be preserved at %s: %v", backupReceipt, err)
	}

	if !hasStepContaining(result.Steps, ".backup") {
		t.Errorf("result.Steps should mention .backup backup: %v", result.Steps)
	}
}

// hasStepContaining returns true if any step in the list contains substr.
func hasStepContaining(steps []string, substr string) bool {
	for _, s := range steps {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}
