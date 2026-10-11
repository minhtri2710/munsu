package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

func TestSyncSeparatesSyncedAndStuck(t *testing.T) {
	root := t.TempDir()
	fleetHome := filepath.Join(root, "home")
	if _, err := home.Init(fleetHome); err != nil {
		t.Fatal(err)
	}

	remote := filepath.Join(root, "remote.git")
	gitSyncTestRun(t, root, "init", "--bare", remote)
	projectsDir := filepath.Join(fleetHome, "projects")
	project := filepath.Join(projectsDir, "synced")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatal(err)
	}
	gitSyncTestRun(t, root, "clone", remote, project)
	gitSyncTestRun(t, project, "checkout", "-b", "main")
	gitSyncTestRun(t, project, "config", "user.name", "Munsu Test")
	gitSyncTestRun(t, project, "config", "user.email", "munsu@example.invalid")
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("synced\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitSyncTestRun(t, project, "add", "README.md")
	gitSyncTestRun(t, project, "commit", "-m", "initial")
	gitSyncTestRun(t, project, "push", "-u", "origin", "main")
	gitSyncTestRun(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")

	stuck := filepath.Join(projectsDir, "stuck")
	if err := os.MkdirAll(stuck, 0755); err != nil {
		t.Fatal(err)
	}
	r, err := openRegistry(fleetHome)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"synced", "stuck"} {
		id, err := domain.NewProjectID(name)
		if err != nil {
			t.Fatal(err)
		}
		rev, err := r.ProjectRevision()
		if err != nil {
			t.Fatal(err)
		}
		req := RegisterProjectRequest{
			HomeID: r.HomeID(), ProjectID: id, Name: name, Path: "remote:" + name,
			Precondition: preconditionOf(rev), Reason: "sync test",
		}
		if _, err := r.RegisterProject(opFor(req), req); err != nil {
			t.Fatalf("RegisterProject(%s): %v", name, err)
		}
	}

	result, err := Sync(fleetHome, "")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(result.Synced) != 1 || result.Synced[0] != "synced" {
		t.Fatalf("Sync result = %+v, want Synced=[synced]", result)
	}
	if len(result.Stuck) != 1 || !strings.HasPrefix(result.Stuck[0], "stuck:") {
		t.Fatalf("Stuck = %v, want one stuck project", result.Stuck)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("Errors = %v, want none for per-project outcomes", result.Errors)
	}
}

func gitSyncTestRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestSyncOneRefusals pins the fleet-sync refusals: a checkout whose git
// status cannot be read, and a git lock file left in the checkout, report the
// project stuck with git's error; a clean checkout fast-forwards.
func TestSyncOneRefusals(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, project string)
		want    []string
	}{
		{
			name: "unreadable git status is stuck",
			prepare: func(t *testing.T, project string) {
				if err := os.WriteFile(filepath.Join(project, ".git", "index"), []byte("not an index"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"reading git status: ", "index file smaller than expected"},
		},
		{
			name: "leftover lock is stuck with git's error",
			prepare: func(t *testing.T, project string) {
				if err := os.WriteFile(filepath.Join(project, ".git", "index.lock"), nil, 0644); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"ff-merge failed", "index.lock", "File exists"},
		},
		{
			name:    "clean checkout syncs",
			prepare: func(*testing.T, string) {},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			remote := filepath.Join(root, "remote.git")
			gitSyncTestRun(t, root, "init", "--bare", "-b", "main", remote)
			upstream := filepath.Join(root, "upstream")
			gitSyncTestRun(t, root, "clone", remote, upstream)
			gitSyncTestRun(t, upstream, "config", "user.name", "Munsu Test")
			gitSyncTestRun(t, upstream, "config", "user.email", "munsu@example.invalid")
			commitSyncTestFile(t, upstream, "first")
			gitSyncTestRun(t, upstream, "push", "origin", "HEAD:main")
			project := filepath.Join(root, "project")
			gitSyncTestRun(t, root, "clone", remote, project)
			commitSyncTestFile(t, upstream, "second")
			gitSyncTestRun(t, upstream, "push", "origin", "HEAD:main")
			tt.prepare(t, project)

			err := syncOne(project)
			if len(tt.want) == 0 {
				if err != nil {
					t.Fatalf("syncOne: %v", err)
				}
				if _, statErr := os.Stat(filepath.Join(project, "second")); statErr != nil {
					t.Fatalf("clean checkout did not fast-forward: %v", statErr)
				}
				return
			}
			for _, want := range tt.want {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("syncOne = %v, want error containing %q", err, want)
				}
			}
		})
	}
}

func commitSyncTestFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitSyncTestRun(t, dir, "add", name)
	gitSyncTestRun(t, dir, "commit", "-m", name)
}
