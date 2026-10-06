package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// excludeFixture returns a project repo with a user-written info/exclude and
// a detached captain worktree of it.
func excludeFixture(t *testing.T) (repo, home, userExclude string) {
	t.Helper()
	repo = newWorktreeFixture(t)
	userExclude = "# user rules\n*.swp\n/secret-notes"
	commonDir := strings.TrimSpace(gitTestRun(t, repo, "rev-parse", "--git-common-dir"))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(repo, commonDir)
	}
	if err := os.MkdirAll(filepath.Join(commonDir, "info"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commonDir, "info", "exclude"), []byte(userExclude), 0644); err != nil {
		t.Fatal(err)
	}
	home = filepath.Join(t.TempDir(), "captain")
	gitTestRun(t, repo, "worktree", "add", "--detach", home, "origin/main")
	return repo, home, userExclude
}

func checkIgnored(t *testing.T, dir, path string) bool {
	t.Helper()
	err := exec.Command("git", "-C", dir, "check-ignore", "-q", "--no-index", path).Run()
	if err == nil {
		return true
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return false
	}
	t.Fatalf("git check-ignore %s in %s: %v", path, dir, err)
	return false
}

func TestWriteWorktreeExcludesScopedToCaptainWorktree(t *testing.T) {
	repo, home, userExclude := excludeFixture(t)
	excludePath := filepath.Join(repo, ".git", "info", "exclude")

	for i := 0; i < 2; i++ {
		if err := writeWorktreeExcludes(home, captainWorktreeExcludes(piIntegrationPath)); err != nil {
			t.Fatalf("seed %d: %v", i+1, err)
		}
		data, err := os.ReadFile(excludePath)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != userExclude {
			t.Fatalf("seed %d changed shared info/exclude:\n%s", i+1, data)
		}
	}

	for _, p := range []string{"state/x", "sessions/x", piIntegrationPath} {
		if checkIgnored(t, repo, p) {
			t.Errorf("main checkout ignores %s; captain excludes leaked", p)
		}
		if !checkIgnored(t, home, p) {
			t.Errorf("captain worktree does not ignore %s", p)
		}
	}
	if !checkIgnored(t, repo, "x.swp") {
		t.Error("user info/exclude entry no longer applies in main checkout")
	}

	wtVal := strings.TrimSpace(gitTestRun(t, home, "config", "--worktree", "--get", "core.excludesFile"))
	gitDir := strings.TrimSpace(gitTestRun(t, home, "rev-parse", "--absolute-git-dir"))
	if wtVal != filepath.Join(gitDir, worktreeExcludeFileName) {
		t.Errorf("worktree core.excludesFile = %q, want file in %s", wtVal, gitDir)
	}
	if out, err := exec.Command("git", "-C", repo, "config", "--local", "--get", "core.excludesFile").Output(); err == nil {
		t.Errorf("common config holds core.excludesFile = %q", out)
	}
}

func TestWriteWorktreeExcludesReseedReplacesIntegrationPaths(t *testing.T) {
	_, home, _ := excludeFixture(t)
	if err := writeWorktreeExcludes(home, captainWorktreeExcludes(piIntegrationPath)); err != nil {
		t.Fatal(err)
	}
	if err := writeWorktreeExcludes(home, captainWorktreeExcludes(".other/integration.json")); err != nil {
		t.Fatal(err)
	}
	if checkIgnored(t, home, piIntegrationPath) {
		t.Errorf("old integration path %s still ignored after reseed", piIntegrationPath)
	}
	if !checkIgnored(t, home, ".other/integration.json") {
		t.Error("new integration path not ignored after reseed")
	}
	if got := gitTestRun(t, home, "config", "--worktree", "--get-all", "core.excludesFile"); strings.Count(got, "\n") > 0 {
		t.Errorf("core.excludesFile set more than once:\n%s", got)
	}
}

func TestWriteWorktreeExcludesSoldierEntriesFollowTheLaunchingHarness(t *testing.T) {
	repo, home, userExclude := excludeFixture(t)
	for _, tc := range []struct {
		harness             string
		ignored, notIgnored []string
	}{
		{"pi", []string{".soldier-charter.md", ".soldier-manifest.json", ".soldier-launch-guard-t-1/identity", ".pi/settings.json"}, []string{"src/.soldier-x", "state/x"}},
		{"claude", []string{".soldier-prompt.md", ".soldier-launch-guard-t-1/identity"}, []string{".pi/settings.json", "state/x"}},
	} {
		if err := writeWorktreeExcludes(home, soldierExcludeContent(tc.harness)); err != nil {
			t.Fatalf("%s: %v", tc.harness, err)
		}
		for _, p := range tc.ignored {
			if !checkIgnored(t, home, p) {
				t.Errorf("%s launch: soldier worktree does not ignore %s", tc.harness, p)
			}
			if checkIgnored(t, repo, p) {
				t.Errorf("%s launch: main checkout ignores %s; soldier excludes leaked", tc.harness, p)
			}
		}
		for _, p := range tc.notIgnored {
			if checkIgnored(t, home, p) {
				t.Errorf("%s launch: soldier worktree ignores %s", tc.harness, p)
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil || string(data) != userExclude {
		t.Errorf("shared info/exclude changed: %q, %v", data, err)
	}
}

func TestWriteWorktreeExcludesRefusesReinterpretedCommonConfig(t *testing.T) {
	for _, tc := range []struct{ key, value, want string }{
		{"core.bare", "true", "core.bare=true"},
		{"core.worktree", "/elsewhere", "core.worktree=/elsewhere"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			repo, home, _ := excludeFixture(t)
			gitTestRun(t, repo, "config", "--local", tc.key, tc.value)
			err := writeWorktreeExcludes(home, captainWorktreeExcludes(piIntegrationPath))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want refusal naming %s", err, tc.want)
			}
			if out, gerr := exec.Command("git", "-C", repo, "config", "--local", "--get", "extensions.worktreeConfig").Output(); gerr == nil {
				t.Errorf("refused seed enabled extensions.worktreeConfig = %q", out)
			}
		})
	}
}

func TestWriteWorktreeExcludesFailsClosedWhenWorktreeConfigCannotBeEnabled(t *testing.T) {
	repo, home, userExclude := excludeFixture(t)
	if err := os.WriteFile(filepath.Join(repo, ".git", "config.lock"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	err := writeWorktreeExcludes(home, captainWorktreeExcludes(piIntegrationPath))
	if err == nil || !strings.Contains(err.Error(), "enabling extensions.worktreeConfig") {
		t.Fatalf("err = %v, want worktreeConfig enable failure", err)
	}
	data, rerr := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if rerr != nil || string(data) != userExclude {
		t.Errorf("failed seed touched shared info/exclude: %q, %v", data, rerr)
	}
}

func TestWriteWorktreeExcludesFailsWhenWorktreeConfigCannotBeWritten(t *testing.T) {
	_, home, _ := excludeFixture(t)
	gitDir := strings.TrimSpace(gitTestRun(t, home, "rev-parse", "--absolute-git-dir"))
	if err := os.WriteFile(filepath.Join(gitDir, "config.worktree.lock"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	err := writeWorktreeExcludes(home, captainWorktreeExcludes(piIntegrationPath))
	if err == nil || !strings.Contains(err.Error(), "setting worktree core.excludesFile") {
		t.Fatalf("err = %v, want worktree core.excludesFile failure", err)
	}
}
