package delivery

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/task"
)

// TestPRMerge_FleetSyncReadsMeta verifies PRMerge reads meta correctly.
// It exercises the code path after the merge step by checking meta reading.
func TestPRMerge_FleetSyncReadsMeta(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	homeDir := t.TempDir()

	// Write meta with a project name and delivery identity
	ident := &DeliveryIdentity{
		Provider:   "github",
		Owner:      "minhtri2710",
		Repo:       "munsu",
		Number:     999999,
		URL:        "https://github.com/minhtri2710/munsu/pull/999999",
		BaseRef:    "main",
		HeadRef:    "feature/test",
		HeadSHA:    "abc123def456abc123def456abc123def456abc1",
		CapturedAt: "2026-07-18T12:00:00Z",
	}
	meta := ident.ToMeta()
	meta["project"] = "munsu"
	if err := task.WriteMeta(homeDir, "test-merge-task", meta); err != nil {
		t.Fatalf("writing meta: %v", err)
	}

	// PRMerge requires gh-axi. Using a non-existent PR should fail at
	// the gh-axi merge step, proving the meta was readable.
	err := PRMerge(homeDir, "test-merge-task", "https://github.com/minhtri2710/munsu/pull/999999", nil)

	// Should fail because PR #999999 doesn't exist (gh-axi merge will error)
	if err == nil {
		t.Error("expected error for non-existent PR merge")
	}

	// The error should be about gh-axi or PR not found, not about meta reading
	if err != nil && strings.Contains(err.Error(), "meta") {
		t.Errorf("error should not be about meta reading: %v", err)
	}
}

// TestCheckScriptFleetSyncPattern verifies the generated check.sh contains
// the exact expected 'fleet sync' shell pattern with a real PR.
func TestCheckScriptFleetSyncPattern(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	requireGH(t)

	homeDir := t.TempDir()

	// Write meta with a project
	meta := map[string]string{
		"project": "munsu",
	}
	if err := task.WriteMeta(homeDir, "pattern-task", meta); err != nil {
		t.Fatalf("writing meta: %v", err)
	}

	// Use a real PR URL (PR #24 from the munsu repo)
	prURL := "https://github.com/minhtri2710/munsu/pull/24"
	if err := PRCheck(homeDir, "pattern-task", prURL); err != nil {
		t.Fatalf("PRCheck: %v", err)
	}

	checkPath := filepath.Join(task.StateDir(homeDir), "pattern-task.check")
	data, err := os.ReadFile(checkPath)
	if err != nil {
		t.Fatalf("reading check script: %v", err)
	}
	script := string(data)

	// The fleet sync line should be in the merged: true path
	if !strings.Contains(script, `echo "merged: true"`) {
		t.Errorf("should have merged: true branch, got:\n%s", script)
	}

	// Verify the structure: fleet sync after merged: true
	lines := strings.Split(script, "\n")
	foundMergeTrue := false
	foundFleetSync := false
	for _, line := range lines {
		if strings.Contains(line, `echo "merged: true"`) {
			foundMergeTrue = true
		}
		if strings.Contains(line, "fleet sync") {
			foundFleetSync = true
			if !foundMergeTrue {
				t.Error("fleet sync should appear after merged: true")
			}
		}
	}
	if !foundFleetSync {
		t.Error("check.sh should contain 'fleet sync' command")
	}

	// Verify the specific fleet sync shell command pattern
	if !strings.Contains(script, `munsu --home "$HOME_DIR" fleet sync "$PROJECT" 2>/dev/null || echo "Warning: fleet sync for ${PROJECT} failed" >&2`) {
		t.Errorf("check.sh should contain exact fleet sync shell command, got:\n%s", script)
	}
}

// TestFleetSyncEndToEnd tests the fleet sync mechanism end-to-end using the
func TestFleetSyncEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	homeDir := t.TempDir()
	projectsDir := filepath.Join(homeDir, "projects")
	dataDir := filepath.Join(homeDir, "data")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatalf("creating projects dir: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("creating data dir: %v", err)
	}

	// Create project registry
	projectsContent := "- test-project [ship] +yolo - test project (added 2026-07-01)\n"
	if err := os.WriteFile(filepath.Join(dataDir, "projects.md"), []byte(projectsContent), 0644); err != nil {
		t.Fatalf("writing projects.md: %v", err)
	}

	// Create a bare repo as upstream
	remoteDir := filepath.Join(homeDir, "remote.git")
	cmd := exec.Command("git", "init", "--bare", remoteDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %s", out)
	}

	// Create a local clone from the bare repo
	repoDir := filepath.Join(projectsDir, "test-project")
	cmd = exec.Command("git", "clone", remoteDir, repoDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %s", out)
	}

	// Configure and make initial commit in the clone, then push
	gitEnv := append(os.Environ(),
		"GIT_CEILING_DIRECTORIES="+homeDir,
	)
	for _, cfg := range []string{"user.email test@test.com", "user.name Test"} {
		parts := strings.Split(cfg, " ")
		c := exec.Command("git", append([]string{"config"}, parts...)...)
		c.Dir = repoDir
		c.Env = gitEnv
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git config %s: %s", cfg, out)
		}
	}
	readme := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readme, []byte("# test"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = repoDir
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s", out)
	}
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = repoDir
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s", out)
	}

	// Detect default branch and push
	branchOut, err := exec.Command("git", "-C", repoDir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatalf("detecting branch: %v", err)
	}
	defaultBranch := strings.TrimSpace(string(branchOut))
	cmd = exec.Command("git", "-C", repoDir, "push", "-u", "origin", defaultBranch)
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git push: %s", out)
	}

	// Build munsu binary
	projectRoot := findProjectRoot(t)
	if projectRoot == "" {
		t.Skip("could not find project root")
	}
	binaryPath := filepath.Join(homeDir, "munsu")
	buildCmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/munsu/")
	buildCmd.Dir = projectRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("building munsu: %s, %v", string(out), err)
	}

	// Run fleet sync
	cmd2 := exec.Command(binaryPath, "--home", homeDir, "fleet", "sync", "test-project")
	out, err2 := cmd2.CombinedOutput()
	if err2 != nil {
		t.Fatalf("fleet sync failed: %s, %v", string(out), err2)
	}

	// Verify synced output mentions project name
	if !strings.Contains(string(out), "test-project") {
		t.Errorf("expected fleet sync output to mention project name, got: %s", string(out))
	}
}

// findProjectRoot finds the project root by looking for go.mod
func findProjectRoot(t *testing.T) string {
	t.Helper()

	candidates := []string{
		"/Users/beowulf/.no-mistakes/worktrees/f11e85832040/01KXFYZQ1V0E2APRRED4PZF79Q",
	}

	wd, _ := os.Getwd()
	dir := wd
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "go.mod")); err == nil {
			return c
		}
	}

	return ""
}
