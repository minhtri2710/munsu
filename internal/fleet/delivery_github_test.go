//go:build integration

package fleet

import (
	"errors"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
)

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

// observePRFromOutput exercises the REST classification directly through the
// production classifier.
func observePRFromOutput(output string) (DeliveryProviderObservation, error) {
	return classifyGitHubObservation(parseGhAxiKeyValues(output))
}

func TestObservePR_ClassifiesMergedByEvidence(t *testing.T) {
	// REST reports merged PRs as state=closed with merged=true and a
	// non-empty merge_commit_sha.
	obs, err := observePRFromOutput("state: closed\nheadSha: abc\nmergedSha: def\nmerged: true\n")
	if err != nil {
		t.Fatal(err)
	}
	if obs.State != "MERGED" {
		t.Errorf("state = %q, want MERGED", obs.State)
	}
	if obs.HeadSHA != "abc" || obs.MergedSHA != "def" {
		t.Errorf("obs = %+v", obs)
	}
}

func TestObservePR_ClassifiesOpen(t *testing.T) {
	obs, err := observePRFromOutput("state: open\nheadSha: abc\nmergedSha: \nmerged: false\n")
	if err != nil {
		t.Fatal(err)
	}
	if obs.State != "OPEN" {
		t.Errorf("state = %q, want OPEN", obs.State)
	}
}

func TestObservePR_ClassifiesClosedUnmerged(t *testing.T) {
	obs, err := observePRFromOutput("state: closed\nheadSha: abc\nmergedSha: \nmerged: false\n")
	if err != nil {
		t.Fatal(err)
	}
	if obs.State != "CLOSED" {
		t.Errorf("state = %q, want CLOSED", obs.State)
	}
}

func TestObservePR_EmptyOutputFailsClosed(t *testing.T) {
	if _, err := observePRFromOutput(""); err == nil {
		t.Fatal("expected error for empty output")
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
