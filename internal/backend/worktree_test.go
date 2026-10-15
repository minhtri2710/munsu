//go:build integration

package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestGitWorktree_GetAndReturnWorktree(t *testing.T) {
	// Create a real git repo with a commit, then exercise git worktree
	// via gitWorktreeProvider directly.
	repoDir := t.TempDir()
	runCmd(t, repoDir, "git", "init")
	runCmd(t, repoDir, "git", "config", "user.email", "test@test.com")
	runCmd(t, repoDir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, repoDir, "git", "add", ".")
	runCmd(t, repoDir, "git", "commit", "-m", "initial")

	p := &gitWorktreeProvider{homeDir: t.TempDir()}

	// Get via git worktree
	wtPath, err := p.Get(repoDir, false)
	if err != nil {
		t.Fatalf("git worktree Get failed: %v", err)
	}
	if wtPath == "" {
		t.Fatal("expected non-empty worktree path from git worktree")
	}

	// Verify it is a real linked worktree: git marks one with a .git *file*
	// pointing at the common dir, where a primary checkout has a directory.
	// Asserted locally — internal/backend does not classify checkout identity
	// and must not import internal/fleet to do so.
	info, err := os.Lstat(filepath.Join(wtPath, ".git"))
	if err != nil {
		t.Fatalf("stat .git in worktree: %v", err)
	}
	if info.IsDir() {
		t.Error("expected linked worktree from git worktree, got a primary checkout (.git is a directory)")
	}

	// Return the worktree
	if err := p.Return(wtPath); err != nil {
		t.Fatalf("git worktree Return failed: %v", err)
	}

	// Verify worktree directory is removed
	if _, err := os.Stat(wtPath); err == nil {
		t.Error("worktree still exists after Return")
	}
}

func TestGitWorktree_Status(t *testing.T) {
	// Create a real git repo, get a worktree, then check status lists it.
	repoDir := t.TempDir()
	runCmd(t, repoDir, "git", "init")
	runCmd(t, repoDir, "git", "config", "user.email", "test@test.com")
	runCmd(t, repoDir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, repoDir, "git", "add", ".")
	runCmd(t, repoDir, "git", "commit", "-m", "initial")

	p := &gitWorktreeProvider{homeDir: t.TempDir()}

	// Status before Get should be empty or no-error
	_, err := p.Status()
	if err != nil {
		t.Fatalf("git worktree Status failed: %v", err)
	}

	// Get a worktree
	wtPath, err := p.Get(repoDir, false)
	if err != nil {
		t.Fatalf("git worktree Get failed: %v", err)
	}
	defer func() {
		_ = p.Return(wtPath)
	}()

	// Status after Get should include the worktree path
	out2, err := p.Status()
	if err != nil {
		t.Fatalf("git worktree Status after Get failed: %v", err)
	}
	if !strings.Contains(out2, wtPath) {
		t.Errorf("Status output should contain worktree path %q, got: %q", wtPath, out2)
	}
}

func TestReservedGitWorktreePath(t *testing.T) {
	homeDir := t.TempDir()
	repoPath := filepath.Join(t.TempDir(), "repo")
	reservationID := "reservation-1"

	path, ok := ReservedGitWorktreePath(homeDir, repoPath, reservationID)
	if !ok {
		t.Fatal("ReservedGitWorktreePath returned ok=false for a non-empty reservation")
	}
	want := filepath.Join(homeDir, ".worktrees", stableHash(repoPath+"\x00"+reservationID))
	if path != want {
		t.Fatalf("ReservedGitWorktreePath = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("ReservedGitWorktreePath created %q: stat error = %v", path, err)
	}

	if path, ok := ReservedGitWorktreePath(homeDir, repoPath, ""); ok || path != "" {
		t.Fatalf("ReservedGitWorktreePath with empty reservation = (%q, %v), want (\"\", false)", path, ok)
	}
}

func TestProviderSelection(t *testing.T) {
	testutil.FakeOnPath(t, "treehouse", "#!/bin/sh\nexit 0\n")
	p, err := selectProvider(t.TempDir(), "")
	if err != nil {
		t.Fatalf("selectProvider without a worktree tool: %v", err)
	}
	if _, ok := p.(*gitWorktreeProvider); !ok {
		t.Errorf("no worktree tool with treehouse on PATH selected %T, want git worktree", p)
	}
	tool := fakeTreehouse(t, fakeCmd{stdout: "ok"})
	p, err = selectProvider("", tool)
	if err != nil {
		t.Fatalf("selectProvider with a configured treehouse: %v", err)
	}
	if th, ok := p.(*treehouseProvider); !ok || th.bin != tool {
		t.Errorf("configured treehouse selected %#v, want treehouse at %s", p, tool)
	}
}

func TestGet_EmptyPath_ReturnsError(t *testing.T) {
	// A treehouse that returns empty stdout.
	tool := fakeTreehouse(t, fakeCmd{})

	repoDir := t.TempDir()

	// Get without lease should return empty -> error
	_, err := GetWorktree(t.TempDir(), tool, repoDir, false)
	if err == nil {
		t.Fatal("expected error for empty path from treehouse, got nil")
	}
	if !strings.Contains(err.Error(), "empty path") {
		t.Errorf("expected 'empty path' in error, got: %v", err)
	}
}

func TestGet_WithLease_ReturnsPath(t *testing.T) {
	// A treehouse that returns a path.
	tool := fakeTreehouse(t, fakeCmd{stdout: "/tmp/wt-12345"})

	repoDir := t.TempDir()

	path, err := GetWorktree(t.TempDir(), tool, repoDir, true)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if path != "/tmp/wt-12345" {
		t.Errorf("expected path '/tmp/wt-12345', got: %q", path)
	}
	if path != "/tmp/wt-12345" {
		t.Errorf("expected path '/tmp/wt-12345', got: %q", path)
	}
}

func newCleanGitWorktree(t *testing.T) (repoDir, worktreePath string) {
	t.Helper()
	repoDir = t.TempDir()
	runCmd(t, repoDir, "git", "init")
	runCmd(t, repoDir, "git", "config", "user.email", "test@test.com")
	runCmd(t, repoDir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "tracked"), []byte("clean\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, repoDir, "git", "add", "tracked")
	runCmd(t, repoDir, "git", "commit", "-m", "initial")
	worktreePath = filepath.Join(t.TempDir(), "worktree")
	runCmd(t, repoDir, "git", "worktree", "add", "--detach", worktreePath, "HEAD")
	return repoDir, worktreePath
}

func TestReturn_AbortedExit0_ReturnsError(t *testing.T) {
	// treehouse return without --force prompts interactively when the worktree
	// has uncommitted changes. With stdin closed / no tty, it prints "Aborted"
	// and exits 0. Our Return must detect this as an error.
	tool := fakeTreehouse(t, fakeCmd{stdout: "Aborted"})
	_, worktreePath := newCleanGitWorktree(t)

	err := ReturnWorktree(t.TempDir(), tool, worktreePath)
	if err == nil {
		t.Fatal("expected error for Aborted output, got nil")
	}
	if !strings.Contains(err.Error(), "Aborted") {
		t.Errorf("expected 'Aborted' in error, got: %v", err)
	}
}

func TestReturn_Clean_ReturnsNil(t *testing.T) {
	// Clean success: "worktree returned to pool" and exit 0.
	tool := fakeTreehouse(t, fakeCmd{stdout: "worktree returned to pool"})
	_, worktreePath := newCleanGitWorktree(t)

	err := ReturnWorktree(t.TempDir(), tool, worktreePath)
	if err != nil {
		t.Fatalf("expected no error for clean return, got: %v", err)
	}
}

func TestReturn_ErrorExit_ReturnsError(t *testing.T) {
	// treehouse exit non-zero (e.g. path not found)
	tool := fakeTreehouse(t, fakeCmd{stdout: "path not found", exitCode: 1})
	_, worktreePath := newCleanGitWorktree(t)

	err := ReturnWorktree(t.TempDir(), tool, worktreePath)
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil")
	}
}

func runCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd %s %v failed: %v\n%s", name, args, err, string(out))
	}
}

func TestGet_RelativeRepoPath_PassesAbsolute(t *testing.T) {
	// Regression: treehouseProvider.Get must pass an ABSOLUTE repo path to
	// `treehouse get` and use it as cmd.Dir. A relative repoPath previously
	// produced a cryptic "not a directory" error.
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	tool := fakeTreehouse(t, fakeCmd{stdout: "/tmp/wt-rel", argsFile: argsFile})

	// Build a real repo dir and reference it by a RELATIVE path from its parent.
	repoDir := t.TempDir()
	parent := filepath.Dir(repoDir)
	rel := filepath.Base(repoDir)

	// chdir to parent so the relative name resolves (package tests are sequential).
	oldCwd, _ := os.Getwd()
	defer os.Chdir(oldCwd)
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}

	path, err := GetWorktree(t.TempDir(), tool, rel, true)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if path != "/tmp/wt-rel" {
		t.Errorf("expected path '/tmp/wt-rel', got: %q", path)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(parts) < 2 {
		t.Fatalf("expected >=2 args recorded, got %#v", parts)
	}
	if parts[0] != "get" {
		t.Errorf("first arg = %q, want \"get\"", parts[0])
	}
	if !filepath.IsAbs(parts[1]) {
		t.Errorf("repo arg = %q, want an absolute path", parts[1])
	}
	want, _ := filepath.Abs(rel)
	if parts[1] != want {
		t.Errorf("repo arg = %q, want %q", parts[1], want)
	}
}
