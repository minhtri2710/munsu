//go:build integration

package fleet

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
)

// fakeGlabRunner implements GlabRunner for testing.
type fakeGlabRunner struct {
	lookPathErr error
	runFn       func(args ...string) ([]byte, error)
}

func (f *fakeGlabRunner) LookPath() (string, error) {
	if f.lookPathErr != nil {
		return "", f.lookPathErr
	}
	return "/usr/local/bin/glab", nil
}

func (f *fakeGlabRunner) Run(args ...string) ([]byte, error) {
	if f.runFn == nil {
		return []byte(""), nil
	}
	return f.runFn(args...)
}

func readyRunner() *fakeGlabRunner {
	return &fakeGlabRunner{
		runFn: func(args ...string) ([]byte, error) {
			if len(args) >= 1 && args[0] == "--version" {
				return []byte("glab version 1.45.0"), nil
			}
			if len(args) >= 3 && args[0] == "mr" && args[1] == "view" && args[2] == "--help" {
				return []byte("view a merge request\n"), nil
			}
			if len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
				return []byte("authenticated to gitlab.com\n"), nil
			}
			// mr view with JSON — return valid GitLab JSON
			if len(args) >= 4 && args[0] == "mr" && args[1] == "view" {
				return []byte(fmt.Sprintf(`{
					"sha": "%s",
					"source_branch": "feature/test",
					"target_branch": "main",
					"state": "opened",
					"merge_commit_sha": null
				}`, sampleSHA)), nil
			}
			return []byte("{}"), nil
		},
	}
}

func mergedRunner() *fakeGlabRunner {
	return &fakeGlabRunner{
		runFn: func(args ...string) ([]byte, error) {
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
				return []byte(`{
					"sha": "abc123def456abc123def456abc123def456abc1",
					"source_branch": "feature/test",
					"target_branch": "main",
					"state": "merged",
					"merge_commit_sha": "def456abc123def456abc123def456abc123def4"
				}`), nil
			}
			return []byte("{}"), nil
		},
	}
}

func closedRunner() *fakeGlabRunner {
	return &fakeGlabRunner{
		runFn: func(args ...string) ([]byte, error) {
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
				return []byte(`{
					"sha": "abc123def456abc123def456abc123def456abc1",
					"source_branch": "feature/test",
					"target_branch": "main",
					"state": "closed",
					"merge_commit_sha": null
				}`), nil
			}
			return []byte("{}"), nil
		},
	}
}

func failedVersionRunner() *fakeGlabRunner {
	return &fakeGlabRunner{
		runFn: func(args ...string) ([]byte, error) {
			if len(args) >= 1 && args[0] == "--version" {
				return nil, errors.New("exec format error")
			}
			return []byte("{}"), nil
		},
	}
}

func failedAuthRunner() *fakeGlabRunner {
	return &fakeGlabRunner{
		runFn: func(args ...string) ([]byte, error) {
			if len(args) >= 1 && args[0] == "--version" {
				return []byte("glab version 1.45.0"), nil
			}
			if len(args) >= 3 && args[0] == "mr" && args[1] == "view" && args[2] == "--help" {
				return []byte("view a merge request\n"), nil
			}
			if len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
				return nil, errors.New("not authenticated")
			}
			return []byte("{}"), nil
		},
	}
}

func unsupportedRunner() *fakeGlabRunner {
	return &fakeGlabRunner{
		runFn: func(args ...string) ([]byte, error) {
			if len(args) >= 1 && args[0] == "--version" {
				return []byte("glab version 1.45.0"), nil
			}
			if len(args) >= 3 && args[0] == "mr" && args[1] == "view" && args[2] == "--help" {
				return nil, errors.New("unknown command")
			}
			return []byte("{}"), nil
		},
	}
}

const sampleSHA = "abc123def456abc123def456abc123def456abc1"

// --- Four-state probe tests ---

func TestProbeGlabCapability_Absent(t *testing.T) {
	runner := &fakeGlabRunner{lookPathErr: errors.New("not found")}
	state := probeGlabCapability(runner)
	if state != backend.Absent {
		t.Errorf("expected Absent, got %v", state)
	}
}

func TestProbeGlabCapability_Failed(t *testing.T) {
	runner := failedVersionRunner()
	state := probeGlabCapability(runner)
	if state != backend.Failed {
		t.Errorf("expected Failed, got %v", state)
	}
}

func TestProbeGlabCapability_Unsupported(t *testing.T) {
	runner := unsupportedRunner()
	state := probeGlabCapability(runner)
	if state != backend.Unsupported {
		t.Errorf("expected Unsupported, got %v", state)
	}
}

func TestProbeGlabCapability_Ready(t *testing.T) {
	runner := readyRunner()
	state := probeGlabCapability(runner)
	if state != backend.Ready {
		t.Errorf("expected Ready, got %v", state)
	}
}

func TestProbeGlabCapability_AuthFailure(t *testing.T) {
	runner := failedAuthRunner()
	state := probeGlabCapability(runner)
	if state != backend.Failed {
		t.Errorf("expected Failed, got %v", state)
	}
}

// --- GitLabClientForState tests ---

func TestGitLabClientForState_AbsentFailsClosed(t *testing.T) {
	_, err := GitLabClientForState(backend.Absent)
	if err == nil {
		t.Fatal("expected error for Absent state")
	}
	if !strings.Contains(err.Error(), "glab not found") {
		t.Errorf("expected 'glab not found' error, got: %v", err)
	}
}

func TestGitLabClientForState_FailedFailsClosed(t *testing.T) {
	_, err := GitLabClientForState(backend.Failed)
	if err == nil {
		t.Fatal("expected error for Failed state")
	}
	if !strings.Contains(err.Error(), "capability failed") {
		t.Errorf("expected 'capability failed' error, got: %v", err)
	}
}

func TestGitLabClientForState_UnsupportedFailsClosed(t *testing.T) {
	_, err := GitLabClientForState(backend.Unsupported)
	if err == nil {
		t.Fatal("expected error for Unsupported state")
	}
	if !strings.Contains(err.Error(), "capability unsupported") {
		t.Errorf("expected 'capability unsupported' error, got: %v", err)
	}
}

func TestGitLabClientForState_ReadyReturnsClient(t *testing.T) {
	client, err := GitLabClientForState(backend.Ready)
	if err != nil {
		t.Fatalf("unexpected error for Ready: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client for Ready")
	}
	_, ok := client.(*glabClient)
	if !ok {
		t.Errorf("expected *glabClient, got %T", client)
	}
}

// --- CaptureIdentity via fake runner ---

func TestGlabClient_CaptureIdentity_InvalidURL(t *testing.T) {
	client := &glabClient{runner: readyRunner()}
	_, err := client.CaptureIdentity("not-a-url")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
	if !strings.Contains(err.Error(), "invalid MR URL") {
		t.Errorf("expected 'invalid MR URL' error, got: %v", err)
	}
}

func TestGlabClient_CaptureIdentity_NonMRURL(t *testing.T) {
	client := &glabClient{runner: readyRunner()}
	_, err := client.CaptureIdentity("https://github.com/owner/repo/pull/1")
	if err == nil {
		t.Fatal("expected error for GitHub URL")
	}
	if !strings.Contains(err.Error(), "invalid MR URL") {
		t.Errorf("expected 'invalid MR URL' error, got: %v", err)
	}
}

func TestGlabClient_CaptureIdentity_ParseSuccess(t *testing.T) {
	client := &glabClient{runner: readyRunner()}
	ident, err := client.CaptureIdentity("https://gitlab.com/owner/project/-/merge_requests/42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ident.Provider != "gitlab" {
		t.Errorf("Provider: got %q, want %q", ident.Provider, "gitlab")
	}
	if ident.Owner != "owner" {
		t.Errorf("Owner: got %q, want %q", ident.Owner, "owner")
	}
	if ident.Repo != "project" {
		t.Errorf("Repo: got %q, want %q", ident.Repo, "project")
	}
	if ident.Number != 42 {
		t.Errorf("Number: got %d, want 42", ident.Number)
	}
	if ident.HeadSHA != sampleSHA {
		t.Errorf("HeadSHA: got %q, want %q", ident.HeadSHA, sampleSHA)
	}
	if ident.BaseRef != "main" {
		t.Errorf("BaseRef: got %q, want %q", ident.BaseRef, "main")
	}
	if ident.HeadRef != "feature/test" {
		t.Errorf("HeadRef: got %q, want %q", ident.HeadRef, "feature/test")
	}
}

// --- Exact argument tests ---

func TestViewMRJSON_ExactArgs(t *testing.T) {
	var capturedArgs []string
	runner := &fakeGlabRunner{
		runFn: func(args ...string) ([]byte, error) {
			capturedArgs = append([]string{}, args...)
			return []byte(`{"state":"opened"}`), nil
		},
	}
	client := &glabClient{runner: runner}
	_, err := client.ViewMRJSON("gitlab.com", "owner", "project", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"mr", "view", "owner/project!42", "-F", "json"}
	if len(capturedArgs) != len(expected) {
		t.Fatalf("args: got %v, want %v", capturedArgs, expected)
	}
	for i := range expected {
		if capturedArgs[i] != expected[i] {
			t.Errorf("arg[%d]: got %q, want %q", i, capturedArgs[i], expected[i])
		}
	}
}

func TestViewMRJSON_SelfHostedExactArgs(t *testing.T) {
	var capturedArgs []string
	runner := &fakeGlabRunner{
		runFn: func(args ...string) ([]byte, error) {
			capturedArgs = append([]string{}, args...)
			return []byte(`{"state":"opened"}`), nil
		},
	}
	client := &glabClient{runner: runner}
	_, err := client.ViewMRJSON("gitlab.example.com", "group", "project", 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"mr", "view", "group/project!7", "--hostname", "gitlab.example.com", "-F", "json"}
	if len(capturedArgs) != len(expected) {
		t.Fatalf("args: got %v, want %v", capturedArgs, expected)
	}
	for i := range expected {
		if capturedArgs[i] != expected[i] {
			t.Errorf("arg[%d]: got %q, want %q", i, capturedArgs[i], expected[i])
		}
	}
}

// --- NormalizeGlabState tests ---

func TestNormalizeGlabState_Opened(t *testing.T) {
	if got := normalizeGlabState("opened"); got != "OPEN" {
		t.Errorf("normalizeGlabState(opened) = %q, want OPEN", got)
	}
}

func TestNormalizeGlabState_Merged(t *testing.T) {
	if got := normalizeGlabState("merged"); got != "MERGED" {
		t.Errorf("normalizeGlabState(merged) = %q, want MERGED", got)
	}
}

func TestNormalizeGlabState_Closed(t *testing.T) {
	if got := normalizeGlabState("closed"); got != "CLOSED" {
		t.Errorf("normalizeGlabState(closed) = %q, want CLOSED", got)
	}
}

func TestNormalizeGlabState_CaseInsensitive(t *testing.T) {
	if got := normalizeGlabState("OPENED"); got != "OPEN" {
		t.Errorf("normalizeGlabState(OPENED) = %q, want OPEN", got)
	}
	if got := normalizeGlabState("Merged"); got != "MERGED" {
		t.Errorf("normalizeGlabState(Merged) = %q, want MERGED", got)
	}
}

func TestNormalizeGlabState_Unknown(t *testing.T) {
	if got := normalizeGlabState("locked"); got != "LOCKED" {
		t.Errorf("normalizeGlabState(locked) = %q, want LOCKED", got)
	}
}

func TestNormalizeGlabState_Empty(t *testing.T) {
	if got := normalizeGlabState(""); got != "" {
		t.Errorf("normalizeGlabState('') = %q, want empty", got)
	}
}

// --- Delivery merge status via fake runner ---

func TestQueryDeliveryMergeStatus_GitLab_Ready_Open(t *testing.T) {
	old := defaultGlabRunner
	defaultGlabRunner = readyRunner()
	defer func() { defaultGlabRunner = old }()

	ident := &domain.DeliveryIdentity{
		Provider: "gitlab",
		Owner:    "owner",
		Repo:     "project",
		Number:   42,
		URL:      "https://gitlab.com/owner/project/-/merge_requests/42",
	}
	status, err := QueryDeliveryMergeStatus(ident)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.State != "OPEN" {
		t.Errorf("State: got %q, want OPEN", status.State)
	}
	if status.Merged {
		t.Error("expected merged=false for open MR")
	}
	if status.Closed {
		t.Error("expected closed=false for open MR")
	}
}

func TestQueryDeliveryMergeStatus_GitLab_Ready_Merged(t *testing.T) {
	oldRunner := defaultGlabRunner
	defaultGlabRunner = mergedRunner()
	defer func() { defaultGlabRunner = oldRunner }()

	ident := &domain.DeliveryIdentity{
		Provider: "gitlab",
		Owner:    "owner",
		Repo:     "project",
		Number:   42,
		URL:      "https://gitlab.com/owner/project/-/merge_requests/42",
	}
	status, err := QueryDeliveryMergeStatus(ident)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.State != "MERGED" {
		t.Errorf("State: got %q, want MERGED", status.State)
	}
	if !status.Merged {
		t.Error("expected merged=true for merged MR")
	}
	if status.MergedSHA != "def456abc123def456abc123def456abc123def4" {
		t.Errorf("MergedSHA: got %q", status.MergedSHA)
	}
}

func TestQueryDeliveryMergeStatus_GitLab_Ready_Closed(t *testing.T) {
	oldRunner := defaultGlabRunner
	defaultGlabRunner = closedRunner()
	defer func() { defaultGlabRunner = oldRunner }()

	ident := &domain.DeliveryIdentity{
		Provider: "gitlab",
		Owner:    "owner",
		Repo:     "project",
		Number:   42,
		URL:      "https://gitlab.com/owner/project/-/merge_requests/42",
	}
	status, err := QueryDeliveryMergeStatus(ident)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.State != "CLOSED" {
		t.Errorf("State: got %q, want CLOSED", status.State)
	}
	if !status.Closed {
		t.Error("expected closed=true for closed MR")
	}
	if status.Merged {
		t.Error("expected merged=false for closed MR")
	}
}

// --- Fallback policy tests ---

func TestQueryDeliveryMergeStatus_GitLab_Failed_FallackSpyUncalled(t *testing.T) {
	oldRunner := defaultGlabRunner
	oldFallback := defaultGlabFallback
	defaultGlabRunner = failedVersionRunner()
	fallbackCalled := false
	defaultGlabFallback = func(ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
		fallbackCalled = true
		return nil, fmt.Errorf("fallback should not be called when Failed")
	}
	defer func() {
		defaultGlabRunner = oldRunner
		defaultGlabFallback = oldFallback
	}()

	ident := &domain.DeliveryIdentity{
		Provider: "gitlab",
		URL:      "https://gitlab.com/owner/project/-/merge_requests/42",
	}
	_, err := QueryDeliveryMergeStatus(ident)
	if err == nil {
		t.Fatal("expected error for Failed state")
	}
	if fallbackCalled {
		t.Error("fallback should not be called for Failed state")
	}
}

func TestQueryDeliveryMergeStatus_GitLab_Absent_FallackCalled(t *testing.T) {
	oldRunner := defaultGlabRunner
	oldFallback := defaultGlabFallback
	defaultGlabRunner = &fakeGlabRunner{lookPathErr: errors.New("not found")}
	fallbackCalled := false
	defaultGlabFallback = func(ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
		fallbackCalled = true
		return &domain.PRMergeStatus{State: "OPEN", Merged: false, Closed: false, HeadSHA: "abc123"}, nil
	}
	defer func() {
		defaultGlabRunner = oldRunner
		defaultGlabFallback = oldFallback
	}()

	ident := &domain.DeliveryIdentity{
		Provider: "gitlab",
		URL:      "https://gitlab.com/owner/project/-/merge_requests/42",
	}
	status, err := QueryDeliveryMergeStatus(ident)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fallbackCalled {
		t.Error("fallback should be called for Absent state")
	}
	if status.State != "OPEN" {
		t.Errorf("State: got %q, want OPEN", status.State)
	}
}

func TestQueryDeliveryMergeStatus_GitLab_Absent_NoFallackError(t *testing.T) {
	oldRunner := defaultGlabRunner
	oldFallback := defaultGlabFallback
	defaultGlabRunner = &fakeGlabRunner{lookPathErr: errors.New("not found")}
	defaultGlabFallback = nil // no fallback configured
	defer func() {
		defaultGlabRunner = oldRunner
		defaultGlabFallback = oldFallback
	}()

	ident := &domain.DeliveryIdentity{
		Provider: "gitlab",
		URL:      "https://gitlab.com/owner/project/-/merge_requests/42",
	}
	_, err := QueryDeliveryMergeStatus(ident)
	if err == nil {
		t.Fatal("expected error when no fallback configured")
	}
}

// --- GitHub delegation regression ---

func TestQueryDeliveryMergeStatus_GitHub_Delegates(t *testing.T) {
	// GitHub URL should route through QueryPRMergeStatus.
	// Use a mock to verify delegation.
	saved := QueryPRMergeStatus
	QueryPRMergeStatus = func(ghURL domain.GHURL) (*domain.PRMergeStatus, error) {
		if ghURL.Owner != "minhtri2710" || ghURL.Repo != "munsu" || ghURL.Num != 42 {
			t.Errorf("unexpected ghURL: %+v", ghURL)
		}
		return &domain.PRMergeStatus{State: "OPEN", Merged: false, Closed: false, HeadSHA: "abc123"}, nil
	}
	defer func() { QueryPRMergeStatus = saved }()

	ident := &domain.DeliveryIdentity{
		Provider: "github",
		Owner:    "minhtri2710",
		Repo:     "munsu",
		Number:   42,
		URL:      "https://github.com/minhtri2710/munsu/pull/42",
	}
	status, err := QueryDeliveryMergeStatus(ident)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.State != "OPEN" {
		t.Errorf("State: got %q, want OPEN", status.State)
	}
}

// --- ParseProviderURL tests ---

func TestParseProviderURL_GitHubURL(t *testing.T) {
	provider, owner, repo, num, fullURL, err := ParseProviderURL("https://github.com/minhtri2710/munsu/pull/42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider != "github" {
		t.Errorf("provider: got %q, want %q", provider, "github")
	}
	if owner != "minhtri2710" {
		t.Errorf("owner: got %q, want %q", owner, "minhtri2710")
	}
	if repo != "munsu" {
		t.Errorf("repo: got %q, want %q", repo, "munsu")
	}
	if num != 42 {
		t.Errorf("num: got %d, want 42", num)
	}
	if fullURL != "https://github.com/minhtri2710/munsu/pull/42" {
		t.Errorf("fullURL: got %q", fullURL)
	}
}

func TestParseProviderURL_GitLabURL(t *testing.T) {
	provider, owner, repo, num, fullURL, err := ParseProviderURL("https://gitlab.com/owner/project/-/merge_requests/42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider != "gitlab" {
		t.Errorf("provider: got %q, want %q", provider, "gitlab")
	}
	if owner != "owner" {
		t.Errorf("owner: got %q, want %q", owner, "owner")
	}
	if repo != "project" {
		t.Errorf("repo: got %q, want %q", repo, "project")
	}
	if num != 42 {
		t.Errorf("num: got %d, want 42", num)
	}
	if fullURL != "https://gitlab.com/owner/project/-/merge_requests/42" {
		t.Errorf("fullURL: got %q", fullURL)
	}
}

func TestParseProviderURL_NestedGitLabURL(t *testing.T) {
	provider, owner, repo, num, _, err := ParseProviderURL("https://gitlab.com/group/subgroup/project/-/merge_requests/7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider != "gitlab" {
		t.Errorf("provider: got %q, want %q", provider, "gitlab")
	}
	if owner != "group/subgroup" {
		t.Errorf("owner: got %q, want %q", owner, "group/subgroup")
	}
	if repo != "project" {
		t.Errorf("repo: got %q, want %q", repo, "project")
	}
	if num != 7 {
		t.Errorf("num: got %d, want 7", num)
	}
}

func TestParseProviderURL_UnrecognizedURL(t *testing.T) {
	_, _, _, _, _, err := ParseProviderURL("https://example.com/foo/bar/42")
	if err == nil {
		t.Fatal("expected error for unrecognized URL")
	}
}

func TestParseProviderURL_SelfHostedGitLab(t *testing.T) {
	provider, owner, repo, num, _, err := ParseProviderURL("https://gitlab.example.com/team/project/-/merge_requests/7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider != "gitlab" {
		t.Errorf("provider: got %q, want %q", provider, "gitlab")
	}
	if owner != "team" {
		t.Errorf("owner: got %q, want %q", owner, "team")
	}
	if repo != "project" {
		t.Errorf("repo: got %q, want %q", repo, "project")
	}
	if num != 7 {
		t.Errorf("num: got %d, want 7", num)
	}
}

// --- GitLab identity round-trip through meta ---

func TestGitLabIdentity_RoundTrip(t *testing.T) {
	original := &domain.DeliveryIdentity{
		Provider:   "gitlab",
		Owner:      "owner",
		Repo:       "project",
		Number:     42,
		URL:        "https://gitlab.com/owner/project/-/merge_requests/42",
		BaseRef:    "main",
		HeadRef:    "feature/test",
		HeadSHA:    "abc123def456abc123def456abc123def456abc1",
		CapturedAt: "2026-07-18T12:00:00Z",
	}

	meta := original.ToMeta()
	restored, err := domain.IdentityFromMeta(meta)
	if err != nil {
		t.Fatalf("IdentityFromMeta: %v", err)
	}
	if restored == nil {
		t.Fatal("IdentityFromMeta returned nil")
	}

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"Provider", restored.Provider, original.Provider},
		{"Owner", restored.Owner, original.Owner},
		{"Repo", restored.Repo, original.Repo},
		{"URL", restored.URL, original.URL},
		{"BaseRef", restored.BaseRef, original.BaseRef},
		{"HeadRef", restored.HeadRef, original.HeadRef},
		{"HeadSHA", restored.HeadSHA, original.HeadSHA},
		{"CapturedAt", restored.CapturedAt, original.CapturedAt},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
	if restored.Number != original.Number {
		t.Errorf("Number: got %d, want %d", restored.Number, original.Number)
	}
}

func TestGitLabIdentity_LegacyPRKey(t *testing.T) {
	meta := map[string]string{
		"pr": "https://gitlab.com/owner/project/-/merge_requests/42",
	}
	id, err := domain.IdentityFromMeta(meta)
	if err != nil {
		t.Fatalf("IdentityFromMeta with legacy pr key: %v", err)
	}
	if id == nil {
		t.Fatal("expected non-nil identity from legacy key")
	}
	if id.URL != "https://gitlab.com/owner/project/-/merge_requests/42" {
		t.Errorf("URL: got %q, want %q", id.URL, "https://gitlab.com/owner/project/-/merge_requests/42")
	}
	if id.Number != 42 {
		t.Errorf("Number: got %d, want 42", id.Number)
	}
}

// --- IdentityFromMeta provider/URL consistency ---

func TestIdentityFromMeta_RejectsProviderURLMismatch(t *testing.T) {
	meta := map[string]string{
		"pr_provider":  "github",
		"pr_url":       "https://gitlab.com/owner/project/-/merge_requests/42",
		"pr_number":    "42",
		"pr_owner":     "owner",
		"pr_repo":      "project",
		"pr_base":      "main",
		"pr_head_ref":  "feature/test",
		"pr_head":      sampleSHA,
		"pr_timestamp": "2026-07-18T12:00:00Z",
	}
	_, err := domain.IdentityFromMeta(meta)
	if err == nil {
		t.Fatal("expected error for provider/URL mismatch")
	}
	if !strings.Contains(err.Error(), "provider mismatch") {
		t.Errorf("expected 'provider mismatch' error, got: %v", err)
	}
}

// --- GitLab capability chain tests ---

func TestGitLabCapabilityChain_NoSilentFallback(t *testing.T) {
	states := []backend.State{
		backend.Absent,
		backend.Failed,
		backend.Unsupported,
	}
	for _, s := range states {
		s := s
		t.Run(s.String(), func(t *testing.T) {
			t.Parallel()
			client, err := GitLabClientForState(s)
			if err == nil {
				t.Fatalf("expected error for %s, got client %T", s, client)
			}
			if !strings.Contains(err.Error(), "capability") &&
				!strings.Contains(err.Error(), "glab") {
				t.Errorf("error must mention capability or glab, got: %v", err)
			}
			if _, ok := client.(*glabClient); ok {
				t.Errorf("non-Ready state %s must not yield glabClient", s)
			}
		})
	}
}

func TestProbeGitLabCapability_Deterministic(t *testing.T) {
	state := ProbeGitLabCapability()
	state2 := ProbeGitLabCapability()
	if state != state2 {
		t.Error("ProbeGitLabCapability is not deterministic")
	}
}

// --- Preserved GitHub behavior regression ---

func TestGitHubIdentityStillWorks(t *testing.T) {
	original := validIdentity()
	meta := original.ToMeta()
	restored, err := domain.IdentityFromMeta(meta)
	if err != nil {
		t.Fatalf("IdentityFromMeta: %v", err)
	}
	if restored == nil {
		t.Fatal("IdentityFromMeta returned nil")
	}
	if restored.Provider != "github" {
		t.Errorf("Provider: got %q, want %q", restored.Provider, "github")
	}
	if restored.Number != 42 {
		t.Errorf("Number: got %d, want 42", restored.Number)
	}
}

func TestExistingGitHubTestsStillPass(t *testing.T) {
	meta := validIdentity().ToMeta()
	restored, err := domain.IdentityFromMeta(meta)
	if err != nil {
		t.Fatalf("IdentityFromMeta: %v", err)
	}
	if restored.URL != validIdentity().URL {
		t.Errorf("URL: got %q, want %q", restored.URL, validIdentity().URL)
	}
	if restored.Owner != "minhtri2710" {
		t.Errorf("Owner: got %q, want %q", restored.Owner, "minhtri2710")
	}
	if restored.Repo != "munsu" {
		t.Errorf("Repo: got %q, want %q", restored.Repo, "munsu")
	}
}

// --- CaptureIdentity provider routing (dispatcher removed with the legacy
// delivery path; the typed clients own identity capture) ---

// --- MergeMR typed mutation tests ---

func TestMergeMR_SquashExactArgs(t *testing.T) {
	var got []string
	client := &glabClient{runner: &fakeGlabRunner{
		runFn: func(args ...string) ([]byte, error) {
			got = args
			return []byte("merged"), nil
		},
	}}
	if err := client.MergeMR("gitlab.com", "owner", "project", 7, "squash"); err != nil {
		t.Fatalf("MergeMR: %v", err)
	}
	want := []string{"mr", "merge", "owner/project!7", "--squash"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", got, want)
	}
}

func TestMergeMR_SelfHostedExactArgs(t *testing.T) {
	var got []string
	client := &glabClient{runner: &fakeGlabRunner{
		runFn: func(args ...string) ([]byte, error) {
			got = args
			return []byte("merged"), nil
		},
	}}
	if err := client.MergeMR("git.example.com", "owner", "project", 7, "rebase"); err != nil {
		t.Fatalf("MergeMR: %v", err)
	}
	want := []string{"mr", "merge", "owner/project!7", "--hostname", "git.example.com", "--rebase"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", got, want)
	}
}

func TestMergeMR_MergeMethodDefaultsToMergeCommit(t *testing.T) {
	var got []string
	client := &glabClient{runner: &fakeGlabRunner{
		runFn: func(args ...string) ([]byte, error) {
			got = args
			return []byte("merged"), nil
		},
	}}
	if err := client.MergeMR("gitlab.com", "owner", "project", 7, "merge"); err != nil {
		t.Fatalf("MergeMR: %v", err)
	}
	want := []string{"mr", "merge", "owner/project!7"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", got, want)
	}
}

func TestMergeMR_UnsupportedMethodFailsClosed(t *testing.T) {
	client := &glabClient{runner: &fakeGlabRunner{}}
	if err := client.MergeMR("gitlab.com", "owner", "project", 7, "explode"); err == nil {
		t.Fatal("expected error for unsupported merge method")
	}
}

// TestGitlabDeliveryProvider_UsesTypedCapabilityOnly proves the GitLab
// delivery adapter routes the irreversible mutation and observation through
// the typed GitLabClient methods only.
func TestGitlabDeliveryProvider_UsesTypedCapabilityOnly(t *testing.T) {
	client := &glabClient{runner: &fakeGlabRunner{
		runFn: func(args ...string) ([]byte, error) {
			if len(args) >= 2 && args[0] == "mr" && args[1] == "view" {
				return []byte(`{"sha": "abc123def456abc123def456abc123def456abc1", "source_branch": "feature", "target_branch": "main", "state": "opened", "merge_commit_sha": null}`), nil
			}
			return []byte("merged"), nil
		},
	}}
	provider := &gitlabDeliveryProvider{client: client}
	ident := domain.DeliveryIdentity{
		Provider: "gitlab", Owner: "glowner", Repo: "glrepo", Number: 7,
		URL:     "https://gitlab.com/glowner/glrepo/-/merge_requests/7",
		BaseRef: "main", HeadRef: "feature", HeadSHA: "abc123def456abc123def456abc123def456abc1",
	}
	obs, err := provider.Observe(ident)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.State != "OPEN" || obs.HeadSHA != "abc123def456abc123def456abc123def456abc1" {
		t.Fatalf("observation = %+v", obs)
	}
	// The MR target branch feeds the pre-mutation base ref fence: an
	// observation without it cannot reject a base changed since capture.
	if obs.BaseRef != "main" {
		t.Fatalf("observation base ref = %q, want the MR target_branch %q", obs.BaseRef, "main")
	}
	if err := provider.Merge(ident, "squash"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
}
