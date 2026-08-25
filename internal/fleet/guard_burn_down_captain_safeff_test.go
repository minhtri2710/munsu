package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type guardSafeFFFixture struct {
	parent  string
	captain string
}

func guardGitTestRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newGuardSafeFFFixture(t *testing.T) guardSafeFFFixture {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	if out, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	parent := filepath.Join(root, "parent")
	captain := filepath.Join(root, "captain")
	for _, dir := range []string{parent, captain} {
		if out, err := exec.Command("git", "clone", remote, dir).CombinedOutput(); err != nil {
			t.Fatalf("git clone: %v\n%s", err, out)
		}
		guardGitTestRun(t, dir, "config", "user.name", "Munsu Test")
		guardGitTestRun(t, dir, "config", "user.email", "munsu@example.invalid")
	}
	guardGitTestRun(t, parent, "checkout", "-b", "main")
	if err := os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("initial\n"), 0644); err != nil {
		t.Fatal(err)
	}
	guardGitTestRun(t, parent, "add", "AGENTS.md")
	guardGitTestRun(t, parent, "commit", "-m", "initial")
	before := guardGitTestRun(t, parent, "rev-parse", "HEAD")
	guardGitTestRun(t, parent, "push", "-u", "origin", "main")
	guardGitTestRun(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")
	guardGitTestRun(t, captain, "fetch", "origin", "main")
	guardGitTestRun(t, captain, "checkout", "-B", "main", before)
	guardGitTestRun(t, captain, "remote", "set-head", "origin", "main")
	guardGitTestRun(t, parent, "remote", "set-head", "origin", "main")
	return guardSafeFFFixture{parent: parent, captain: captain}
}

func TestGuardBurnDownSafeFFRefusesRemoteMismatch(t *testing.T) {
	f := newGuardSafeFFFixture(t)
	guardGitTestRun(t, f.captain, "remote", "set-url", "origin", "https://example.invalid/other/repo.git")

	_, _, _, err := safeFF(f.captain, f.parent)
	if err == nil || !strings.Contains(err.Error(), "differs from upstream remote") {
		t.Fatalf("safeFF error = %v, want remote-mismatch refusal", err)
	}
}

func TestGuardBurnDownSafeFFRefusesMalformedOriginHead(t *testing.T) {
	f := newGuardSafeFFFixture(t)
	guardGitTestRun(t, f.parent, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/upstream/main")

	_, _, _, err := safeFF(f.captain, f.parent)
	if err == nil || !strings.Contains(err.Error(), "unexpected origin/HEAD symbolic ref format") {
		t.Fatalf("safeFF error = %v, want malformed-origin-head refusal", err)
	}
}

func TestGuardBurnDownSafeFFRefusesDivergentHistory(t *testing.T) {
	f := newGuardSafeFFFixture(t)
	if err := os.WriteFile(filepath.Join(f.captain, "diverged.txt"), []byte("captain-only\n"), 0644); err != nil {
		t.Fatal(err)
	}
	guardGitTestRun(t, f.captain, "add", "diverged.txt")
	guardGitTestRun(t, f.captain, "commit", "-m", "diverge captain history")

	_, _, _, err := safeFF(f.captain, f.parent)
	if err == nil || !strings.Contains(err.Error(), "is not an ancestor of upstream default-branch commit") {
		t.Fatalf("safeFF error = %v, want divergent-history refusal", err)
	}
}
