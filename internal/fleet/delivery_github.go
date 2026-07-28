// Package delivery implements delivery operations: review-diff, pr-check,
// pr-merge, merge-local, and no-mistakes pipeline integration.
package fleet

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/capability"
	"github.com/minhtri2710/munsu/internal/domain"
)

// GitHubClient defines the GitHub operations used by delivery surfaces.
// All operations go through the consolidated authority path backed by gh-axi.
// When the GitHub capability is Absent or Failed, callers must fail closed.
type GitHubClient interface {
	// MergePR merges a pull request via gh-axi.
	MergePR(owner, repo string, number int, method string) error

	// ViewPRState returns the PR state (OPEN, MERGED, CLOSED) via gh-axi.
	ViewPRState(owner, repo string, number int) (string, error)

	// ViewPRJSON fetches PR metadata as JSON bytes for the requested fields.
	// Uses gh CLI through the consolidated adapter when gh-axi is Ready.
	ViewPRJSON(owner, repo string, number int, fields string) ([]byte, error)

	// CaptureIdentity captures a full domain.DeliveryIdentity from a PR URL.
	CaptureIdentity(prURL string) (*domain.DeliveryIdentity, error)
}

// ghAxiClient implements GitHubClient backed by gh-axi.
// When gh-axi is Ready, merge and view operations route through gh-axi.
// JSON queries fall through to gh CLI via the consolidated adapter path,
// never at the call site.
type ghAxiClient struct{}

// compile-time check
var _ GitHubClient = (*ghAxiClient)(nil)

// ProbeGitHubCapability checks whether gh-axi is available on PATH.
// Returns Ready if found, Absent if not found.
// Fail-closed: only Ready permits gh-axi operations; all other states
// cause callers to reject the operation.
func ProbeGitHubCapability() capability.State {
	_, err := ghAxiLookPath()
	if err != nil {
		return capability.Absent
	}
	return capability.Ready
}

// ghAxiLookPath is a variable for testing — can be replaced to simulate
// gh-axi absence without modifying PATH.
var ghAxiLookPath = func() (string, error) {
	return exec.LookPath("gh-axi")
}

// ghCLI is a variable for testing — replaces the gh binary path lookup.
var ghCLILookPath = func() (string, error) {
	return exec.LookPath("gh")
}

// GitHubClientForState returns the appropriate GitHubClient or an error
// based on the capability state. Fails closed on Absent/Failed/Unsupported.
func GitHubClientForState(s capability.State) (GitHubClient, error) {
	switch s {
	case capability.Ready:
		return &ghAxiClient{}, nil
	case capability.Absent:
		return nil, fmt.Errorf("GitHub capability absent: gh-axi not found on PATH")
	case capability.Unsupported:
		return nil, fmt.Errorf("GitHub capability unsupported: gh-axi is not available on this platform")
	case capability.Failed:
		return nil, fmt.Errorf("GitHub capability failed: gh-axi encountered an error")
	default:
		return nil, fmt.Errorf("GitHub capability in unknown state: %v", s)
	}
}

// DefaultGitHubClient probes the current environment and returns a client
// if gh-axi is Ready, or an error if it is Absent/Failed/Unsupported.
func DefaultGitHubClient() (GitHubClient, error) {
	return GitHubClientForState(ProbeGitHubCapability())
}

// MergePR merges a PR via gh-axi CLI. Stdout/stderr pass through to the caller.
func (c *ghAxiClient) MergePR(owner, repo string, number int, method string) error {
	ghAxiPath, err := ghAxiLookPath()
	if err != nil {
		return fmt.Errorf("gh-axi not found on PATH: %w", err)
	}
	args := []string{
		"pr", "merge",
		fmt.Sprintf("%d", number),
		"--repo", fmt.Sprintf("%s/%s", owner, repo),
		fmt.Sprintf("--%s", method),
	}
	cmd := exec.Command(ghAxiPath, args...)
	return cmd.Run()
}

// ViewPRState returns the PR state (OPEN, MERGED, CLOSED) via gh-axi.
// Parses the gh-axi structured output for the "state" field.
func (c *ghAxiClient) ViewPRState(owner, repo string, number int) (string, error) {
	ghAxiPath, err := ghAxiLookPath()
	if err != nil {
		return "", fmt.Errorf("gh-axi not found on PATH: %w", err)
	}
	args := []string{
		"pr", "view",
		fmt.Sprintf("%d", number),
		"--repo", fmt.Sprintf("%s/%s", owner, repo),
	}
	out, err := exec.Command(ghAxiPath, args...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("gh-axi pr view: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("gh-axi pr view: %w", err)
	}

	state := parseGhAxiState(string(out))
	if state == "" {
		return "", fmt.Errorf("gh-axi pr view: could not determine PR state from output")
	}
	return state, nil
}

// parseGhAxiState extracts the "state" field from gh-axi's YAML-like output.
// Example input line: "  state: merged"
func parseGhAxiState(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "state:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "state:"))
			val = strings.Trim(val, `"`)
			return strings.ToUpper(val)
		}
	}
	return ""
}

// ViewPRJSON fetches PR metadata as JSON via gh CLI.
// This is used when gh-axi does not expose the raw JSON fields needed
// (headRefOid, headRefName, baseRefName, mergeCommit). The call goes
// through the consolidated adapter — never at the call site directly.
func (c *ghAxiClient) ViewPRJSON(owner, repo string, number int, fields string) ([]byte, error) {
	ghPath, err := ghCLILookPath()
	if err != nil {
		return nil, fmt.Errorf("gh not found on PATH: %w", err)
	}
	args := []string{
		"pr", "view",
		fmt.Sprintf("%d", number),
		"--repo", fmt.Sprintf("%s/%s", owner, repo),
		"--json", fields,
	}
	cmd := exec.Command(ghPath, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh pr view: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh pr view: %w", err)
	}
	return out, nil
}

// CaptureIdentity captures a full domain.DeliveryIdentity from a PR URL.
// Uses gh CLI through the consolidated adapter for JSON fields that
// gh-axi does not expose (headRefOid, headRefName, baseRefName).
func (c *ghAxiClient) CaptureIdentity(prURL string) (*domain.DeliveryIdentity, error) {
	ghURL, err := domain.ParseGHURL(prURL)
	if err != nil {
		return nil, fmt.Errorf("invalid PR URL: %w", err)
	}

	data, err := c.ViewPRJSON(ghURL.Owner, ghURL.Repo, ghURL.Num, "headRefOid,headRefName,baseRefName")
	if err != nil {
		return nil, err
	}

	var result struct {
		HeadRefOid  string `json:"headRefOid"`
		HeadRefName string `json:"headRefName"`
		BaseRefName string `json:"baseRefName"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing gh pr view output: %w", err)
	}

	if result.HeadRefOid == "" {
		return nil, fmt.Errorf("gh pr view returned empty headRefOid")
	}

	return &domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      ghURL.Owner,
		Repo:       ghURL.Repo,
		Number:     ghURL.Num,
		URL:        prURL,
		BaseRef:    result.BaseRefName,
		HeadRef:    result.HeadRefName,
		HeadSHA:    result.HeadRefOid,
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}
