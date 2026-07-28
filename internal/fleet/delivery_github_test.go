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

// --- ViewPRState tests ---

func TestParseGhAxiState_Merged(t *testing.T) {
	output := `pull_request:
  number: 24
  title: "test title"
  state: merged
  author: testuser
`
	state := parseGhAxiState(output)
	if state != "MERGED" {
		t.Errorf("expected MERGED, got %q", state)
	}
}

func TestParseGhAxiState_Open(t *testing.T) {
	output := `pull_request:
  number: 42
  title: "test"
  state: open
  author: testuser
`
	state := parseGhAxiState(output)
	if state != "OPEN" {
		t.Errorf("expected OPEN, got %q", state)
	}
}

func TestParseGhAxiState_Closed(t *testing.T) {
	output := `pull_request:
  number: 99
  title: "closed pr"
  state: closed
  author: testuser
`
	state := parseGhAxiState(output)
	if state != "CLOSED" {
		t.Errorf("expected CLOSED, got %q", state)
	}
}

func TestParseGhAxiState_EmptyOutput(t *testing.T) {
	state := parseGhAxiState("")
	if state != "" {
		t.Errorf("expected empty state, got %q", state)
	}
}

func TestParseGhAxiState_NoStateField(t *testing.T) {
	output := `pull_request:
  number: 1
  title: "no state"
`
	state := parseGhAxiState(output)
	if state != "" {
		t.Errorf("expected empty state, got %q", state)
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

// --- Pipeline GHAxiAdapter routing ---

func TestGHAxiAdapter_StateAwareConstruction(t *testing.T) {
	a := NewGHAxiAdapter()
	if a == nil {
		t.Fatal("expected non-nil adapter")
	}
	// State should be Ready or Absent depending on PATH
	if a.state != backend.Ready && a.state != backend.Absent {
		t.Errorf("unexpected state: %v", a.state)
	}
}

func TestGHAxiAdapter_RunPRCheck_FailsClosedOnAbsent(t *testing.T) {
	a := &GHAxiAdapter{state: backend.Absent}
	err := a.RunPRCheck("", "", "")
	if err == nil {
		t.Fatal("expected error for Absent state")
	}
	if !strings.Contains(err.Error(), "capability not ready") {
		t.Errorf("expected 'capability not ready' error, got: %v", err)
	}
}

func TestGHAxiAdapter_RunPRCheck_FailsClosedOnFailed(t *testing.T) {
	a := &GHAxiAdapter{state: backend.Failed}
	err := a.RunPRCheck("", "", "")
	if err == nil {
		t.Fatal("expected error for Failed state")
	}
	if !strings.Contains(err.Error(), "capability not ready") {
		t.Errorf("expected 'capability not ready' error, got: %v", err)
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

// --- Existing delivery contracts still hold ---

func TestGHAxiAdapter_ImplementsPipeline(t *testing.T) {
	// Compile-time check via var _ checks in pipeline.go
	// Runtime check that construction works and methods exist
	a := NewGHAxiAdapter()
	var p Pipeline = a
	if p == nil {
		t.Fatal("GHAxiAdapter must implement Pipeline")
	}
}

func TestExistingAdapterContracts_StillHold(t *testing.T) {
	// All existing adapters must still compile and construct
	adapters := []Pipeline{
		NewGHAxiAdapter(),
		NewNoMistakesAdapter(),
		NewGitLocalAdapter(),
		NewCompositeAdapter(),
	}
	for i, p := range adapters {
		if p == nil {
			t.Errorf("adapter[%d] must not be nil", i)
		}
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
