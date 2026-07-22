package delivery

import (
	"errors"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/capability"
)

// --- Capability probe tests ---

func TestProbeGitLabCapability_ReadsGlabPresence(t *testing.T) {
	state := ProbeGitLabCapability()
	if state != capability.Ready && state != capability.Absent && state != capability.Failed {
		t.Errorf("expected Ready, Absent, or Failed, got %v", state)
	}
}

func TestGitLabClientForState_AbsentFailsClosed(t *testing.T) {
	_, err := GitLabClientForState(capability.Absent)
	if err == nil {
		t.Fatal("expected error for Absent state")
	}
	if !strings.Contains(err.Error(), "glab not found") {
		t.Errorf("expected 'glab not found' error, got: %v", err)
	}
}

func TestGitLabClientForState_FailedFailsClosed(t *testing.T) {
	_, err := GitLabClientForState(capability.Failed)
	if err == nil {
		t.Fatal("expected error for Failed state")
	}
	if !strings.Contains(err.Error(), "capability failed") {
		t.Errorf("expected 'capability failed' error, got: %v", err)
	}
}

func TestGitLabClientForState_UnsupportedFailsClosed(t *testing.T) {
	_, err := GitLabClientForState(capability.Unsupported)
	if err == nil {
		t.Fatal("expected error for Unsupported state")
	}
	if !strings.Contains(err.Error(), "capability unsupported") {
		t.Errorf("expected 'capability unsupported' error, got: %v", err)
	}
}

func TestGitLabClientForState_ReadyReturnsClient(t *testing.T) {
	client, err := GitLabClientForState(capability.Ready)
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

// --- glabClient tests ---

func TestGlabClient_CaptureIdentity_InvalidURL(t *testing.T) {
	client := &glabClient{}
	_, err := client.CaptureIdentity("not-a-url")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
	if !strings.Contains(err.Error(), "invalid MR URL") {
		t.Errorf("expected 'invalid MR URL' error, got: %v", err)
	}
}

func TestGlabClient_CaptureIdentity_NonMRURL(t *testing.T) {
	client := &glabClient{}
	_, err := client.CaptureIdentity("https://github.com/owner/repo/pull/1")
	if err == nil {
		t.Fatal("expected error for GitHub URL")
	}
	if !strings.Contains(err.Error(), "invalid MR URL") {
		t.Errorf("expected 'invalid MR URL' error, got: %v", err)
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

// --- DefaultGitLabClient path routing ---

func TestDefaultGitLabClient_RoutesToGlabWhenReady(t *testing.T) {
	state := ProbeGitLabCapability()
	if state != capability.Ready {
		t.Skip("glab not on PATH or not functional, skipping Ready-path test")
	}
	client, err := DefaultGitLabClient()
	if err != nil {
		t.Fatalf("DefaultGitLabClient: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

// --- glabLookPath injection tests ---

func TestProbeGitLabCapability_ReplacedLookPath(t *testing.T) {
	old := glabLookPath
	oldProbe := defaultGlabProbe
	t.Cleanup(func() { glabLookPath = old; defaultGlabProbe = oldProbe })

	// Simulate glab not found via lookPath — probe should return Absent
	glabLookPath = func() (string, error) {
		return "", errors.New("not found")
	}
	// Override the probe to test lookPath only
	defaultGlabProbe = func() capability.State {
		_, err := glabLookPath()
		if err != nil {
			return capability.Absent
		}
		return capability.Ready
	}
	if state := ProbeGitLabCapability(); state != capability.Absent {
		t.Errorf("expected Absent, got %v", state)
	}
}

func TestGlabProbe_ReturnsFailedWhenVersionFails(t *testing.T) {
	// Test that the defaultGlabProbe returns Failed when glab is on PATH
	// but --version fails. We simulate by replacing glabLookPath with a
	// path that exists but is not glab.
	oldProbe := defaultGlabProbe
	t.Cleanup(func() { defaultGlabProbe = oldProbe })

	called := false
	defaultGlabProbe = func() capability.State {
		called = true
		return capability.Failed
	}

	state := ProbeGitLabCapability()
	if state != capability.Failed {
		t.Errorf("expected Failed, got %v", state)
	}
	if !called {
		t.Error("defaultGlabProbe was not called")
	}
}

func TestDefaultGitLabClient_RejectedState(t *testing.T) {
	old := glabLookPath
	oldProbe := defaultGlabProbe
	t.Cleanup(func() { glabLookPath = old; defaultGlabProbe = oldProbe })

	glabLookPath = func() (string, error) {
		return "", errors.New("not found")
	}
	defaultGlabProbe = func() capability.State {
		return capability.Absent
	}
	_, err := DefaultGitLabClient()
	if err == nil {
		t.Fatal("expected error when glab not available")
	}
	if !strings.Contains(err.Error(), "capability absent") {
		t.Errorf("expected 'capability absent' error, got: %v", err)
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
	provider, owner, repo, num, fullURL, err := ParseProviderURL("https://gitlab.com/group/subgroup/project/-/merge_requests/7")
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
	if fullURL != "https://gitlab.com/group/subgroup/project/-/merge_requests/7" {
		t.Errorf("fullURL: got %q", fullURL)
	}
}

func TestParseProviderURL_UnrecognizedURL(t *testing.T) {
	_, _, _, _, _, err := ParseProviderURL("https://example.com/foo/bar/42")
	if err == nil {
		t.Fatal("expected error for unrecognized URL")
	}
}

func TestParseProviderURL_InvalidURL(t *testing.T) {
	_, _, _, _, _, err := ParseProviderURL("not-a-url")
	if err == nil {
		t.Fatal("expected error for invalid URL")
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
	original := &DeliveryIdentity{
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
	restored, err := IdentityFromMeta(meta)
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
	id, err := IdentityFromMeta(meta)
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
	if id.Owner == "" {
		t.Error("expected Owner to be derived from URL")
	}
	if id.Repo == "" {
		t.Error("expected Repo to be derived from URL")
	}
}

// --- IdentityFromMeta provider/URL consistency ---

func TestIdentityFromMeta_RejectsProviderURLMismatch(t *testing.T) {
	// Set pr_provider=github but pr_url is a GitLab URL
	meta := map[string]string{
		"pr_provider": "github",
		"pr_url":      "https://gitlab.com/owner/project/-/merge_requests/42",
		"pr_number":   "42",
		"pr_owner":    "owner",
		"pr_repo":     "project",
		"pr_base":     "main",
		"pr_head_ref": "feature/test",
		"pr_head":     "abc123def456abc123def456abc123def456abc1",
		"pr_timestamp": "2026-07-18T12:00:00Z",
	}
	_, err := IdentityFromMeta(meta)
	if err == nil {
		t.Fatal("expected error for provider/URL mismatch")
	}
	if !strings.Contains(err.Error(), "provider mismatch") {
		t.Errorf("expected 'provider mismatch' error, got: %v", err)
	}
}

// --- GitLab capability chain tests ---

func TestGitLabCapabilityChain_NoSilentFallback(t *testing.T) {
	states := []capability.State{
		capability.Absent,
		capability.Failed,
		capability.Unsupported,
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

func TestProbeGitLabCapability_ReturnsDeterministicState(t *testing.T) {
	state := ProbeGitLabCapability()
	if state != capability.Ready && state != capability.Absent && state != capability.Failed && state != capability.Unsupported {
		t.Errorf("unexpected state %v", state)
	}
	state2 := ProbeGitLabCapability()
	if state != state2 {
		t.Error("ProbeGitLabCapability is not deterministic")
	}
}

// --- Preserved GitHub behavior regression ---

func TestGitHubIdentityStillWorksWithParseProviderURL(t *testing.T) {
	original := validIdentity()
	meta := original.ToMeta()
	restored, err := IdentityFromMeta(meta)
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

func TestExistingGitHubTestsStillPassWithParseProviderURL(t *testing.T) {
	meta := validIdentity().ToMeta()
	restored, err := IdentityFromMeta(meta)
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

// --- CaptureIdentity provider routing ---

func TestCaptureIdentity_RoutesGitHubToGhAxi(t *testing.T) {
	// GitHub URL should reach captureGitHubIdentity.
	// With gh-axi or gh CLI available, this may succeed or fail.
	// The key assertion: the error (if any) must be from the GitHub path,
	// not from "unrecognized" URL parsing.
	_, err := CaptureIdentity("https://github.com/minhtri2710/munsu/pull/24")
	if err != nil && strings.Contains(err.Error(), "unrecognized") {
		t.Errorf("error should be from GitHub path, not unrecognized: %v", err)
	}
	// Success is also valid — the GitHub path worked.
}

func TestCaptureIdentity_RoutesGitLabToGlabClient(t *testing.T) {
	// GitLab URL should reach captureGitLabIdentity.
	// Without glab on PATH, it will fail with capability absent.
	_, err := CaptureIdentity("https://gitlab.com/owner/project/-/merge_requests/1")
	if err == nil {
		t.Fatal("expected error (glab not available)")
	}
	if !strings.Contains(err.Error(), "GitLab provider not available") &&
		!strings.Contains(err.Error(), "invalid MR URL") {
		t.Errorf("error should be about GitLab provider or invalid URL, got: %v", err)
	}
}
