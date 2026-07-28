package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoMistakesConfig_IntentTargeted verifies that .no-mistakes.yaml uses
// the intent-targeted helper script for its test command, not the full
// "go test ./..." — CI handles full verification.
func TestNoMistakesConfig_IntentTargeted(t *testing.T) {
	// Resolve repo root from the test's working directory.
	repoRoot := resolveRepoRoot(t)
	configPath := filepath.Join(repoRoot, ".no-mistakes.yaml")

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading .no-mistakes.yaml: %v", err)
	}
	content := string(data)

	// The test command must reference the intent-targeted script, not the
	// full "go test ./..." — local gates run narrowed tests.
	if !strings.Contains(content, "scripts/test-intent.sh") {
		t.Error(".no-mistakes.yaml: commands.test must reference scripts/test-intent.sh")
	}
	if strings.Contains(content, "test: 'go test ./...'") {
		t.Error(".no-mistakes.yaml: commands.test must NOT be the full 'go test ./...' command")
	}
}

// TestCI_YAML_RetainsFullSuite verifies that .github/workflows/ci.yml uses the
// full "go test ./..." — CI must never be narrowed to match the local gate.
func TestCI_YAML_RetainsFullSuite(t *testing.T) {
	repoRoot := resolveRepoRoot(t)
	ciPath := filepath.Join(repoRoot, ".github", "workflows", "ci.yml")

	data, err := os.ReadFile(ciPath)
	if err != nil {
		t.Fatalf("reading .github/workflows/ci.yml: %v", err)
	}
	content := string(data)

	// CI must retain the full build, vet, and test suite.
	if !strings.Contains(content, "go build ./...") {
		t.Error(".github/workflows/ci.yml: must retain go build ./...")
	}
	if !strings.Contains(content, "go vet ./...") {
		t.Error(".github/workflows/ci.yml: must retain go vet ./...")
	}
	if !strings.Contains(content, "go test ./...") {
		t.Error(".github/workflows/ci.yml: must retain go test ./...")
	}
}

// TestIntentScript_SelectsChangedPackage creates a temp git+go fixture with
// two packages, modifies one, and verifies the script --print-packages output
// selects only the changed package (alpha), not the unrelated package (beta).
func TestIntentScript_SelectsChangedPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping intent script fixture test in short mode")
	}

	repoRoot := resolveRepoRoot(t)
	scriptPath := filepath.Join(repoRoot, "scripts", "test-intent.sh")

	fixture := t.TempDir()
	initFixtureRepo(t, fixture)

	// Copy the script into fixture's scripts/ dir (the script expects to live
	// in a scripts/ subdirectory of the repo root via "$(dirname "$0")/..").
	scriptsDir := filepath.Join(fixture, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatal(err)
	}
	localScript := filepath.Join(scriptsDir, "test-intent.sh")
	copyFile(t, scriptPath, localScript)

	// Run the script with --print-packages on the fixture where alpha was
	// changed but beta was not. It should SELECT: the alpha package.
	cmd := exec.Command(localScript, "--print-packages")
	cmd.Dir = fixture
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test-intent.sh --print-packages: %v\noutput: %s", err, out)
	}
	output := strings.TrimSpace(string(out))

	// The result marker is the last line of output (SELECTED: or FULL_SUITE:).
	lines := strings.Split(output, "\n")
	lastLine := strings.TrimSpace(lines[len(lines)-1])

	t.Logf("last line: %q", lastLine)

	if !strings.HasPrefix(lastLine, "SELECTED:") {
		t.Fatalf("expected SELECTED: prefix on last line, got: %q (full: %q)", lastLine, output)
	}
	selected := strings.TrimPrefix(lastLine, "SELECTED:")
	selected = strings.TrimSpace(selected)

	// Must select alpha package.
	if !strings.Contains(selected, "test-fixture/alpha") {
		t.Errorf("expected selected packages to include test-fixture/alpha, got: %q", selected)
	}
	// Must NOT select beta package.
	if strings.Contains(selected, "test-fixture/beta") {
		t.Errorf("selected packages should NOT include test-fixture/beta (unchanged), got: %q", selected)
	}
}

// TestIntentScript_ModuleChangeTriggersFullSuite verifies that changing
// go.mod triggers a full-suite fallback.
func TestIntentScript_ModuleChangeTriggersFullSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping intent script fixture test in short mode")
	}

	repoRoot := resolveRepoRoot(t)
	scriptPath := filepath.Join(repoRoot, "scripts", "test-intent.sh")

	fixture := t.TempDir()
	initFixtureRepo(t, fixture)

	// Copy the script into fixture's scripts/ dir.
	scriptsDir := filepath.Join(fixture, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatal(err)
	}
	localScript := filepath.Join(scriptsDir, "test-intent.sh")
	copyFile(t, scriptPath, localScript)

	// Add a go.mod change to trigger full suite.
	if err := os.WriteFile(filepath.Join(fixture, "go.mod"), []byte("module test\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, fixture, "git", "add", "go.mod")
	runCmd(t, fixture, "git", "commit", "-m", "change go.mod")

	cmd := exec.Command(localScript, "--print-packages")
	cmd.Dir = fixture
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test-intent.sh --print-packages: %v\noutput: %s", err, out)
	}
	output := strings.TrimSpace(string(out))

	// The result marker is the last line of output.
	lines := strings.Split(output, "\n")
	lastLine := strings.TrimSpace(lines[len(lines)-1])

	t.Logf("last line: %q", lastLine)

	// Must output FULL_SUITE:module-changed when go.mod changes.
	if !strings.HasPrefix(lastLine, "FULL_SUITE:") {
		t.Errorf("expected FULL_SUITE: prefix on last line, got: %q (full: %q)", lastLine, output)
	}
	if !strings.Contains(lastLine, "module-changed") {
		t.Errorf("expected FULL_SUITE:module-changed when go.mod changes, got: %q", lastLine)
	}
}

// TestIntentScript_NoChanges_TriggersFullSuite verifies that when no Go files
// are changed (only the initial commit exists on the feature branch), the
// script falls back to full suite.
func TestIntentScript_NoChanges_TriggersFullSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping intent script fixture test in short mode")
	}

	repoRoot := resolveRepoRoot(t)
	scriptPath := filepath.Join(repoRoot, "scripts", "test-intent.sh")

	fixture := t.TempDir()
	initFixtureRepo(t, fixture)

	// Copy the script into fixture's scripts/ dir.
	scriptsDir := filepath.Join(fixture, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatal(err)
	}
	localScript := filepath.Join(scriptsDir, "test-intent.sh")
	copyFile(t, scriptPath, localScript)

	// Remove the feature branch and stay on main with no changes.
	runCmd(t, fixture, "git", "checkout", "main")

	cmd := exec.Command(localScript, "--print-packages")
	cmd.Dir = fixture
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test-intent.sh --print-packages (no changes): %v\noutput: %s", err, out)
	}
	output := strings.TrimSpace(string(out))

	// The result marker is the last line of output.
	lines := strings.Split(output, "\n")
	lastLine := strings.TrimSpace(lines[len(lines)-1])

	t.Logf("last line: %q", lastLine)

	// Must output FULL_SUITE: since no Go files changed vs merge-base.
	if !strings.HasPrefix(lastLine, "FULL_SUITE:") {
		t.Errorf("expected FULL_SUITE: prefix on last line, got: %q (full: %q)", lastLine, output)
	}
}

// --- helpers ---

// resolveRepoRoot walks up from the test's working directory to find
// the repo root (containing .no-mistakes.yaml).
func resolveRepoRoot(t *testing.T) string {
	t.Helper()
	// Start from test package directory and walk up.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".no-mistakes.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no .no-mistakes.yaml found)")
		}
		dir = parent
	}
}

// initFixtureRepo creates a minimal Go module + git repo in dir with two
// packages (alpha and beta) and an initial commit on main.
func initFixtureRepo(t *testing.T, dir string) {
	t.Helper()
	runCmd(t, dir, "git", "init", "-b", "main")
	runCmd(t, dir, "git", "config", "user.email", "test@test.com")
	runCmd(t, dir, "git", "config", "user.name", "Test")

	// Create go.mod
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test-fixture\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create alpha package with test
	if err := os.MkdirAll(filepath.Join(dir, "alpha"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "alpha.go"), []byte("package alpha\n\nfunc Hello() string { return \"hello\" }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "alpha_test.go"), []byte("package alpha\n\nimport \"testing\"\n\nfunc TestHello(t *testing.T) {\n\tif Hello() != \"hello\" {\n\t\tt.Fatal(\"unexpected\")\n\t}\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create beta package with test (unrelated, should NOT be selected)
	if err := os.MkdirAll(filepath.Join(dir, "beta"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "beta", "beta.go"), []byte("package beta\n\nfunc World() string { return \"world\" }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "beta", "beta_test.go"), []byte("package beta\n\nimport \"testing\"\n\nfunc TestWorld(t *testing.T) {\n\tif World() != \"world\" {\n\t\tt.Fatal(\"unexpected\")\n\t}\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Initial commit on main
	runCmd(t, dir, "git", "add", ".")
	runCmd(t, dir, "git", "commit", "-m", "initial")

	// Create and switch to a feature branch
	runCmd(t, dir, "git", "checkout", "-b", "feature/test-change")

	// Modify alpha only
	if err := os.WriteFile(filepath.Join(dir, "alpha", "alpha.go"), []byte("package alpha\n\nfunc Hello() string { return \"hello world\" }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "alpha_test.go"), []byte("package alpha\n\nimport \"testing\"\n\nfunc TestHello(t *testing.T) {\n\tif Hello() != \"hello world\" {\n\t\tt.Fatal(\"unexpected\")\n\t}\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	runCmd(t, dir, "git", "add", ".")
	runCmd(t, dir, "git", "commit", "-m", "modify alpha")
}

func runCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd %s %v in %s: %v\n%s", name, args, dir, err, string(out))
	}
}

// copyFile copies src to dst, preserving mode.
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading %s: %v", src, err)
	}
	fi, err := os.Stat(src)
	if err != nil {
		t.Fatalf("stat %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, fi.Mode()); err != nil {
		t.Fatalf("writing %s: %v", dst, err)
	}
}
