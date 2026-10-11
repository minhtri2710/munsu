package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

func TestGuardBurnDownRegistrySyncRefusals(t *testing.T) {
	t.Run("project retirement requires current generation", func(t *testing.T) {
		r, _, _ := newTestRegistry(t)
		mustRegisterProject(t, r, "retire-generation-project")
		req := RetireProjectRequest{
			HomeID:       r.HomeID(),
			ProjectID:    mustProjectID(t, "retire-generation-project"),
			Precondition: domain.Of(2, 0),
			Reason:       "guard test",
		}
		if _, err := r.RetireProject(mustOp(t, "op-retire-generation-project", req), req); err == nil || !strings.Contains(err.Error(), "precondition generation must be 1") {
			t.Fatalf("RetireProject error = %v, want generation refusal", err)
		}
	})

	t.Run("sync requires a default branch", func(t *testing.T) {
		root := t.TempDir()
		remote := filepath.Join(root, "remote.git")
		dir := filepath.Join(root, "repo")
		registrySyncGit(t, root, "init", "--bare", remote)
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
		registrySyncGit(t, dir, "init")
		registrySyncGit(t, dir, "remote", "add", "origin", remote)
		if err := syncOne(dir); err == nil || !strings.Contains(err.Error(), "cannot resolve default branch") {
			t.Fatalf("syncOne no-default-branch error = %v, want default-branch refusal", err)
		}
	})

	t.Run("sync refuses an origin/HEAD outside refs/remotes/origin/", func(t *testing.T) {
		dir := registrySyncRepoWithOrigin(t)
		registrySyncGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/upstream/main")
		if err := syncOne(dir); err == nil || !strings.Contains(err.Error(), "unexpected origin/HEAD symbolic ref format") {
			t.Fatalf("syncOne malformed-origin-HEAD error = %v, want malformed refusal", err)
		}
	})

	t.Run("sync accepts a valid origin/HEAD", func(t *testing.T) {
		dir := registrySyncRepoWithOrigin(t)
		registrySyncGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
		if err := syncOne(dir); err != nil {
			t.Fatalf("syncOne with a valid origin/HEAD = %v, want nil", err)
		}
	})

	t.Run("sync refuses dirty working tree", func(t *testing.T) {
		dir := t.TempDir()
		registrySyncGit(t, dir, "init", "-b", "main")
		registrySyncGit(t, dir, "config", "user.email", "test@example.invalid")
		registrySyncGit(t, dir, "config", "user.name", "Registry Guard Test")
		writeFile(t, filepath.Join(dir, "tracked.txt"), "clean\n")
		registrySyncGit(t, dir, "add", "tracked.txt")
		registrySyncGit(t, dir, "commit", "-m", "initial")
		writeFile(t, filepath.Join(dir, "tracked.txt"), "dirty\n")
		if err := syncOne(dir); err == nil || !strings.Contains(err.Error(), "dirty working tree, skipping") {
			t.Fatalf("syncOne dirty-tree error = %v, want dirty-tree refusal", err)
		}
	})
}

// registrySyncRepoWithOrigin returns a clean repo on main whose origin has main.
func registrySyncRepoWithOrigin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	dir := filepath.Join(root, "repo")
	registrySyncGit(t, root, "init", "--bare", remote)
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	registrySyncGit(t, dir, "init", "-b", "main")
	registrySyncGit(t, dir, "config", "user.email", "test@example.invalid")
	registrySyncGit(t, dir, "config", "user.name", "Registry Guard Test")
	writeFile(t, filepath.Join(dir, "tracked.txt"), "clean\n")
	registrySyncGit(t, dir, "add", "tracked.txt")
	registrySyncGit(t, dir, "commit", "-m", "initial")
	registrySyncGit(t, dir, "remote", "add", "origin", remote)
	registrySyncGit(t, dir, "push", "origin", "main")
	registrySyncGit(t, dir, "config", "remote.origin.followRemoteHEAD", "never")
	return dir
}

func registrySyncGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
