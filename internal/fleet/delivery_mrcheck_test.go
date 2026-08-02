package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// routeCheckSampleSHA is a valid 40-hex SHA used by canned identities.
const routeCheckSampleSHA = "abc123def456abc123def456abc123def456abc1"

// routeCheckGlabRunner fakes a Ready glab so GitLab capture runs offline.
type routeCheckGlabRunner struct{}

func (routeCheckGlabRunner) LookPath() (string, error) { return "/usr/local/bin/glab", nil }

func (routeCheckGlabRunner) Run(args ...string) ([]byte, error) {
	if len(args) >= 1 && args[0] == "--version" {
		return []byte("glab version 1.45.0"), nil
	}
	if len(args) >= 3 && args[0] == "mr" && args[1] == "view" && args[2] == "--help" {
		return []byte("view a merge request\n"), nil
	}
	if len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
		return []byte("authenticated to gitlab.com\n"), nil
	}
	if len(args) >= 4 && args[0] == "mr" && args[1] == "view" {
		return []byte(`{"sha":"abc123def456abc123def456abc123def456abc1","source_branch":"feature/test","target_branch":"main","state":"opened","merge_commit_sha":null}`), nil
	}
	return []byte("{}"), nil
}

// routeCheckGitHubStub implements GitHubClient with a canned identity.
type routeCheckGitHubStub struct{}

func (routeCheckGitHubStub) MergePR(owner, repo string, number int, method string) error {
	return nil
}

func (routeCheckGitHubStub) ViewPRState(owner, repo string, number int) (string, error) {
	return "OPEN", nil
}

func (routeCheckGitHubStub) ViewPRJSON(owner, repo string, number int, fields string) ([]byte, error) {
	return nil, nil
}

func (routeCheckGitHubStub) CaptureIdentity(prURL string) (*domain.DeliveryIdentity, error) {
	return &domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      "owner",
		Repo:       "repo",
		Number:     42,
		URL:        prURL,
		BaseRef:    "main",
		HeadRef:    "feature/test",
		HeadSHA:    routeCheckSampleSHA,
		CapturedAt: "2026-01-01T00:00:00Z",
	}, nil
}

func (routeCheckGitHubStub) ViewIssueState(owner, repo string, number int) (string, error) {
	return "OPEN", nil
}

// TestRoutePRCheck_GitLabWritesProviderNeutralCheck is the GitLab routing
// test: pr-check on a GitLab MR URL must dispatch to MRLiveCheck and write a
// provider-neutral .check script (munsu delivery merge-status), never gh.
func TestRoutePRCheck_GitLabWritesProviderNeutralCheck(t *testing.T) {
	savedRunner := defaultGlabRunner
	defaultGlabRunner = routeCheckGlabRunner{}
	defer func() { defaultGlabRunner = savedRunner }()

	homeDir := t.TempDir()
	id := "mr-route-task"
	if err := home.WriteMeta(homeDir, id, map[string]string{"kind": "ship", "project": "myproject"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	mrURL := "https://gitlab.com/owner/project/-/merge_requests/42"
	if err := RoutePRCheck(homeDir, id, mrURL, preparedCheckAuth(t, id)); err != nil {
		t.Fatalf("RoutePRCheck: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home.StateDir(homeDir), id+".check"))
	if err != nil {
		t.Fatalf("reading check script: %v", err)
	}
	script := string(data)

	if !strings.Contains(script, "delivery merge-status") {
		t.Errorf("GitLab check script must use the provider-neutral merge-status seam, got:\n%s", script)
	}
	if strings.Contains(script, "gh pr view") {
		t.Errorf("GitLab check script must not invoke gh pr view, got:\n%s", script)
	}
	if strings.Contains(script, "glab") {
		t.Errorf("GitLab check script must not embed raw glab, got:\n%s", script)
	}
	if !strings.Contains(script, `exit "${RESULT}"`) {
		t.Errorf("GitLab check script must preserve merge-status exit codes, got:\n%s", script)
	}
}

func TestMergeStatus_MissingIdentityIsUnverifiable(t *testing.T) {
	err := MergeStatus(t.TempDir(), "missing-task")
	var statusErr *MergeStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("MergeStatus error = %T %v, want *MergeStatusError", err, err)
	}
	if !statusErr.Unverifiable {
		t.Fatalf("MergeStatus error = %v, want unverifiable classification", err)
	}
}

// TestRoutePRCheck_GitHubKeepsPRCheckScript guards backward compatibility:
// a GitHub PR URL must keep the existing gh-based PRCheck script.
func TestRoutePRCheck_GitHubKeepsPRCheckScript(t *testing.T) {
	savedClient := DefaultGitHubClient
	DefaultGitHubClient = func() (GitHubClient, error) { return routeCheckGitHubStub{}, nil }
	defer func() { DefaultGitHubClient = savedClient }()

	homeDir := t.TempDir()
	id := "pr-route-task"
	if err := home.WriteMeta(homeDir, id, map[string]string{"kind": "ship", "project": "munsu"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	prURL := "https://github.com/owner/repo/pull/42"
	if err := RoutePRCheck(homeDir, id, prURL, preparedCheckAuth(t, id)); err != nil {
		t.Fatalf("RoutePRCheck: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home.StateDir(homeDir), id+".check"))
	if err != nil {
		t.Fatalf("reading check script: %v", err)
	}
	script := string(data)

	if !strings.Contains(script, "gh pr view") {
		t.Errorf("GitHub check script must poll via gh pr view, got:\n%s", script)
	}
	if strings.Contains(script, "delivery merge-status") {
		t.Errorf("GitHub check script must keep the existing gh-based poll, got:\n%s", script)
	}
}

// TestRoutePRCheck_UnsupportedURLFailsClosed verifies that pr-check rejects
// URLs that are neither GitHub PRs nor GitLab MRs before writing anything.
func TestRoutePRCheck_UnsupportedURLFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	id := "unsupported-route-task"

	err := RoutePRCheck(homeDir, id, "https://example.com/owner/repo/thing/1", nil)
	if err == nil {
		t.Fatal("expected error for unsupported URL")
	}
	if !strings.Contains(err.Error(), "unsupported") && !strings.Contains(err.Error(), "unrecognized") {
		t.Errorf("error should mention the unsupported URL, got: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(home.StateDir(homeDir), id+".check")); !os.IsNotExist(statErr) {
		t.Errorf("no check script should be written for unsupported URLs")
	}
}

// preparedCheckAuth builds an in-memory Authority with one ship task seeded
// for a pr-check routing test (the task must exist in the Authority before
// the delivery preparation commits).
func preparedCheckAuth(t *testing.T, taskID string) *taskauthority.Authority {
	t.Helper()
	auth := taskauthority.New(taskauthority.NewMemStore())
	if _, err := auth.Create(taskauthority.CreateRequest{
		OperationID: "op-create-" + taskID,
		Actor:       taskauthority.Actor{ID: "owner", Rank: "general"},
		TaskID:      taskID,
		Owner:       "owner",
		Kind:        "ship",
		Reason:      "create",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return auth
}
