package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaultBranch is the one table for the default-branch owner and its
// refusals: DefaultBranch, RemoteDefaultBranch, and the tangle check built on
// DefaultBranch. Each refusal has a blocking row and its nearest allowing row.
// The tangle check passes without reading the default branch when HEAD cannot
// be read (the empty repo) or is detached.
func TestDefaultBranch(t *testing.T) {
	tests := []struct {
		name       string
		empty      bool     // a repo with no commits and no refs
		detach     bool     // HEAD detached at the first commit
		local      []string // local branches; the first is the one checked out
		origin     []string // branches pushed to origin
		head       string   // origin/HEAD: a branch name, or a full ref to plant it malformed
		wantName   string
		wantRef    string
		wantRemote string // RemoteDefaultBranch
		wantErr    string // DefaultBranch and RemoteDefaultBranch both refuse with this
		wantTangle string // AssertNotTangled refuses with this; empty allows
	}{
		{name: "unresolved: no ref names a default", empty: true, wantErr: "cannot resolve default branch"},
		{name: "unresolved: on a branch no ref names a default", local: []string{"feature"}, wantErr: "cannot resolve default branch",
			wantTangle: "the default branch cannot be resolved"},
		{name: "allowed: detached HEAD, no ref names a default", local: []string{"feature"}, detach: true, wantErr: "cannot resolve default branch"},
		{name: "allowed: origin/HEAD with a slash in the branch", local: []string{"release/1"}, origin: []string{"release/1"}, head: "release/1",
			wantName: "release/1", wantRef: "release/1", wantRemote: "release/1"},
		{name: "malformed origin/HEAD is refused", local: []string{"main"}, origin: []string{"main"}, head: "refs/remotes/upstream/main",
			wantErr: "unexpected origin/HEAD symbolic ref format", wantTangle: "unexpected origin/HEAD symbolic ref format"},
		{name: "tangled: default known only as origin/main", local: []string{"feature"}, origin: []string{"main"},
			wantName: "main", wantRef: "origin/main", wantRemote: "main", wantTangle: "cannot spawn: p is on branch feature, not an isolated worktree. Use a detached HEAD or a worktree"},
		{name: "allowed: local master and origin/main, on master", local: []string{"master"}, origin: []string{"main"},
			wantName: "master", wantRef: "master", wantRemote: "main"},
		{name: "allowed: local main and origin/master, on main", local: []string{"main"}, origin: []string{"master"},
			wantName: "main", wantRef: "main", wantRemote: "master"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := defaultBranchRepo(t, tt.empty, tt.local, tt.origin, tt.head)
			if tt.detach {
				defaultBranchGit(t, repo, "git", "checkout", "--detach")
			}

			got, err := DefaultBranch(repo)
			_, remoteErr := RemoteDefaultBranch(repo)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("DefaultBranch = %v, want refusal %q", err, tt.wantErr)
				}
				if remoteErr == nil || !strings.Contains(remoteErr.Error(), tt.wantErr) {
					t.Fatalf("RemoteDefaultBranch = %v, want refusal %q", remoteErr, tt.wantErr)
				}
			} else {
				if err != nil || got != (Default{Name: tt.wantName, Ref: tt.wantRef}) {
					t.Fatalf("DefaultBranch = %+v, %v; want {%s %s}", got, err, tt.wantName, tt.wantRef)
				}
				remote, err := RemoteDefaultBranch(repo)
				if err != nil || remote != tt.wantRemote {
					t.Fatalf("RemoteDefaultBranch = %q, %v; want %q", remote, err, tt.wantRemote)
				}
			}
			if tt.head == "" {
				if _, err := OriginHeadBranch(repo); err == nil {
					t.Fatal("OriginHeadBranch resolved with origin/HEAD unset; it has no fallback")
				}
			}
			tangle := AssertNotTangled(repo, "p")
			switch {
			case tt.wantTangle == "" && tangle != nil:
				t.Fatalf("AssertNotTangled = %v, want allowed", tangle)
			case tt.wantTangle != "" && (tangle == nil || !strings.Contains(tangle.Error(), tt.wantTangle)):
				t.Fatalf("AssertNotTangled = %v, want refusal %q", tangle, tt.wantTangle)
			case tt.wantTangle != "" && tt.wantErr != "" && !strings.Contains(tangle.Error(), "git -C "+repo+" remote set-head origin -a"):
				t.Fatalf("AssertNotTangled = %v, want the set-head fix named", tangle)
			}
		})
	}
}

// defaultBranchRepo builds a repo whose local branches, origin branches and
// origin/HEAD are exactly the ones given.
func defaultBranchRepo(t *testing.T, empty bool, local, origin []string, head string) string {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if empty {
		defaultBranchGit(t, "", "git", "init", repo)
		return repo
	}
	remote := filepath.Join(root, "remote.git")
	defaultBranchGit(t, "", "git", "init", "--bare", remote)
	defaultBranchGit(t, "", "git", "init", "-b", local[0], repo)
	defaultBranchGit(t, repo, "git", "config", "user.email", "test@test.com")
	defaultBranchGit(t, repo, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	defaultBranchGit(t, repo, "git", "add", ".")
	defaultBranchGit(t, repo, "git", "commit", "-m", "init")
	defaultBranchGit(t, repo, "git", "remote", "add", "origin", remote)
	for _, b := range origin {
		defaultBranchGit(t, repo, "git", "push", "origin", "HEAD:refs/heads/"+b)
	}
	for _, b := range local[1:] {
		defaultBranchGit(t, repo, "git", "branch", b)
	}
	switch {
	case strings.HasPrefix(head, "refs/"):
		defaultBranchGit(t, repo, "git", "symbolic-ref", "refs/remotes/origin/HEAD", head)
	case head != "":
		defaultBranchGit(t, repo, "git", "remote", "set-head", "origin", head)
	}
	return repo
}

func defaultBranchGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v failed: %v\n%s", args, err, out)
	}
}
