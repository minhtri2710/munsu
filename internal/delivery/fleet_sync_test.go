package delivery

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/task"
)

// requireGH skips the test if the GitHub CLI is not available.
func requireGH(t *testing.T) {
	t.Helper()
	if os.Getenv("GH_TOKEN") == "" && os.Getenv("GITHUB_TOKEN") == "" {
		t.Skip("skipping test: GH_TOKEN or GITHUB_TOKEN not set")
	}
	if err := exec.Command("gh", "--help").Run(); err != nil {
		t.Skipf("skipping test: gh CLI not available: %v", err)
	}
}

// TestPRCheck_GeneratesCheckScriptWithFleetSync verifies that PRCheck generates a
// check.sh script including the fleet-sync command when project is in meta.
func TestPRCheck_GeneratesCheckScriptWithFleetSync(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	requireGH(t)

	homeDir := t.TempDir()

	// Write meta with a project name
	meta := map[string]string{
		"project": "munsu",
	}
	if err := task.WriteMeta(homeDir, "test-task", meta); err != nil {
		t.Fatalf("writing meta: %v", err)
	}

	// Use a real PR URL from the munsu repo (PR #24 is merged)
	prURL := "https://github.com/minhtri2710/munsu/pull/24"

	// Run PRCheck - this will call gh CLI to fetch the head SHA
	if err := PRCheck(homeDir, "test-task", prURL); err != nil {
		t.Fatalf("PRCheck: %v", err)
	}

	// Read the generated check.sh script
	checkPath := filepath.Join(task.StateDir(homeDir), "test-task.check.sh")
	data, err := os.ReadFile(checkPath)
	if err != nil {
		t.Fatalf("reading check script: %v", err)
	}
	script := string(data)

	// Verify the script contains the fleet-sync command
	if !strings.Contains(script, "fleet-sync") {
		t.Errorf("check.sh should contain fleet-sync command, got:\n%s", script)
	}

	// Verify the project is set correctly
	if !strings.Contains(script, `PROJECT="munsu"`) {
		t.Errorf("check.sh should have PROJECT=\"munsu\", got:\n%s", script)
	}

	// Verify home dir is set
	if !strings.Contains(script, `HOME_DIR="`+homeDir+`"`) {
		t.Errorf("check.sh should contain HOME_DIR with the correct path, got:\n%s", script)
	}

	// Verify the best-effort fleet-sync shell pattern
	if !strings.Contains(script, "munsu --home") {
		t.Errorf("check.sh should call munsu --home for fleet-sync, got:\n%s", script)
	}

	if !strings.Contains(script, "fleet-sync") {
		t.Errorf("check.sh should contain fleet-sync subcommand, got:\n%s", script)
	}

	if !strings.Contains(script, "2>/dev/null") {
		t.Errorf("check.sh should suppress fleet-sync stderr, got:\n%s", script)
	}

	if !strings.Contains(script, "Warning: fleet-sync") {
		t.Errorf("check.sh should print a warning on fleet-sync failure, got:\n%s", script)
	}
}

// TestPRCheck_GeneratesCheckScriptWithoutProjectFallback verifies that when
// project is NOT set in meta, PRCheck falls back to ghURL.Repo.
func TestPRCheck_GeneratesCheckScriptWithoutProjectFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	requireGH(t)

	homeDir := t.TempDir()

	// Write meta WITHOUT a project name
	meta := map[string]string{
		"kind": "ship",
	}
	if err := task.WriteMeta(homeDir, "test-task-no-project", meta); err != nil {
		t.Fatalf("writing meta: %v", err)
	}

	// Use a real PR URL
	prURL := "https://github.com/minhtri2710/munsu/pull/24"

	if err := PRCheck(homeDir, "test-task-no-project", prURL); err != nil {
		t.Fatalf("PRCheck: %v", err)
	}

	// Read the generated check.sh script
	checkPath := filepath.Join(task.StateDir(homeDir), "test-task-no-project.check.sh")
	data, err := os.ReadFile(checkPath)
	if err != nil {
		t.Fatalf("reading check script: %v", err)
	}
	script := string(data)

	// Verify the script falls back to repo name as project
	if !strings.Contains(script, `PROJECT="munsu"`) {
		t.Errorf("check.sh should have PROJECT=\"munsu\" (repo fallback), got:\n%s", script)
	}

	// Verify fleet-sync is still present
	if !strings.Contains(script, "fleet-sync") {
		t.Errorf("check.sh should contain fleet-sync even without explicit project, got:\n%s", script)
	}
}
