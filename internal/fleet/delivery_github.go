// Package delivery implements delivery operations: the journaled Fleet
// delivery execution path, review-diff, pr status reads, and no-mistakes
// pipeline integration.
package fleet

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
)

// GitHubClient defines the typed read-only GitHub capability behind the
// provider-neutral status and identity seams. The delivery observation and the
// merge mutation are the separate GitHubDeliveryClient (delivery_github_merge.go).
type GitHubClient interface {
	// ViewPRJSON fetches PR metadata as JSON bytes via gh CLI. It backs the
	// retained provider-neutral read-only status seam.
	ViewPRJSON(owner, repo string, number int, fields string) ([]byte, error)

	// CaptureIdentity captures a full domain.DeliveryIdentity from a PR URL
	// via gh-axi.
	CaptureIdentity(prURL string) (*domain.DeliveryIdentity, error)
}

// ghAxiClient implements GitHubClient backed by gh-axi.
type ghAxiClient struct{}

// compile-time check
var _ GitHubClient = (*ghAxiClient)(nil)

// ProbeGitHubCapability checks whether gh-axi is available on PATH.
// Returns Ready if found, Absent if not found.
// Fail-closed: only Ready permits gh-axi operations; all other states
// cause callers to reject the operation.
func ProbeGitHubCapability() backend.State {
	_, err := ghAxiLookPath()
	if err != nil {
		return backend.Absent
	}
	return backend.Ready
}

// ghAxiLookPath is a variable for testing — can be replaced to simulate
// gh-axi absence without modifying PATH.
var ghAxiLookPath = func() (string, error) {
	return exec.LookPath("gh-axi")
}

// ghCLILookPath is a variable for testing — replaces the gh binary path
// lookup for the retained read-only ViewPRJSON seam.
var ghCLILookPath = func() (string, error) {
	return exec.LookPath("gh")
}

// GitHubClientForState returns the appropriate GitHubClient or an error
// based on the capability state. Fails closed on Absent/Failed/Unsupported.
func GitHubClientForState(s backend.State) (GitHubClient, error) {
	switch s {
	case backend.Ready:
		return &ghAxiClient{}, nil
	case backend.Absent:
		return nil, fmt.Errorf("GitHub capability absent: gh-axi not found on PATH")
	case backend.Unsupported:
		return nil, fmt.Errorf("GitHub capability unsupported: gh-axi is not available on this platform")
	case backend.Failed:
		return nil, fmt.Errorf("GitHub capability failed: gh-axi encountered an error")
	default:
		return nil, fmt.Errorf("GitHub capability in unknown state: %v", s)
	}
}

// DefaultGitHubClient probes the current environment and returns a client
// if gh-axi is Ready, or an error if it is Absent/Failed/Unsupported.
var DefaultGitHubClient = defaultGitHubClientImpl

func defaultGitHubClientImpl() (GitHubClient, error) {
	return GitHubClientForState(ProbeGitHubCapability())
}

// ghAxiAPI runs one gh-axi api invocation and returns stdout. GitHub
// identity capture routes through gh-axi; there is no raw gh fallback.
func ghAxiAPI(args ...string) ([]byte, error) {
	ghAxiPath, err := ghAxiLookPath()
	if err != nil {
		return nil, fmt.Errorf("gh-axi not found on PATH: %w", err)
	}
	cmd := exec.Command(ghAxiPath, append([]string{"api"}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh-axi api: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh-axi api: %w", err)
	}
	return out, nil
}

// parseGhAxiKeyValues parses gh-axi's "key: value" line output into a map.
func parseGhAxiKeyValues(output string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		value = strings.Trim(value, `"`)
		values[key] = value
	}
	return values
}

// ViewPRJSON fetches PR metadata as JSON via gh CLI. This backs the retained
// provider-neutral read-only status seam (QueryPRMergeStatus).
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

// CaptureIdentity captures a full domain.DeliveryIdentity from a PR URL via
// gh-axi api (no raw gh fallback).
func (c *ghAxiClient) CaptureIdentity(prURL string) (*domain.DeliveryIdentity, error) {
	ghURL, err := domain.ParseGHURL(prURL)
	if err != nil {
		return nil, fmt.Errorf("invalid PR URL: %w", err)
	}

	out, err := ghAxiAPI(
		fmt.Sprintf("/repos/%s/%s/pulls/%d", ghURL.Owner, ghURL.Repo, ghURL.Num),
		"--jq", `{headRefOid: .head.sha, headRefName: .head.ref, baseRefName: .base.ref}`,
	)
	if err != nil {
		return nil, err
	}
	values := parseGhAxiKeyValues(string(out))
	if values["headRefOid"] == "" {
		return nil, fmt.Errorf("gh-axi api returned empty headRefOid")
	}

	return &domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      ghURL.Owner,
		Repo:       ghURL.Repo,
		Number:     ghURL.Num,
		URL:        prURL,
		BaseRef:    values["baseRefName"],
		HeadRef:    values["headRefName"],
		HeadSHA:    values["headRefOid"],
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}
