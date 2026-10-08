package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
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
	testutil.FakeOnPath(t, "treehouse", "#!/bin/sh\n"+
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

func setupCleanOrphanWorktree(t *testing.T) (homeDir, repoPath, worktreePath, tracePath string) {
	t.Helper()
	homeDir = t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)
	initCLITestHome(t, homeDir)
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
	tracePath = filepath.Join(homeDir, "treehouse-returns")
	status, err := json.Marshal([]map[string]string{{"path": worktreePath, "lease_holder": ""}})
	if err != nil {
		t.Fatal(err)
	}
	testutil.FakeOnPath(t, "treehouse", "#!/bin/sh\n"+
		"if [ \"$1\" = \"status\" ] && [ \"$2\" = \"--json\" ]; then printf '%s\\n' '"+string(status)+"'; exit 0; fi\n"+
		"if [ \"$1\" = \"return\" ]; then printf '%s\\n' \"$3\" >> \""+tracePath+"\"; exit 0; fi\nexit 1\n")
	return homeDir, repoPath, worktreePath, tracePath
}

func TestWorktreeReturnAllowsCleanCanonicalOrphan(t *testing.T) {
	_, _, worktreePath, tracePath := setupCleanOrphanWorktree(t)
	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "return", worktreePath})
	if err := root.Execute(); err != nil {
		t.Fatalf("returning clean canonical orphan: %v", err)
	}
	trace, err := os.ReadFile(tracePath)
	if err != nil || !strings.Contains(string(trace), worktreePath) {
		t.Fatalf("provider return trace = %q, err=%v", trace, err)
	}
}

func TestWorktreeReturnRefusesDirtyAndUnanchoredContent(t *testing.T) {
	for _, name := range []string{"user-notes.txt", ".soldier-manifest.json"} {
		t.Run(name, func(t *testing.T) {
			_, _, worktreePath, tracePath := setupCleanOrphanWorktree(t)
			if err := os.WriteFile(filepath.Join(worktreePath, name), []byte("protected content\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			root := NewRootCommand()
			root.SetOut(new(strings.Builder))
			root.SetErr(new(strings.Builder))
			root.SetArgs([]string{"worktree", "return", worktreePath})
			err := root.Execute()
			if err == nil || !(strings.Contains(err.Error(), "dirty") || strings.Contains(err.Error(), "launch artifact")) {
				t.Fatalf("return error = %v, want content-preserving refusal", err)
			}
			got, readErr := os.ReadFile(filepath.Join(worktreePath, name))
			if readErr != nil || string(got) != "protected content\n" {
				t.Fatalf("protected content = %q, err=%v", got, readErr)
			}
			if _, statErr := os.Stat(tracePath); !os.IsNotExist(statErr) {
				t.Fatalf("provider was called for protected content: %v", statErr)
			}
		})
	}
}

func TestWorktreeReturnRejectsForceFlag(t *testing.T) {
	_, _, worktreePath, tracePath := setupCleanOrphanWorktree(t)
	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "return", "--force", worktreePath})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag: --force") {
		t.Fatalf("--force error = %v, want explicit flag rejection", err)
	}
	if _, err := os.Stat(tracePath); !os.IsNotExist(err) {
		t.Fatalf("provider was called with --force: %v", err)
	}
}

func TestWorktreeReturnRefusesClaimedWorktree(t *testing.T) {
	worktreePath, tracePath := setupClaimedWorktreeReturn(t)
	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "return", worktreePath})

	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "refusing to return claimed worktree") {
		t.Fatalf("return claimed worktree error = %v, want refusal", err)
	}
	if _, err := os.Stat(tracePath); !os.IsNotExist(err) {
		t.Fatalf("backend return should not run, trace stat error = %v", err)
	}
}

func TestWorktreeReturnRefusesAliases(t *testing.T) {
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
	for _, path := range []string{relative, alias} {
		root := NewRootCommand()
		root.SetOut(new(strings.Builder))
		root.SetErr(new(strings.Builder))
		root.SetArgs([]string{"worktree", "return", path})
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "refusing to return claimed worktree") {
			t.Fatalf("return %q error = %v, want refusal", path, err)
		}
	}
	if _, err := os.Stat(tracePath); !os.IsNotExist(err) {
		t.Fatalf("backend return should not run, trace stat error = %v", err)
	}
}

func TestWorktreeReturnRejectsForceForTaskBinding(t *testing.T) {
	worktreePath, tracePath := setupClaimedWorktreeReturn(t)
	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "return", "--force", worktreePath})

	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag: --force") {
		t.Fatalf("forced return error = %v, want removed force flag refusal", err)
	}
	if _, err := os.Stat(tracePath); !os.IsNotExist(err) {
		t.Fatalf("forced return reached provider despite canonical task binding: %v", err)
	}
}

func TestWorktreeReturnRefusesUnregisteredPath(t *testing.T) {
	_, _, worktreePath, tracePath := setupCleanOrphanWorktree(t)
	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "return", filepath.Join(filepath.Dir(worktreePath), "unlisted")})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "ownership is not established by provider status") {
		t.Fatalf("unregistered return error = %v, want provider ownership refusal", err)
	}
	if _, err := os.Stat(tracePath); !os.IsNotExist(err) {
		t.Fatalf("provider was called for an unregistered path: %v", err)
	}
}

func TestWorktreeReturnRejectsForceWithUnreadableClaims(t *testing.T) {
	worktreePath, tracePath := setupClaimedWorktreeReturn(t)
	homeDir := os.Getenv("MUNSU_HOME")
	metaPath, err := home.MetaFilePath(homeDir, "claimed-task")
	if err != nil {
		t.Fatalf("finding task meta: %v", err)
	}
	if err := os.WriteFile(metaPath, []byte(strings.Repeat("x", 128*1024)), 0o600); err != nil {
		t.Fatalf("corrupting task meta: %v", err)
	}

	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "return", "--force", worktreePath})

	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag: --force") {
		t.Fatalf("forced return with unreadable claims error = %v, want removed force flag refusal", err)
	}
	if _, err := os.Stat(tracePath); !os.IsNotExist(err) {
		t.Fatalf("forced return reached provider despite unreadable claims: %v", err)
	}
}
