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
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/testutil"
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

// --- gh-axi output parsing ---

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

// --- DefaultGitHubClient path routing ---

func TestForgeClientFor_GitHubRoutesToGhAxiOnlyWhenReady(t *testing.T) {
	old := ghAxiLookPath
	t.Cleanup(func() { ghAxiLookPath = old })

	ghAxiLookPath = func() (string, error) { return "/fake/gh-axi", nil }
	forge, err := forgeClientFor(githubForgeStep)
	if err != nil {
		t.Fatalf("forgeClientFor with gh-axi Ready: %v", err)
	}
	if forge.github == nil {
		t.Fatalf("forgeClientFor with gh-axi Ready = %+v, want the gh-axi client", forge)
	}

	ghAxiLookPath = func() (string, error) { return "", errors.New("not found") }
	forge, err = forgeClientFor(githubForgeStep)
	if err == nil || !strings.Contains(err.Error(), "configured forge is not Ready") {
		t.Fatalf("forgeClientFor with gh-axi Absent = %+v, %v; want not-Ready refusal", forge, err)
	}
	if forge.github != nil {
		t.Fatalf("forgeClientFor with gh-axi Absent returned a client")
	}
}

// --- QueryDeliveryMergeStatus routing ---

var githubStatusIdentity = domain.DeliveryIdentity{Provider: "github", Owner: "owner", Repo: "repo", Number: 42, URL: "https://github.com/owner/repo/pull/42"}

func TestQueryDeliveryMergeStatus_UsesGhAxiWhenReady(t *testing.T) {
	// gh-axi on PATH makes the capability Ready. The consolidated adapter reads
	// status through the ghCLILookPath seam; that gh is off PATH, so only the
	// Ready route can answer.
	binDir := t.TempDir()
	testutil.WriteFakeExecutable(t, filepath.Join(binDir, "gh-axi"), "#!/bin/sh\nexit 1\n")
	testutil.SetPath(t, binDir)

	ghDir := t.TempDir()
	argsFile := filepath.Join(ghDir, "args")
	gh := testutil.WriteFakeExecutable(t, filepath.Join(ghDir, "gh"), `#!/bin/sh
printf '%s\n' "$@" > '`+filepath.ToSlash(argsFile)+`'
echo '{"state":"MERGED","headRefOid":"6b52a27d68fdf6034cc2defc79420882440e87ef","mergeCommit":{"oid":"38a1a401bfa3b272ee4ee99e7cef7920461d9e10"}}'
`)
	oldGH := ghCLILookPath
	t.Cleanup(func() { ghCLILookPath = oldGH })
	ghCLILookPath = func() (string, error) { return gh, nil }

	status, err := QueryDeliveryMergeStatus(githubForgeStep, &githubStatusIdentity)
	if err != nil {
		t.Fatalf("QueryDeliveryMergeStatus: %v", err)
	}
	want := domain.PRMergeStatus{
		State:     "MERGED",
		HeadSHA:   "6b52a27d68fdf6034cc2defc79420882440e87ef",
		MergedSHA: "38a1a401bfa3b272ee4ee99e7cef7920461d9e10",
		Merged:    true,
	}
	if *status != want {
		t.Fatalf("status = %+v, want %+v", *status, want)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("Ready path did not run the adapter gh: %v", err)
	}
	if got, wantArgs := string(args), "pr\nview\n42\n--repo\nowner/repo\n--json\nstate,headRefOid,mergeCommit\n"; got != wantArgs {
		t.Fatalf("adapter gh args = %q, want %q", got, wantArgs)
	}
}

func TestQueryDeliveryMergeStatus_FailsClosedWithoutGhAxi(t *testing.T) {
	// gh-axi is absent and a gh that records any call is on PATH: the status
	// read must refuse and gh must never run.
	binDir := t.TempDir()
	calledFile := filepath.Join(binDir, "called")
	testutil.WriteFakeExecutable(t, filepath.Join(binDir, "gh"), `#!/bin/sh
: > '`+filepath.ToSlash(calledFile)+`'
echo '{"state":"OPEN","headRefOid":"6b52a27d68fdf6034cc2defc79420882440e87ef"}'
`)
	testutil.SetPath(t, binDir)
	old := ghAxiLookPath
	t.Cleanup(func() { ghAxiLookPath = old })
	ghAxiLookPath = func() (string, error) { return "", errors.New("not found") }

	status, err := QueryDeliveryMergeStatus(githubForgeStep, &githubStatusIdentity)
	if err == nil || !strings.Contains(err.Error(), "configured forge is not Ready") {
		t.Fatalf("QueryDeliveryMergeStatus without gh-axi = %+v, %v; want not-Ready refusal", status, err)
	}
	if _, statErr := os.Stat(calledFile); statErr == nil {
		t.Fatal("QueryDeliveryMergeStatus without gh-axi ran gh")
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

// TestForgeClientRefusesBaselineAndProviderMismatch pins the two refusals in
// captured-forge-to-client resolution: a baseline task has no forge, and a
// delivery identity must name the step's own provider.
func TestForgeClientRefusesBaselineAndProviderMismatch(t *testing.T) {
	if _, err := forgeClientFor(taskauthority.DeliveryStep{Baseline: true, ProbeState: "baseline"}); err == nil || !strings.Contains(err.Error(), "no configured forge tool") {
		t.Fatalf("forgeClientFor(baseline) = %v, want a no-forge refusal", err)
	}
	if _, err := forgeClientForIdentity(gitlabForgeStep, domain.DeliveryIdentity{Provider: "github"}); err == nil || !strings.Contains(err.Error(), "does not match the task's configured forge adapter") {
		t.Fatalf("forgeClientForIdentity(gitlab step, github identity) = %v, want a provider mismatch refusal", err)
	}
}
