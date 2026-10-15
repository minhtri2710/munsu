package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func setupClaimedWorktreeReturn(t *testing.T) (worktreePath, tracePath string) {
	t.Helper()
	homeDir := t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)
	initCLITestHome(t, homeDir)
	worktreePath = filepath.Join(t.TempDir(), "claimed-worktree")
	if err := os.Mkdir(worktreePath, 0o755); err != nil {
		t.Fatalf("creating worktree: %v", err)
	}
	if err := home.WriteMeta(homeDir, "claimed-task", map[string]string{"worktree": worktreePath}); err != nil {
		t.Fatalf("writing claimed task meta: %v", err)
	}
	auth := testAuthorityFor(t, homeDir)
	taskID := mustTaskIDFor(t, "claimed-task")
	create := taskauthority.CanonicalCreateRequest{HomeID: auth.HomeID(), TaskID: taskID, Owner: "general", Description: "claimed worktree", Kind: "ship", Reason: "worktree return test"}
	if _, err := auth.Create(mustCanonicalOp(t, "claimed-worktree-create", create), create); err != nil {
		t.Fatalf("creating canonical task: %v", err)
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatalf("reading canonical task: %v", err)
	}
	binding := taskauthority.WorktreeBinding{RepositoryIdentity: "repo", Path: worktreePath, GitDir: "git", CommonDir: "common", BaseHead: "head", LeaseID: "claimed-lease", FenceToken: "claimed-fence", BoundAtUnix: 1}
	bind := taskauthority.CanonicalBindWorktreeRequest{HomeID: auth.HomeID(), TaskID: taskID, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), Binding: binding, Reason: "worktree return test"}
	if _, err := auth.BindWorktree(mustCanonicalOp(t, "claimed-worktree-bind", bind), bind); err != nil {
		t.Fatalf("binding canonical worktree: %v", err)
	}
	tracePath = filepath.Join(homeDir, "treehouse-returns")
	fakeTreehouseProject(t, homeDir, "#!/bin/sh\n"+
		"if [ \"$1\" = \"status\" ] && [ \"$2\" = \"--json\" ]; then\n"+
		"  printf '[{\"path\":\"%s\",\"lease_holder\":\"\"}]\\n' '"+worktreePath+"'\n"+
		"  exit 0\n"+
		"fi\n"+
		"if [ \"$1\" = \"return\" ]; then\n"+
		"  printf '%s\\n' \"$3\" >> \""+tracePath+"\"\n"+
		"  exit 0\n"+
		"fi\n"+
		"exit 1\n")
	return worktreePath, tracePath
}

const treehouseProject = "treehouse-proj"

// fakeTreehouseProject registers treehouseProject in homeDir with a fake
// treehouse running script as its configured worktree tool.
func fakeTreehouseProject(t *testing.T, homeDir, script string) {
	t.Helper()
	tool := testutil.WriteFakeExecutable(t, filepath.Join(t.TempDir(), "treehouse"), script)
	if _, err := config.LoadFleetBase(homeDir); err != nil {
		if err := config.StoreFleetBase(homeDir, config.FleetBaseDocument{SchemaVersion: config.FleetBaseSchemaVersion, Config: config.FleetBaseConfig{Backend: "tmux"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fleet.Find(homeDir, treehouseProject); err != nil {
		if err := fleet.Add(homeDir, treehouseProject, t.TempDir(), true); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.StoreProjectOverlay(homeDir, treehouseProject, config.ProjectOverlay{Worktree: &config.ToolEntry{Adapter: "treehouse", Path: tool}}); err != nil {
		t.Fatal(err)
	}
}

func gitOrphanWorktree(t *testing.T) (repoPath, worktreePath string) {
	t.Helper()
	repoPath = filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", repoPath}, {"-C", repoPath, "config", "user.email", "test@example.invalid"}, {"-C", repoPath, "config", "user.name", "test"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"-C", repoPath, "add", "README.md"}, {"-C", repoPath, "commit", "-qm", "seed"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	worktreePath = filepath.Join(t.TempDir(), "orphan-worktree")
	if out, err := exec.Command("git", "-C", repoPath, "worktree", "add", "--detach", worktreePath, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("adding orphan worktree: %v\n%s", err, out)
	}
	return repoPath, worktreePath
}

func setupCleanOrphanWorktree(t *testing.T) (homeDir, repoPath, worktreePath, tracePath string) {
	t.Helper()
	homeDir = t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)
	initCLITestHome(t, homeDir)
	repoPath, worktreePath = gitOrphanWorktree(t)
	tracePath = filepath.Join(homeDir, "treehouse-returns")
	status, err := json.Marshal([]map[string]string{{"path": worktreePath, "lease_holder": ""}})
	if err != nil {
		t.Fatal(err)
	}
	fakeTreehouseProject(t, homeDir, "#!/bin/sh\n"+
		"if [ \"$1\" = \"status\" ] && [ \"$2\" = \"--json\" ]; then printf '%s\\n' '"+string(status)+"'; exit 0; fi\n"+
		"if [ \"$1\" = \"return\" ]; then printf '%s\\n' \"$3\" >> \""+tracePath+"\"; exit 0; fi\nexit 1\n")
	return homeDir, repoPath, worktreePath, tracePath
}

func TestWorktreeReturnGuards(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T) (paths []string, tracePath string)
		refused string
	}{
		{"block: claimed worktree by absolute, relative and symlink path", func(t *testing.T) ([]string, string) {
			worktreePath, tracePath := setupClaimedWorktreeReturn(t)
			alias := filepath.Join(t.TempDir(), "alias")
			if err := os.Symlink(worktreePath, alias); err != nil {
				t.Fatalf("creating symlink: %v", err)
			}
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatalf("getting cwd: %v", err)
			}
			relative, err := filepath.Rel(cwd, worktreePath)
			if err != nil {
				t.Fatalf("making relative path: %v", err)
			}
			return []string{worktreePath, relative, alias}, tracePath
		}, "refusing to return claimed worktree"},
		{"block: unclaimed worktree with dirty content", func(t *testing.T) ([]string, string) {
			_, _, worktreePath, tracePath := setupCleanOrphanWorktree(t)
			if err := os.WriteFile(filepath.Join(worktreePath, "user-notes.txt"), []byte("protected content\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return []string{worktreePath}, tracePath
		}, "worktree contains dirty, untracked, or ignored content; refusing return"},
		{"allow: clean canonical orphan", func(t *testing.T) ([]string, string) {
			_, _, worktreePath, tracePath := setupCleanOrphanWorktree(t)
			return []string{worktreePath}, tracePath
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			paths, tracePath := tc.setup(t)
			for _, path := range paths {
				root := NewRootCommand()
				root.SetOut(new(strings.Builder))
				root.SetErr(new(strings.Builder))
				root.SetArgs([]string{"worktree", "return", "--project", treehouseProject, path})
				err := root.Execute()
				if tc.refused == "" {
					if err != nil {
						t.Fatalf("return %q: %v", path, err)
					}
					continue
				}
				if err == nil || !strings.Contains(err.Error(), tc.refused) {
					t.Fatalf("return %q error = %v, want %q", path, err, tc.refused)
				}
			}
			trace, err := os.ReadFile(tracePath)
			if tc.refused == "" {
				if err != nil || !strings.Contains(string(trace), paths[0]) {
					t.Fatalf("provider return trace = %q, err=%v, want %q", trace, err, paths[0])
				}
				return
			}
			if !os.IsNotExist(err) {
				t.Fatalf("provider was called for a refused return: trace=%q err=%v", trace, err)
			}
		})
	}
}

func TestWorktreeReturnRejectsForceFlag(t *testing.T) {
	worktreePath, tracePath := setupClaimedWorktreeReturn(t)
	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "return", "--project", treehouseProject, "--force", worktreePath})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag: --force") {
		t.Fatalf("--force error = %v, want explicit flag rejection", err)
	}
	if _, err := os.Stat(tracePath); !os.IsNotExist(err) {
		t.Fatalf("provider was called with --force: %v", err)
	}
}
