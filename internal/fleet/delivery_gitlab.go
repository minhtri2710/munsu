package fleet

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
)

// GlabRunner abstracts glab CLI operations for testability.
// All glab invocations route through this interface.
type GlabRunner interface {
	// LookPath returns the glab binary path, or an error if not found.
	LookPath() (string, error)

	// Run executes glab with the given args and returns stdout.
	Run(args ...string) ([]byte, error)
}

// glabRunnerImpl is the production GlabRunner using os/exec.
type glabRunnerImpl struct{}

func (r *glabRunnerImpl) LookPath() (string, error) {
	return exec.LookPath("glab")
}

func (r *glabRunnerImpl) Run(args ...string) ([]byte, error) {
	path, err := r.LookPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("glab %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("glab %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// defaultGlabRunner is the production runner; replace for testing.
var defaultGlabRunner GlabRunner = &glabRunnerImpl{}

// GlabFallbackFn is an injectable read-only fallback for when glab is
// Absent or Unsupported. When nil, the fallback returns an unavailable error.
type GlabFallbackFn func(ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error)

// defaultGlabFallback is the production fallback (no fallback available).
var defaultGlabFallback GlabFallbackFn = nil

// ParseProviderURL detects the provider from a PR/MR URL and parses it
// into the provider-specific components. Returns the provider name and a
// provider-agnostic (owner, repo, number) tuple.
// Supports GitHub and GitLab URLs. Rejects unrecognized URLs fail closed.
func ParseProviderURL(raw string) (provider string, owner string, repo string, number int, fullURL string, err error) {
	// Try GitHub first (must not break existing behavior)
	gh, ghErr := domain.ParseGHURL(raw)
	if ghErr == nil {
		return "github", gh.Owner, gh.Repo, gh.Num, gh.FullURL(), nil
	}

	// Try GitLab
	gl, glErr := domain.ParseMRURL(raw)
	if glErr == nil {
		return "gitlab", gl.Owner, gl.Project, gl.IID, gl.FullURL(), nil
	}

	// Both failed — report both errors for diagnostic clarity
	return "", "", "", 0, "", fmt.Errorf("unrecognized PR/MR URL: %q (github: %v; gitlab: %v)", raw, ghErr, glErr)
}

// GitLabClient defines the GitLab operations used by delivery surfaces.
// All operations go through the consolidated authority path backed by glab.
// When the GitLab capability is Absent or Failed, callers must fail closed.
type GitLabClient interface {
	// CaptureIdentity captures a full domain.DeliveryIdentity from a GitLab MR URL.
	CaptureIdentity(mrURL string) (*domain.DeliveryIdentity, error)

	// ViewMRState returns the MR state (OPEN, MERGED, CLOSED) via glab.
	ViewMRState(host, owner, project string, iid int) (string, error)

	// ViewMRJSON fetches MR metadata as JSON via glab --output json.
	ViewMRJSON(host, owner, project string, iid int) ([]byte, error)

	// MergeMR merges a merge request via glab. It is the irreversible
	// provider mutation of the delivery execution path, called at most once
	// per delivery journal.
	MergeMR(host, owner, project string, iid int, method string) error
}

// glabClient implements GitLabClient backed by glab CLI via GlabRunner.
type glabClient struct {
	runner GlabRunner
}

// compile-time check
var _ GitLabClient = (*glabClient)(nil)

// ProbeGitLabCapability probes glab availability through the default runner.
// Returns one of: Ready, Absent, Failed, Unsupported.
func ProbeGitLabCapability() backend.State {
	return probeGlabCapability(defaultGlabRunner)
}

// probeGlabCapability probes glab availability through the given runner.
func probeGlabCapability(runner GlabRunner) backend.State {
	_, err := runner.LookPath()
	if err != nil {
		return backend.Absent
	}

	// Verify --version works
	out, err := runner.Run("--version")
	if err != nil || len(out) == 0 {
		return backend.Failed
	}

	// Verify mr view subcommand is available
	helpOut, err := runner.Run("mr", "view", "--help")
	if err != nil || !strings.Contains(string(helpOut), "view") {
		return backend.Unsupported
	}

	// Verify authentication via glab auth status
	authOut, err := runner.Run("auth", "status")
	if err != nil || !strings.Contains(string(authOut), "authenticated") {
		return backend.Failed
	}

	return backend.Ready
}

// GitLabClientForState returns the appropriate GitLabClient or an error
// based on the capability state. Fails closed on Absent/Failed/Unsupported.
func GitLabClientForState(s backend.State) (GitLabClient, error) {
	switch s {
	case backend.Ready:
		return &glabClient{runner: defaultGlabRunner}, nil
	case backend.Absent:
		return nil, fmt.Errorf("GitLab capability absent: glab not found on PATH")
	case backend.Unsupported:
		return nil, fmt.Errorf("GitLab capability unsupported: glab is not available on this platform")
	case backend.Failed:
		return nil, fmt.Errorf("GitLab capability failed: glab encountered an error")
	default:
		return nil, fmt.Errorf("GitLab capability in unknown state: %v", s)
	}
}

// DefaultGitLabClient probes the current environment and returns a client
// if glab is Ready, or an error if it is Absent/Failed/Unsupported.
func DefaultGitLabClient() (GitLabClient, error) {
	return GitLabClientForState(ProbeGitLabCapability())
}

// gitlabDeliveryProvider adapts the typed GitLab capability (glab only) to
// the narrow delivery capability consumed by Deliver.
type gitlabDeliveryProvider struct {
	client GitLabClient
}

// compile-time check
var _ DeliveryProvider = (*gitlabDeliveryProvider)(nil)

// Merge executes the irreversible provider merge under the exact identity.
func (p *gitlabDeliveryProvider) Merge(ident domain.DeliveryIdentity, method string) error {
	if p.client == nil {
		return fmt.Errorf("GitLab delivery capability is not composed")
	}
	glURL, err := domain.ParseMRURL(ident.URL)
	if err != nil {
		return fmt.Errorf("invalid MR URL in identity: %w", err)
	}
	return p.client.MergeMR(glURL.Host, glURL.Owner, glURL.Project, glURL.IID, method)
}

// Observe reads the current provider state under the exact identity.
func (p *gitlabDeliveryProvider) Observe(ident domain.DeliveryIdentity) (DeliveryProviderObservation, error) {
	if p.client == nil {
		return DeliveryProviderObservation{}, fmt.Errorf("GitLab delivery capability is not composed")
	}
	glURL, err := domain.ParseMRURL(ident.URL)
	if err != nil {
		return DeliveryProviderObservation{}, fmt.Errorf("invalid MR URL in identity: %w", err)
	}
	data, err := p.client.ViewMRJSON(glURL.Host, glURL.Owner, glURL.Project, glURL.IID)
	if err != nil {
		return DeliveryProviderObservation{}, err
	}
	status, err := parseGLMergeStatus(data)
	if err != nil {
		return DeliveryProviderObservation{}, err
	}
	return DeliveryProviderObservation{
		State:     status.State,
		HeadSHA:   status.HeadSHA,
		MergedSHA: status.MergedSHA,
	}, nil
}

// MergeMR merges a merge request via glab with the given method: squash,
// merge (default merge commit), or rebase.
func (c *glabClient) MergeMR(host, owner, project string, iid int, method string) error {
	args := []string{
		"mr", "merge",
		fmt.Sprintf("%s/%s!%d", owner, project, iid),
	}
	if host != "" && host != "gitlab.com" {
		args = append(args, "--hostname", host)
	}
	switch method {
	case "squash":
		args = append(args, "--squash")
	case "rebase":
		args = append(args, "--rebase")
	case "merge":
		// glab default: merge commit
	default:
		return fmt.Errorf("unsupported GitLab merge method %q", method)
	}
	if _, err := c.runner.Run(args...); err != nil {
		return err
	}
	return nil
}

// ViewMRState returns the MR state (OPEN, MERGED, CLOSED) via glab.
func (c *glabClient) ViewMRState(host, owner, project string, iid int) (string, error) {
	data, err := c.ViewMRJSON(host, owner, project, iid)
	if err != nil {
		return "", err
	}

	var raw struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("parsing glab mr view JSON: %w", err)
	}

	return normalizeGlabState(raw.State), nil
}

// normalizeGlabState normalizes GitLab state strings to the domain convention.
//
//	"opened" -> "OPEN"
//	"merged" -> "MERGED"
//	"closed" -> "CLOSED"
func normalizeGlabState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "opened":
		return "OPEN"
	case "merged":
		return "MERGED"
	case "closed":
		return "CLOSED"
	default:
		return strings.ToUpper(s)
	}
}

// ViewMRJSON fetches MR metadata as JSON via glab CLI using --output json.
func (c *glabClient) ViewMRJSON(host, owner, project string, iid int) ([]byte, error) {
	args := []string{
		"mr", "view",
		fmt.Sprintf("%s/%s!%d", owner, project, iid),
	}
	if host != "" && host != "gitlab.com" {
		args = append(args, "--hostname", host)
	}
	args = append(args, "-F", "json")

	return c.runner.Run(args...)
}

// CaptureIdentity captures a full domain.DeliveryIdentity from a GitLab MR URL.
func (c *glabClient) CaptureIdentity(mrURL string) (*domain.DeliveryIdentity, error) {
	glURL, err := domain.ParseMRURL(mrURL)
	if err != nil {
		return nil, fmt.Errorf("invalid MR URL: %w", err)
	}

	data, err := c.ViewMRJSON(glURL.Host, glURL.Owner, glURL.Project, glURL.IID)
	if err != nil {
		return nil, err
	}

	// GitLab API JSON uses snake_case fields
	var result struct {
		SHA            string `json:"sha"`
		SourceBranch   string `json:"source_branch"`
		TargetBranch   string `json:"target_branch"`
		MergeCommitSHA string `json:"merge_commit_sha"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing glab mr view JSON: %w", err)
	}

	if result.SHA == "" {
		return nil, fmt.Errorf("glab mr view returned empty sha")
	}
	if result.SourceBranch == "" {
		return nil, fmt.Errorf("glab mr view returned empty source_branch")
	}
	if result.TargetBranch == "" {
		return nil, fmt.Errorf("glab mr view returned empty target_branch")
	}

	return &domain.DeliveryIdentity{
		Provider:   "gitlab",
		Owner:      glURL.Owner,
		Repo:       glURL.Project,
		Number:     glURL.IID,
		URL:        mrURL,
		BaseRef:    result.TargetBranch,
		HeadRef:    result.SourceBranch,
		HeadSHA:    result.SHA,
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}
