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

// GitHubClient defines the typed GitHub capability used by the delivery
// surfaces. Delivery execution (Deliver) observes provider state through
// ObservePR and validates the typed delivery request through the gh-axi
// capability; the capability must be Ready and no raw gh fallback or
// alternate execution route exists on the delivery path. Read-only status
// and identity helpers (ViewPRJSON, CaptureIdentity) back the retained
// provider-neutral seams and route through gh-axi.
type GitHubClient interface {
	// ObservePR reads the current provider state of a pull request via
	// gh-axi, returning the exact observation (OPEN/MERGED/CLOSED plus head
	// and merged SHAs) needed to reconcile after a mutation.
	ObservePR(owner, repo string, number int) (DeliveryProviderObservation, error)

	// ViewPRJSON fetches PR metadata as JSON bytes via gh CLI. It backs the
	// retained provider-neutral read-only status seam; the delivery
	// execution path never uses it.
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

// githubDeliveryProvider adapts the typed GitHub capability (gh-axi only) to
// the narrow delivery capability consumed by Deliver.
type githubDeliveryProvider struct {
	client GitHubClient
}

// compile-time check
var _ DeliveryProvider = (*githubDeliveryProvider)(nil)

// ValidateMergeRequest verifies the pinned identity constraints. The current
// gh-axi capability cannot atomically enforce them for an irreversible merge.
func (p *githubDeliveryProvider) ValidateMergeRequest(ident domain.DeliveryIdentity, request DeliveryMergeRequest) error {
	if request.HeadSHA == "" || request.HeadSHA != ident.HeadSHA || request.BaseRef == "" || request.BaseRef != ident.BaseRef {
		return fmt.Errorf("GitHub merge constraints do not match the delivery identity")
	}
	return ErrDeliveryMergeConstraintsUnsupported
}

func (p *githubDeliveryProvider) Merge(ident domain.DeliveryIdentity, request DeliveryMergeRequest) error {
	return p.ValidateMergeRequest(ident, request)
}

// Observe reads the current provider state under the exact identity.
func (p *githubDeliveryProvider) Observe(ident domain.DeliveryIdentity) (DeliveryProviderObservation, error) {
	if p.client == nil {
		return DeliveryProviderObservation{}, fmt.Errorf("GitHub delivery capability is not composed")
	}
	return p.client.ObservePR(ident.Owner, ident.Repo, ident.Number)
}

// ghAxiAPI runs one gh-axi api invocation and returns stdout. All typed
// GitHub observation routes through gh-axi; there is no raw gh fallback.
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

// ObservePR reads the current pull request state via gh-axi api and returns
// the exact observation needed to reconcile after a mutation. GitHub REST
// reports merged PRs as state=closed with merged=true and a non-empty
// merge_commit_sha, so the merged classification never trusts state alone.
func (c *ghAxiClient) ObservePR(owner, repo string, number int) (DeliveryProviderObservation, error) {
	out, err := ghAxiAPI(
		fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number),
		"--jq", `{state: .state, headSha: .head.sha, baseRef: .base.ref, merged: .merged, mergedSha: .merge_commit_sha}`,
	)
	if err != nil {
		return DeliveryProviderObservation{}, err
	}
	return classifyGitHubRESTObservation(out)
}

func classifyGitHubRESTObservation(data []byte) (DeliveryProviderObservation, error) {
	values := parseGhAxiKeyValues(string(data))
	state := strings.ToUpper(values["state"])
	if state != "OPEN" && state != "CLOSED" {
		return DeliveryProviderObservation{}, fmt.Errorf("gh-axi api: invalid pull request state")
	}
	if values["headSha"] == "" || values["baseRef"] == "" {
		return DeliveryProviderObservation{}, fmt.Errorf("gh-axi api: incomplete pull request identity evidence")
	}
	merged, ok := parseBoolean(values["merged"])
	if !ok {
		return DeliveryProviderObservation{}, fmt.Errorf("gh-axi api: missing or invalid merged evidence")
	}
	obs := DeliveryProviderObservation{
		State:        state,
		HeadSHA:      values["headSha"],
		BaseRef:      values["baseRef"],
		Mergeability: DeliveryMergeabilityUnknown,
	}
	if merged {
		if values["mergedSha"] == "" {
			return DeliveryProviderObservation{}, fmt.Errorf("gh-axi api: merged pull request is missing merge commit evidence")
		}
		obs.State = "MERGED"
		obs.MergedSHA = values["mergedSha"]
	}
	return obs, nil
}

func parseBoolean(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

// ViewPRJSON fetches PR metadata as JSON via gh CLI. This backs the retained
// provider-neutral read-only status seam (QueryPRMergeStatus); the delivery
// execution path never uses it.
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
