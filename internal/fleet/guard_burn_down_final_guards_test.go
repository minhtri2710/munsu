package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/testutil"
)

type mockGlabRunner struct {
	data []byte
	err  error
}

func (m *mockGlabRunner) LookPath() (string, error) {
	return "/usr/bin/glab", nil
}

func (m *mockGlabRunner) Run(args ...string) ([]byte, error) {
	if len(args) > 0 {
		switch args[0] {
		case "--version":
			return []byte("glab version 1.25.0 (2023-01-01)"), nil
		case "api":
			if len(args) > 1 && args[1] == "--help" {
				return []byte("glab api [flags]"), nil
			}
		case "auth":
			if len(args) > 1 && args[1] == "status" {
				return []byte("Logged in to gitlab.com as testuser (authenticated)"), nil
			}
		}
	}
	if m.err != nil {
		return nil, m.err
	}
	return m.data, nil
}

// ----------------------------------------------------------------------------
// Group A: delivery_amend.go (4 guards)
// ----------------------------------------------------------------------------

// fakeGHPRView installs a gh that answers `gh pr view` with body.
func fakeGHPRView(t *testing.T, body string) {
	t.Helper()
	installFakeGH(t, ghReply{match: "pr view", stdout: body})
}

func TestFetchGitHubProviderSnapshot_EmptyRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"empty head or base ref", `{"state":"OPEN","headRefOid":"abc1234567890123456789012345678901234567","headRefName":"","baseRefName":"main"}`, "gh pr view returned empty headRefName or baseRefName"},
		{"empty headRefOid", `{"state":"OPEN","headRefOid":"","headRefName":"feat","baseRefName":"main"}`, "gh pr view returned empty headRefOid"},
		{"empty state", `{"state":"","headRefOid":"abc1234567890123456789012345678901234567","headRefName":"feat","baseRefName":"main"}`, "gh pr view returned empty state"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeGHPRView(t, tc.json)
			_, err := fetchGitHubProviderSnapshot(githubForgeStep, "https://github.com/owner/repo/pull/1")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("fetchGitHubProviderSnapshot err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFetchGitLabProviderSnapshot_EmptyRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"empty state", `{"state":"","sha":"abc1234567890123456789012345678901234567","source_branch":"feat","target_branch":"main"}`, "glab mr view returned empty state"},
		{"empty sha", `{"state":"opened","sha":"","source_branch":"feat","target_branch":"main"}`, "glab mr view returned empty sha"},
		{"empty source_branch", `{"state":"opened","sha":"abc1234567890123456789012345678901234567","source_branch":"","target_branch":"main"}`, "glab mr view returned empty source_branch or target_branch"},
		{"empty target_branch", `{"state":"opened","sha":"abc1234567890123456789012345678901234567","source_branch":"feat","target_branch":""}`, "glab mr view returned empty source_branch or target_branch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldRunner := glabRunnerFor
			t.Cleanup(func() { glabRunnerFor = oldRunner })
			glabRunnerFor = fixedGlabRunner(&mockGlabRunner{data: []byte(tc.json)})
			_, err := fetchGitLabProviderSnapshot(gitlabForgeStep, "https://gitlab.com/owner/project/-/merge_requests/1")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("fetchGitLabProviderSnapshot err = %v, want empty field error", err)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// Group B: delivery_github.go (1 guard)
// ----------------------------------------------------------------------------

func TestGHAxiClient_CaptureIdentity_EmptyHeadRefOid(t *testing.T) {
	scriptContent := "#!/bin/sh\necho 'headRefOid: \"\"'\necho 'headRefName: feat'\necho 'baseRefName: main'\n"
	scriptPath := testutil.WriteFakeExecutable(t, filepath.Join(t.TempDir(), "gh-axi"), scriptContent)
	old := ghAxiLookPath
	t.Cleanup(func() { ghAxiLookPath = old })
	ghAxiLookPath = func() (string, error) { return scriptPath, nil }

	c := &ghAxiClient{}
	_, err := c.CaptureIdentity("https://github.com/owner/repo/pull/1")
	if err == nil || !strings.Contains(err.Error(), "gh-axi api returned empty headRefOid") {
		t.Fatalf("CaptureIdentity err = %v, want empty headRefOid", err)
	}
}

// ----------------------------------------------------------------------------
// Group C: delivery_gitlab.go (4 guards)
// ----------------------------------------------------------------------------

func TestGitLabDeliveryProvider_Observe_NilClient(t *testing.T) {
	p := &gitlabDeliveryProvider{client: nil}
	_, err := p.Observe(domain.DeliveryIdentity{})
	if err == nil || !strings.Contains(err.Error(), "GitLab delivery capability is not composed") {
		t.Fatalf("Observe err = %v, want not composed", err)
	}
}

func TestGlabClient_CaptureIdentity_EmptySHA(t *testing.T) {
	c := &glabClient{runner: &mockGlabRunner{data: []byte(`{"sha":"","source_branch":"feat","target_branch":"main"}`)}}
	_, err := c.CaptureIdentity("https://gitlab.com/owner/project/-/merge_requests/1")
	if err == nil || !strings.Contains(err.Error(), "glab mr view returned empty sha") {
		t.Fatalf("CaptureIdentity err = %v, want empty sha", err)
	}
}

func TestGlabClient_CaptureIdentity_EmptySourceBranch(t *testing.T) {
	c := &glabClient{runner: &mockGlabRunner{data: []byte(`{"sha":"abc1234567890123456789012345678901234567","source_branch":"","target_branch":"main"}`)}}
	_, err := c.CaptureIdentity("https://gitlab.com/owner/project/-/merge_requests/1")
	if err == nil || !strings.Contains(err.Error(), "glab mr view returned empty source_branch") {
		t.Fatalf("CaptureIdentity err = %v, want empty source_branch", err)
	}
}

func TestGlabClient_CaptureIdentity_EmptyTargetBranch(t *testing.T) {
	c := &glabClient{runner: &mockGlabRunner{data: []byte(`{"sha":"abc1234567890123456789012345678901234567","source_branch":"feat","target_branch":""}`)}}
	_, err := c.CaptureIdentity("https://gitlab.com/owner/project/-/merge_requests/1")
	if err == nil || !strings.Contains(err.Error(), "glab mr view returned empty target_branch") {
		t.Fatalf("CaptureIdentity err = %v, want empty target_branch", err)
	}
}

// ----------------------------------------------------------------------------
// Group F: delivery_prstatus.go (2 guards)
// ----------------------------------------------------------------------------

func TestParseGLMergeStatus_EmptyState(t *testing.T) {
	_, err := parseGLMergeStatus([]byte(`{"state":"","sha":"abc1234567890123456789012345678901234567"}`))
	if err == nil || !strings.Contains(err.Error(), "glab mr view returned empty state") {
		t.Fatalf("parseGLMergeStatus err = %v, want empty state", err)
	}
}

func TestParseGLMergeStatus_EmptySHA(t *testing.T) {
	_, err := parseGLMergeStatus([]byte(`{"state":"opened","sha":""}`))
	if err == nil || !strings.Contains(err.Error(), "glab mr view returned empty sha") {
		t.Fatalf("parseGLMergeStatus err = %v, want empty sha", err)
	}
}

// ----------------------------------------------------------------------------
// Group G: delivery_resolve.go (2 guards)
// ----------------------------------------------------------------------------

func TestResolveTaskHome_EmptyHome(t *testing.T) {
	_, _, err := ResolveTaskHome("", "t1")
	if err == nil || !strings.Contains(err.Error(), "home directory is empty") {
		t.Fatalf("ResolveTaskHome err = %v, want home directory is empty", err)
	}
}

func TestResolveTaskHome_EmptyTaskID(t *testing.T) {
	_, _, err := ResolveTaskHome(t.TempDir(), "")
	if err == nil || !strings.Contains(err.Error(), "task id is empty") {
		t.Fatalf("ResolveTaskHome err = %v, want task id is empty", err)
	}
}

// ----------------------------------------------------------------------------
// Group H: delivery_reviewdiff.go (2 guards)
// ----------------------------------------------------------------------------

// TestReviewDiff_ProjectAndWorktreeRefusals pins the two refusals in
// ReviewDiff's preamble. The project is read from the canonical task, never
// from .meta: the blocking row names a project in .meta that the canonical
// task lacks, the passing row holds the project only in the canonical task.
func TestReviewDiff_ProjectAndWorktreeRefusals(t *testing.T) {
	for _, tc := range []struct {
		name      string
		project   string
		meta      func(worktree string) map[string]string
		wantError string
	}{
		{"canonical task without a project is refused although meta names one", "", func(wt string) map[string]string {
			return map[string]string{"project": "proj-1", "worktree": wt}
		}, "task t1 has no project"},
		{"canonical project passes to the worktree check without a meta project", "proj-1", func(string) map[string]string {
			return map[string]string{}
		}, "task t1 has no worktree path in meta"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth, homeDir := newFleetCanonical(t)
			req := taskauthority.CanonicalCreateRequest{
				HomeID: auth.HomeID(), TaskID: mustFleetTaskID(t, "t1"), Owner: "owner", Description: "work", Kind: "ship", Reason: "create",
			}
			if tc.project != "" {
				pid, err := domain.NewProjectID(tc.project)
				if err != nil {
					t.Fatal(err)
				}
				req.Project = pid
			}
			if _, err := auth.Create(mustFleetOperation(t, "op-create-t1", req), req); err != nil {
				t.Fatal(err)
			}
			if err := home.WriteMeta(homeDir, "t1", tc.meta(t.TempDir())); err != nil {
				t.Fatal(err)
			}
			err := ReviewDiff(homeDir, "t1")
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("ReviewDiff err = %v, want %q", err, tc.wantError)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// Group I: scope_scope.go (4 guards + 1 premise test)
// ----------------------------------------------------------------------------

func TestClassifyIdentity_NotDirectory(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(filePath, []byte("file"), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := ClassifyIdentity(filePath)
	if err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("ClassifyIdentity err = %v, want is not a directory", err)
	}
}

// runCmd is a test helper that runs a command in a directory.
func scopeRunCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd %s %v failed (dir=%s): %v\n%s", name, args, dir, err, string(out))
	}
}

// initRepo creates a minimal git repo at dir with an initial commit and
// returns the canonical resolved path.
func initRepo(t *testing.T, dir string) string {
	t.Helper()
	scopeRunCmd(t, dir, "git", "init")
	scopeRunCmd(t, dir, "git", "config", "user.email", "test@test.com")
	scopeRunCmd(t, dir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	scopeRunCmd(t, dir, "git", "add", ".")
	scopeRunCmd(t, dir, "git", "commit", "-m", "initial")
	return dir
}

func initGateCheckout(t *testing.T) (string, string) {
	t.Helper()
	nmHome := filepath.Join(t.TempDir(), ".no-mistakes")
	commonDir := filepath.Join(nmHome, "repos", "gate.git")
	if err := os.MkdirAll(filepath.Dir(commonDir), 0755); err != nil {
		t.Fatal(err)
	}
	scopeRunCmd(t, filepath.Dir(commonDir), "git", "init", "--bare", commonDir)
	checkout := t.TempDir()
	if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: "+commonDir+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NM_HOME", nmHome)
	return checkout, commonDir
}

// --- GateRefuseFromCWD tests ---

func TestGateRefuseFromCWD(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T) string // returns the cwd; may set env
		want  string                    // refusal text; empty means allowed
	}{
		{
			name: "gate marker in the environment refuses",
			setup: func(t *testing.T) string {
				t.Setenv("NO_MISTAKES_GATE", "1")
				return initRepo(t, t.TempDir())
			},
			want: "gate agent refused for",
		},
		{
			name: "checkout of a gate repository refuses",
			setup: func(t *testing.T) string {
				checkout, _ := initGateCheckout(t)
				return checkout
			},
			want: "(git-common-dir)",
		},
		{
			name: "git unavailable refuses",
			setup: func(t *testing.T) string {
				repo := initRepo(t, t.TempDir())
				t.Setenv("PATH", t.TempDir())
				return repo
			},
			want: "classifying identity: git rev-parse --git-dir",
		},
		{
			// darwin still reports a removed cwd by path, so there the refusal comes
			// from resolving that path; linux fails os.Getwd and takes the first branch.
			name: "removed working directory refuses",
			setup: func(t *testing.T) string {
				if runtime.GOOS == "windows" {
					t.Skip("a process cannot remove its own working directory on windows")
				}
				gone := t.TempDir()
				t.Chdir(gone)
				if err := os.RemoveAll(gone); err != nil {
					t.Fatal(err)
				}
				return ""
			},
			want: "classifying identity: ",
		},
		{
			name: "ordinary checkout passes",
			setup: func(t *testing.T) string {
				return initRepo(t, t.TempDir())
			},
		},
		{
			name: "directory outside any repository passes",
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NO_MISTAKES_GATE", "")
			os.Unsetenv("NO_MISTAKES_GATE")
			t.Setenv("NM_HOME", filepath.Join(t.TempDir(), ".no-mistakes"))
			if cwd := tc.setup(t); cwd != "" {
				t.Chdir(cwd)
			}

			err := GateRefuseFromCWD()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("GateRefuseFromCWD = %v, want allowed", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("GateRefuseFromCWD = %v, want a refusal containing %q", err, tc.want)
			}
		})
	}
}

func TestResult_GateRefusalError_NilResult(t *testing.T) {
	var r *Result
	err := r.GateRefusalError()
	if err == nil || !strings.Contains(err.Error(), "scope classification unavailable") {
		t.Fatalf("GateRefusalError err = %v, want scope classification unavailable", err)
	}
}

func TestPremiseClassifyIdentityNonRepoExitsUnrelatedBeforeCommonDir(t *testing.T) {
	tmpDir := t.TempDir()
	identity, gitDir, commonDir, err := ClassifyIdentity(tmpDir)
	if err != nil {
		t.Fatalf("ClassifyIdentity on non-repo returned error: %v", err)
	}
	if identity != Unrelated || gitDir != "" || commonDir != "" {
		t.Fatalf("ClassifyIdentity on non-repo = (%v, %q, %q), want (Unrelated, \"\", \"\")", identity, gitDir, commonDir)
	}
}

// ----------------------------------------------------------------------------
// Group J: soldier_envelope.go (1 guard)
// ----------------------------------------------------------------------------

func TestWriteEnvelope_NilEnvelope(t *testing.T) {
	err := WriteEnvelope(t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "launch envelope is nil") {
		t.Fatalf("WriteEnvelope err = %v, want launch envelope is nil", err)
	}
}

// ----------------------------------------------------------------------------
// Group K: taskauthority_reads.go (1 guard)
// ----------------------------------------------------------------------------

func TestCanonicalCurrentStateQ_Read_MissingCanonical(t *testing.T) {
	tmpDir := t.TempDir()
	h, err := home.Init(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	q := NewCanonicalCurrentState()
	_, err = q.Read(h.Root(), "t-nonexistent")
	if err == nil || !strings.Contains(err.Error(), "has no canonical Task Authority record") {
		t.Fatalf("Read err = %v, want has no canonical Task Authority record", err)
	}
	if strings.Contains(err.Error(), strconv.Quote(h.Root())) {
		t.Fatalf("Read error pre-quotes home path: %v", err)
	}
}
