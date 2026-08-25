//go:build integration

package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestGitHubDeliveryProvider_RejectsMismatchedPinnedConstraints(t *testing.T) {
	provider := &githubDeliveryProvider{client: &recordingGitHubClient{}}
	ident := domain.DeliveryIdentity{HeadSHA: sampleSHA, BaseRef: "main"}

	for _, tc := range []struct {
		name    string
		request DeliveryMergeRequest
	}{
		{name: "head mismatch", request: DeliveryMergeRequest{Method: "merge", HeadSHA: "different", BaseRef: "main"}},
		{name: "base mismatch", request: DeliveryMergeRequest{Method: "merge", HeadSHA: sampleSHA, BaseRef: "release"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := provider.ValidateMergeRequest(ident, tc.request); err == nil || !strings.Contains(err.Error(), "do not match") {
				t.Fatalf("ValidateMergeRequest error = %v, want identity-mismatch refusal", err)
			}
		})
	}
}

func TestGitHubDeliveryProvider_RejectsUnenforceableConstraints(t *testing.T) {
	called := false
	client := &recordingGitHubClient{}
	provider := &githubDeliveryProvider{client: client}
	ident := domain.DeliveryIdentity{Owner: "owner", Repo: "repo", Number: 7, HeadSHA: sampleSHA, BaseRef: "main"}
	request := DeliveryMergeRequest{Method: "merge", HeadSHA: sampleSHA, BaseRef: "main"}
	if err := provider.ValidateMergeRequest(ident, request); err == nil {
		t.Fatal("expected unsupported atomic constraints")
	}
	if err := provider.Merge(ident, request); err == nil {
		t.Fatal("expected merge refusal")
	}
	if called {
		t.Fatal("client invoked after unsupported-constraint refusal")
	}
}

type recordingGitHubClient struct{}

func (c *recordingGitHubClient) ObservePR(string, string, int) (DeliveryProviderObservation, error) {
	return DeliveryProviderObservation{}, nil
}
func (c *recordingGitHubClient) ViewPRJSON(string, string, int, string) ([]byte, error) {
	return nil, nil
}
func (c *recordingGitHubClient) CaptureIdentity(string) (*domain.DeliveryIdentity, error) {
	return nil, nil
}

func TestGitHubClientForStateUnknownRefuses(t *testing.T) {
	_, err := GitHubClientForState(backend.State(99))
	if err == nil || !strings.Contains(err.Error(), "unknown state") {
		t.Fatalf("GitHubClientForState error = %v, want unknown-state refusal", err)
	}
}

// --- Capability probe tests ---

func TestProbeGitHubCapability_ReadsGhAxiPresence(t *testing.T) {
	// ProbeGitHubCapability should return Ready when gh-axi is on PATH
	// or Absent when it's not. This test relies on the actual PATH.
	state := ProbeGitHubCapability()
	if state != backend.Ready && state != backend.Absent {
		t.Errorf("expected Ready or Absent, got %v", state)
	}
}

func TestGitHubClientForState_AbsentFailsClosed(t *testing.T) {
	_, err := GitHubClientForState(backend.Absent)
	if err == nil {
		t.Fatal("expected error for Absent state")
	}
	if !strings.Contains(err.Error(), "gh-axi not found") {
		t.Errorf("expected 'gh-axi not found' error, got: %v", err)
	}
}

func TestGitHubClientForState_FailedFailsClosed(t *testing.T) {
	_, err := GitHubClientForState(backend.Failed)
	if err == nil {
		t.Fatal("expected error for Failed state")
	}
	if !strings.Contains(err.Error(), "capability failed") {
		t.Errorf("expected 'capability failed' error, got: %v", err)
	}
}

func TestGitHubClientForState_UnsupportedFailsClosed(t *testing.T) {
	_, err := GitHubClientForState(backend.Unsupported)
	if err == nil {
		t.Fatal("expected error for Unsupported state")
	}
	if !strings.Contains(err.Error(), "capability unsupported") {
		t.Errorf("expected 'capability unsupported' error, got: %v", err)
	}
}

func TestGitHubClientForState_ReadyReturnsClient(t *testing.T) {
	client, err := GitHubClientForState(backend.Ready)
	if err != nil {
		t.Fatalf("unexpected error for Ready: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client for Ready")
	}
	_, ok := client.(*ghAxiClient)
	if !ok {
		t.Errorf("expected *ghAxiClient, got %T", client)
	}
}

// --- ghAxiClient tests ---

func TestGhAxiClient_CaptureIdentity_InvalidURL(t *testing.T) {
	client := &ghAxiClient{}
	_, err := client.CaptureIdentity("not-a-url")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
	if !strings.Contains(err.Error(), "invalid PR URL") {
		t.Errorf("expected 'invalid PR URL' error, got: %v", err)
	}
}

func TestGhAxiClient_CaptureIdentity_NonGithubURL(t *testing.T) {
	client := &ghAxiClient{}
	_, err := client.CaptureIdentity("https://gitlab.com/owner/repo/pull/1")
	if err == nil {
		t.Fatal("expected error for non-github URL")
	}
	if !strings.Contains(err.Error(), "not a github.com URL") {
		t.Errorf("expected 'not a github.com URL' error, got: %v", err)
	}
}

// --- ObservePR / gh-axi api observation tests ---

func TestParseGhAxiKeyValues(t *testing.T) {
	output := `state: closed
headSha: 6b52a27d68fdf6034cc2defc79420882440e87ef
mergedSha: 38a1a401bfa3b272ee4ee99e7cef7920461d9e10
merged: true
`
	values := parseGhAxiKeyValues(output)
	if values["state"] != "closed" {
		t.Errorf("state = %q, want closed", values["state"])
	}
	if values["headSha"] != "6b52a27d68fdf6034cc2defc79420882440e87ef" {
		t.Errorf("headSha = %q", values["headSha"])
	}
	if values["mergedSha"] != "38a1a401bfa3b272ee4ee99e7cef7920461d9e10" {
		t.Errorf("mergedSha = %q", values["mergedSha"])
	}
	if values["merged"] != "true" {
		t.Errorf("merged = %q, want true", values["merged"])
	}
}

func TestClassifyGitHubRESTObservation(t *testing.T) {
	open := []byte("state: open\nheadSha: abc\nbaseRef: main\nmerged: false\n")
	obs, err := classifyGitHubRESTObservation(open)
	if err != nil || obs.State != "OPEN" || obs.Mergeability != DeliveryMergeabilityUnknown {
		t.Fatalf("open observation = %+v, %v", obs, err)
	}
	merged, err := classifyGitHubRESTObservation([]byte("state: closed\nheadSha: abc\nbaseRef: main\nmerged: true\nmergedSha: def\n"))
	if err != nil || merged.State != "MERGED" || merged.MergedSHA != "def" {
		t.Fatalf("merged observation = %+v, %v", merged, err)
	}
	closed, err := classifyGitHubRESTObservation([]byte("state: closed\nheadSha: abc\nbaseRef: main\nmerged: false\n"))
	if err != nil || closed.State != "CLOSED" {
		t.Fatalf("closed observation = %+v, %v", closed, err)
	}
	for _, tc := range []struct {
		name string
		data string
	}{
		{"missing identity", "state: open\nmerged: false\n"},
		{"invalid state", "state: draft\nheadSha: abc\nbaseRef: main\nmerged: false\n"},
		{"missing merged evidence", "state: open\nheadSha: abc\nbaseRef: main\n"},
		{"invalid merged evidence", "state: open\nheadSha: abc\nbaseRef: main\nmerged: maybe\n"},
		{"missing merged sha", "state: closed\nheadSha: abc\nbaseRef: main\nmerged: true\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := classifyGitHubRESTObservation([]byte(tc.data)); err == nil {
				t.Fatal("expected incomplete observation to fail closed")
			}
		})
	}
}

func TestGhAxiClient_ObservePR_UsesRESTContract(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\nprintf '%s\\n' 'state: open' 'headSha: abc' 'baseRef: main' 'merged: false'\n"
	testutil.WriteFakeExecutable(t, filepath.Join(dir, "gh-axi"), script)
	testutil.PrependPath(t, dir)

	obs, err := (&ghAxiClient{}).ObservePR("owner", "repo", 7)
	if err != nil {
		t.Fatalf("ObservePR: %v", err)
	}
	if obs.State != "OPEN" || obs.HeadSHA != "abc" || obs.BaseRef != "main" {
		t.Fatalf("observation = %+v", obs)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(args)
	for _, want := range []string{"api\n", "/repos/owner/repo/pulls/7\n", "--jq\n", "{state: .state, headSha: .head.sha, baseRef: .base.ref, merged: .merged, mergedSha: .merge_commit_sha}\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("gh-axi args %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "graphql") || strings.Contains(got, "-f\n") || strings.Contains(got, "-F\n") {
		t.Fatalf("unsupported GraphQL invocation used: %q", got)
	}
}

// --- DefaultGitHubClient path routing ---

func TestDefaultGitHubClient_RoutesToGhAxiWhenReady(t *testing.T) {
	// Probe current environment
	state := ProbeGitHubCapability()
	if state != backend.Ready {
		t.Skip("gh-axi not on PATH, skipping Ready-path test")
	}

	client, err := DefaultGitHubClient()
	if err != nil {
		t.Fatalf("DefaultGitHubClient: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

// --- QueryPRMergeStatus routing ---

func TestQueryPRMergeStatus_UsesGhAxiWhenReady(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := ProbeGitHubCapability()
	if state != backend.Ready {
		t.Skip("gh-axi not on PATH, skipping Ready-path test")
	}

	// Query a non-existent PR to verify routing reaches gh-axi/adapter
	ghURL := domain.GHURL{Owner: "minhtri2710", Repo: "munsu", Num: 999999}
	_, err := QueryPRMergeStatus(ghURL)
	if err == nil {
		t.Fatal("expected error for non-existent PR")
	}
	// Error should indicate gh-axi or gh pr view failure, not capability absence
	if strings.Contains(err.Error(), "gh-axi not found") || strings.Contains(err.Error(), "capability absent") {
		t.Errorf("error should be from gh-axi/gh PR view, not capability: %v", err)
	}
}

// --- ghAxiLookPath injection tests ---

func TestProbeGitHubCapability_ReplacedLookPath(t *testing.T) {
	old := ghAxiLookPath
	t.Cleanup(func() { ghAxiLookPath = old })

	// Simulate gh-axi not found
	ghAxiLookPath = func() (string, error) {
		return "", errors.New("not found")
	}
	if state := ProbeGitHubCapability(); state != backend.Absent {
		t.Errorf("expected Absent, got %v", state)
	}

	// Simulate gh-axi found
	ghAxiLookPath = func() (string, error) {
		return "/usr/local/bin/gh-axi", nil
	}
	if state := ProbeGitHubCapability(); state != backend.Ready {
		t.Errorf("expected Ready, got %v", state)
	}
}

func TestDefaultGitHubClient_RejectedState(t *testing.T) {
	old := ghAxiLookPath
	t.Cleanup(func() { ghAxiLookPath = old })

	ghAxiLookPath = func() (string, error) {
		return "", errors.New("not found")
	}
	_, err := DefaultGitHubClient()
	if err == nil {
		t.Fatal("expected error when gh-axi not available")
	}
	if !strings.Contains(err.Error(), "capability absent") {
		t.Errorf("expected 'capability absent' error, got: %v", err)
	}
}

// TestCapabilityChain_NoSilentFallback verifies that each state in the
// capability chain (Absent, Failed, Unsupported) produces an error and
// never silently falls back to Ready.
func TestCapabilityChain_NoSilentFallback(t *testing.T) {
	states := []backend.State{
		backend.Absent,
		backend.Failed,
		backend.Unsupported,
	}
	for _, s := range states {
		s := s
		t.Run(s.String(), func(t *testing.T) {
			t.Parallel()
			// GitHubClientForState must reject all non-Ready states.
			client, err := GitHubClientForState(s)
			if err == nil {
				t.Fatalf("expected error for %s, got client %T", s, client)
			}
			// Error must mention the capability state, not something generic.
			if !strings.Contains(err.Error(), "capability") &&
				!strings.Contains(err.Error(), "gh-axi") {
				t.Errorf("error must mention capability or gh-axi, got: %v", err)
			}
			// Must not return a usable client.
			if _, ok := client.(*ghAxiClient); ok {
				t.Errorf("non-Ready state %s must not yield ghAxiClient", s)
			}
		})
	}
}

// TestProbeGitHubCapability_ReturnsDeterministicState verifies that
// ProbeGitHubCapability returns exactly Ready or Absent (never Failed or
// Unsupported for the lookPath pathway).
func TestProbeGitHubCapability_ReturnsDeterministicState(t *testing.T) {
	state := ProbeGitHubCapability()
	if state != backend.Ready && state != backend.Absent {
		t.Errorf("unexpected state %v, want Ready or Absent", state)
	}
	// Should be deterministic within the same test process.
	state2 := ProbeGitHubCapability()
	if state != state2 {
		t.Error("ProbeGitHubCapability is not deterministic")
	}
}
