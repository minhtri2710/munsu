package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/testutil"
)

type mockGitHubClient struct {
	data []byte
	err  error
}

func (m *mockGitHubClient) ObservePR(owner, repo string, number int) (DeliveryProviderObservation, error) {
	return DeliveryProviderObservation{}, m.err
}

func (m *mockGitHubClient) ViewPRJSON(owner, repo string, number int, fields string) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.data, nil
}

func (m *mockGitHubClient) CaptureIdentity(prURL string) (*domain.DeliveryIdentity, error) {
	return nil, m.err
}

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

func TestFetchGitHubProviderSnapshot_EmptyHeadOrBaseRef(t *testing.T) {
	old := DefaultGitHubClient
	t.Cleanup(func() { DefaultGitHubClient = old })
	DefaultGitHubClient = func() (GitHubClient, error) {
		return &mockGitHubClient{data: []byte(`{"state":"OPEN","headRefOid":"abc1234567890123456789012345678901234567","headRefName":"","baseRefName":"main"}`)}, nil
	}
	_, err := fetchGitHubProviderSnapshot("https://github.com/owner/repo/pull/1")
	if err == nil || !strings.Contains(err.Error(), "gh pr view returned empty headRefName or baseRefName") {
		t.Fatalf("fetchGitHubProviderSnapshot err = %v, want empty headRefName or baseRefName", err)
	}
}

func TestFetchGitHubProviderSnapshot_EmptyHeadRefOid(t *testing.T) {
	old := DefaultGitHubClient
	t.Cleanup(func() { DefaultGitHubClient = old })
	DefaultGitHubClient = func() (GitHubClient, error) {
		return &mockGitHubClient{data: []byte(`{"state":"OPEN","headRefOid":"","headRefName":"feat","baseRefName":"main"}`)}, nil
	}
	_, err := fetchGitHubProviderSnapshot("https://github.com/owner/repo/pull/1")
	if err == nil || !strings.Contains(err.Error(), "gh pr view returned empty headRefOid") {
		t.Fatalf("fetchGitHubProviderSnapshot err = %v, want empty headRefOid", err)
	}
}

func TestFetchGitHubProviderSnapshot_EmptyState(t *testing.T) {
	old := DefaultGitHubClient
	t.Cleanup(func() { DefaultGitHubClient = old })
	DefaultGitHubClient = func() (GitHubClient, error) {
		return &mockGitHubClient{data: []byte(`{"state":"","headRefOid":"abc1234567890123456789012345678901234567","headRefName":"feat","baseRefName":"main"}`)}, nil
	}
	_, err := fetchGitHubProviderSnapshot("https://github.com/owner/repo/pull/1")
	if err == nil || !strings.Contains(err.Error(), "gh pr view returned empty state") {
		t.Fatalf("fetchGitHubProviderSnapshot err = %v, want empty state", err)
	}
}

func TestFetchGitLabProviderSnapshot_EmptyRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"empty state", `{"state":"","sha":"abc1234567890123456789012345678901234567","source_branch":"feat","target_branch":"main"}`},
		{"empty sha", `{"state":"opened","sha":"","source_branch":"feat","target_branch":"main"}`},
		{"empty source_branch", `{"state":"opened","sha":"abc1234567890123456789012345678901234567","source_branch":"","target_branch":"main"}`},
		{"empty target_branch", `{"state":"opened","sha":"abc1234567890123456789012345678901234567","source_branch":"feat","target_branch":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldRunner := defaultGlabRunner
			t.Cleanup(func() { defaultGlabRunner = oldRunner })
			defaultGlabRunner = &mockGlabRunner{data: []byte(tc.json)}
			_, err := fetchGitLabProviderSnapshot("https://gitlab.com/owner/project/-/merge_requests/1")
			if err == nil || !strings.Contains(err.Error(), "glab mr view returned empty state, sha, source_branch, or target_branch") {
				t.Fatalf("fetchGitLabProviderSnapshot err = %v, want empty field error", err)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// Group B: delivery_github.go (2 guards)
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

func TestGitHubDeliveryProvider_Observe_NilClient(t *testing.T) {
	p := &githubDeliveryProvider{client: nil}
	_, err := p.Observe(domain.DeliveryIdentity{})
	if err == nil || !strings.Contains(err.Error(), "GitHub delivery capability is not composed") {
		t.Fatalf("Observe err = %v, want not composed", err)
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
// Group D: delivery_mergeandretire.go (1 guard)
// ----------------------------------------------------------------------------

func TestMergeAndRetireDeliveryRequest_NilIdentity(t *testing.T) {
	tmpDir := t.TempDir()
	h, err := home.Init(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(h.Root(), "t1", map[string]string{
		"description": "test task without delivery identity",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = mergeAndRetireDeliveryRequest(h.Root(), "t1", "", nil)
	if err == nil || !strings.Contains(err.Error(), "no delivery identity for task t1; capture one first") {
		t.Fatalf("mergeAndRetireDeliveryRequest err = %v, want no delivery identity", err)
	}
}

// ----------------------------------------------------------------------------
// Group E: delivery_mrcheck.go (1 guard)
// ----------------------------------------------------------------------------

func TestMergeStatus_ClosedNotMerged(t *testing.T) {
	tmpDir := t.TempDir()
	h, err := home.Init(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	ident := &domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      "owner",
		Repo:       "repo",
		Number:     1,
		URL:        "https://github.com/owner/repo/pull/1",
		BaseRef:    "main",
		HeadRef:    "feat",
		HeadSHA:    "abc1234567890123456789012345678901234567",
		CapturedAt: "2026-07-18T12:00:00Z",
	}
	if err := home.WriteMeta(h.Root(), "t1", ident.ToMeta()); err != nil {
		t.Fatal(err)
	}
	old := DefaultGitHubClient
	t.Cleanup(func() { DefaultGitHubClient = old })
	DefaultGitHubClient = func() (GitHubClient, error) {
		return &mockGitHubClient{data: []byte(`{"state":"CLOSED","headRefOid":"abc1234567890123456789012345678901234567"}`)}, nil
	}
	err = MergeStatus(h.Root(), "t1")
	if err == nil || !strings.Contains(err.Error(), "is closed but not merged") {
		t.Fatalf("MergeStatus err = %v, want closed but not merged", err)
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

func TestReviewDiff_NoProjectInMeta(t *testing.T) {
	tmpDir := t.TempDir()
	h, err := home.Init(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(h.Root(), "t1", map[string]string{
		"worktree": tmpDir,
	}); err != nil {
		t.Fatal(err)
	}
	err = ReviewDiff(h.Root(), "t1")
	if err == nil || !strings.Contains(err.Error(), "task t1 has no project in meta") {
		t.Fatalf("ReviewDiff err = %v, want no project in meta", err)
	}
}

func TestReviewDiff_NoWorktreeInMeta(t *testing.T) {
	tmpDir := t.TempDir()
	h, err := home.Init(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(h.Root(), "t1", map[string]string{
		"project": "proj-1",
	}); err != nil {
		t.Fatal(err)
	}
	err = ReviewDiff(h.Root(), "t1")
	if err == nil || !strings.Contains(err.Error(), "task t1 has no worktree path in meta") {
		t.Fatalf("ReviewDiff err = %v, want no worktree path in meta", err)
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

func TestGateRefuseFromCWD_EnvPresent(t *testing.T) {
	t.Setenv("NO_MISTAKES_GATE", "1")
	t.Setenv("PATH", t.TempDir())
	err := GateRefuseFromCWD()
	if err == nil || !strings.Contains(err.Error(), "classifying identity:") {
		t.Fatalf("GateRefuseFromCWD err = %v, want classifying identity error", err)
	}
}

func TestGateRefuseFromCWD_GitCommonDirGatePresent(t *testing.T) {
	t.Setenv("NO_MISTAKES_GATE", "")
	os.Unsetenv("NO_MISTAKES_GATE")
	nmHome := filepath.Join(t.TempDir(), ".no-mistakes")
	commonDir := filepath.Join(nmHome, "repos", "gate.git")
	if err := os.MkdirAll(filepath.Dir(commonDir), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "--bare", commonDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v, %s", err, string(out))
	}
	checkout := t.TempDir()
	if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: "+commonDir+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NM_HOME", nmHome)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(checkout); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	err = GateRefuseFromCWD()
	if err == nil || !strings.Contains(err.Error(), "gate agent refused for") || !strings.Contains(err.Error(), "git-common-dir") {
		t.Fatalf("GateRefuseFromCWD err = %v, want gate agent refused (git-common-dir)", err)
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
