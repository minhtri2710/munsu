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

func TestGuardBurnDownSafeFFRefusesMalformedOriginHead(t *testing.T) {
	f := newGuardSafeFFFixture(t)
	guardGitTestRun(t, f.parent, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/upstream/main")

	_, _, _, err := safeFF(f.captain, f.parent)
	if err == nil || !strings.Contains(err.Error(), "unexpected origin/HEAD symbolic ref format") {
		t.Fatalf("safeFF error = %v, want malformed-origin-head refusal", err)
	}
}

type safeFFFixture struct {
	parent  string
	captain string
	before  string
	after   string
}

func newSafeFFFixture(t *testing.T) safeFFFixture {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	if out, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	parent := filepath.Join(root, "parent")
	captain := filepath.Join(root, "captain")
	for _, dst := range []string{parent, captain} {
		if out, err := exec.Command("git", "clone", remote, dst).CombinedOutput(); err != nil {
			t.Fatalf("git clone: %v\n%s", err, out)
		}
		gitTestRun(t, dst, "config", "user.name", "Munsu Test")
		gitTestRun(t, dst, "config", "user.email", "munsu@example.invalid")
	}
	gitTestRun(t, parent, "checkout", "-b", "main")
	if err := os.WriteFile(filepath.Join(parent, ".gitignore"), []byte("state/ignored\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, parent, "add", ".gitignore", "AGENTS.md")
	gitTestRun(t, parent, "commit", "-m", "initial")
	before := gitTestRun(t, parent, "rev-parse", "HEAD")
	gitTestRun(t, parent, "push", "-u", "origin", "main")
	gitTestRun(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")

	gitTestRun(t, captain, "fetch", "origin", "main")
	gitTestRun(t, captain, "checkout", "-B", "main", before)
	gitTestRun(t, captain, "remote", "set-head", "origin", "main")
	gitTestRun(t, parent, "remote", "set-head", "origin", "main")

	if err := os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, parent, "commit", "-am", "advance instructions")
	after := gitTestRun(t, parent, "rev-parse", "HEAD")
	gitTestRun(t, parent, "push", "origin", "main")
	// Seed the already-local object without changing the general checkout.
	gitTestRun(t, captain, "fetch", "origin", "main")
	gitTestRun(t, captain, "reset", "--hard", before)
	return safeFFFixture{parent: parent, captain: captain, before: before, after: after}
}

// safeFF fast-forwards a captain clone only when it shares the upstream's
// remote, sits on the default branch or a detached HEAD, carries no tracked
// change or unignored untracked file, and is an ancestor of the upstream's
// default-branch commit. Each refusal leaves the captain HEAD untouched.
func TestSafeFF(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, f safeFFFixture)
		want  string // refusal text; empty means the captain fast-forwards
	}{
		{
			name: "refuses a different remote",
			setup: func(t *testing.T, f safeFFFixture) {
				gitTestRun(t, f.captain, "remote", "set-url", "origin", "https://example.invalid/other/repo.git")
			},
			want: "differs from upstream remote",
		},
		{
			name: "refuses a captain on another branch",
			setup: func(t *testing.T, f safeFFFixture) {
				gitTestRun(t, f.captain, "checkout", "-b", "feature")
			},
			want: "expected \"main\"",
		},
		{
			name: "refuses a tracked change",
			setup: func(t *testing.T, f safeFFFixture) {
				if err := os.WriteFile(filepath.Join(f.captain, "AGENTS.md"), []byte("dirty\n"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			want: "uncommitted changes: M AGENTS.md",
		},
		{
			name: "refuses an unignored untracked file",
			setup: func(t *testing.T, f safeFFFixture) {
				if err := os.WriteFile(filepath.Join(f.captain, "rogue.txt"), []byte("rogue\n"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			want: "uncommitted changes: ?? rogue.txt",
		},
		{
			name: "refuses an unignored file in a directory that also holds ignored files",
			setup: func(t *testing.T, f safeFFFixture) {
				exclude := filepath.Join(f.captain, ".git", "info", "exclude")
				if err := os.WriteFile(exclude, []byte("sub/*\n!sub/keep\n"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(f.captain, "sub"), 0755); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"x", "keep"} {
					if err := os.WriteFile(filepath.Join(f.captain, "sub", name), []byte("local\n"), 0644); err != nil {
						t.Fatal(err)
					}
				}
			},
			want: "uncommitted changes: ?? sub/",
		},
		{
			name: "refuses history that diverged from the upstream",
			setup: func(t *testing.T, f safeFFFixture) {
				if err := os.WriteFile(filepath.Join(f.captain, "diverged.txt"), []byte("captain-only\n"), 0644); err != nil {
					t.Fatal(err)
				}
				gitTestRun(t, f.captain, "add", "diverged.txt")
				gitTestRun(t, f.captain, "commit", "-m", "diverge captain history")
			},
			want: "is not an ancestor of upstream default-branch commit",
		},
		{
			name: "fast-forwards past a gitignored artifact",
			setup: func(t *testing.T, f safeFFFixture) {
				if err := os.MkdirAll(filepath.Join(f.captain, "state"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(f.captain, "state", "ignored"), []byte("local\n"), 0644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "fast-forwards a detached HEAD",
			setup: func(t *testing.T, f safeFFFixture) {
				gitTestRun(t, f.captain, "checkout", "--detach")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSafeFFFixture(t)
			tc.setup(t, f)
			head := gitTestRun(t, f.captain, "rev-parse", "HEAD")

			before, after, _, err := safeFF(f.captain, f.parent)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("safeFF: %v", err)
				}
				if before != f.before || after != f.after {
					t.Fatalf("safeFF = (%s, %s), want (%s, %s)", before, after, f.before, f.after)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("safeFF error = %v, want %q", err, tc.want)
			}
			if got := gitTestRun(t, f.captain, "rev-parse", "HEAD"); got != head {
				t.Fatalf("captain HEAD changed after refusal: %s -> %s", head, got)
			}
		})
	}
}
