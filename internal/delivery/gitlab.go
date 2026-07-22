package delivery

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/capability"
	"github.com/minhtri2710/munsu/internal/ghurl"
	"github.com/minhtri2710/munsu/internal/glurl"
)

// ParseProviderURL detects the provider from a PR/MR URL and parses it
// into the provider-specific components. Returns the provider name and a
// provider-agnostic (owner, repo, number) tuple.
// Supports GitHub and GitLab URLs. Rejects unrecognized URLs fail closed.
func ParseProviderURL(raw string) (provider string, owner string, repo string, number int, fullURL string, err error) {
	// Try GitHub first (must not break existing behavior)
	gh, ghErr := ghurl.ParseGHURL(raw)
	if ghErr == nil {
		return "github", gh.Owner, gh.Repo, gh.Num, gh.FullURL(), nil
	}

	// Try GitLab
	gl, glErr := glurl.ParseMRURL(raw)
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
	// CaptureIdentity captures a full DeliveryIdentity from a GitLab MR URL.
	CaptureIdentity(mrURL string) (*DeliveryIdentity, error)

	// ViewMRState returns the MR state (OPEN, MERGED, CLOSED) via glab.
	ViewMRState(host, owner, project string, iid int) (string, error)

	// ViewMRJSON fetches MR metadata as JSON via glab --output json.
	// Returns the full JSON output; callers parse the fields they need.
	ViewMRJSON(host, owner, project string, iid int) ([]byte, error)
}

// glabClient implements GitLabClient backed by glab CLI.
// When glab is Ready, all operations route through glab.
type glabClient struct{}

// compile-time check
var _ GitLabClient = (*glabClient)(nil)

// GlabProbeFn is an injectable probe function that returns a full
// capability.State (Ready, Absent, Failed, or Unsupported) based on
// glab availability and correctness.
type GlabProbeFn func() capability.State

// defaultGlabProbe is the production probe that checks PATH and basic
// invocation. Replace for testing.
var defaultGlabProbe GlabProbeFn = func() capability.State {
	path, err := glabLookPath()
	if err != nil {
		return capability.Absent
	}

	// Verify glab responds to a basic command
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return capability.Failed
	}
	if len(out) == 0 {
		return capability.Failed
	}

	// Verify mr view subcommand is available
	helpOut, err := exec.Command(path, "mr", "view", "--help").Output()
	if err != nil || !strings.Contains(string(helpOut), "view") {
		return capability.Unsupported
	}

	return capability.Ready
}

// ProbeGitLabCapability checks whether glab is available on PATH using the
// default probe. Returns one of: Ready (found+working), Absent (not on PATH),
// Failed (found but errored), Unsupported (found but command surface missing).
func ProbeGitLabCapability() capability.State {
	return defaultGlabProbe()
}

// glabLookPath is a variable for testing — can be replaced to simulate
// glab absence without modifying PATH.
var glabLookPath = func() (string, error) {
	return exec.LookPath("glab")
}

// GitLabClientForState returns the appropriate GitLabClient or an error
// based on the capability state. Fails closed on Absent/Failed/Unsupported.
func GitLabClientForState(s capability.State) (GitLabClient, error) {
	switch s {
	case capability.Ready:
		return &glabClient{}, nil
	case capability.Absent:
		return nil, fmt.Errorf("GitLab capability absent: glab not found on PATH")
	case capability.Unsupported:
		return nil, fmt.Errorf("GitLab capability unsupported: glab is not available on this platform")
	case capability.Failed:
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

// ViewMRState returns the MR state (OPEN, MERGED, CLOSED) via glab.
// Parses the glab JSON output for the "state" field.
// GitLab uses "opened" / "merged" / "closed"; we normalize to upper case.
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
//   "opened" -> "OPEN"
//   "merged" -> "MERGED"
//   "closed" -> "CLOSED"
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
// glab does not support field selection at the CLI level — it returns the
// full MR JSON. Callers extract the fields they need via json.Unmarshal.
func (c *glabClient) ViewMRJSON(host, owner, project string, iid int) ([]byte, error) {
	glabPath, err := glabLookPath()
	if err != nil {
		return nil, fmt.Errorf("glab not found on PATH: %w", err)
	}
	args := []string{
		"mr", "view",
		fmt.Sprintf("%s/%s!%d", owner, project, iid),
	}
	if host != "" && host != "gitlab.com" {
		args = append(args, "--hostname", host)
	}
	args = append(args, "-F", "json")

	cmd := exec.Command(glabPath, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("glab mr view: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("glab mr view: %w", err)
	}
	return out, nil
}

// CaptureIdentity captures a full DeliveryIdentity from a GitLab MR URL.
// Uses glab CLI through the consolidated adapter for JSON fields.
// The GitLab API uses snake_case JSON keys: sha, source_branch, target_branch.
func (c *glabClient) CaptureIdentity(mrURL string) (*DeliveryIdentity, error) {
	glURL, err := glurl.ParseMRURL(mrURL)
	if err != nil {
		return nil, fmt.Errorf("invalid MR URL: %w", err)
	}

	// Fetch full MR JSON via glab --output json
	data, err := c.ViewMRJSON(glURL.Host, glURL.Owner, glURL.Project, glURL.IID)
	if err != nil {
		return nil, err
	}

	// Parse GitLab API fields (snake_case)
	var result struct {
		SHA          string `json:"sha"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
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

	return &DeliveryIdentity{
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

// runGlabMRView runs glab mr view for the given MR identity and returns
// the raw output. This is the typed authority seam for GitLab MR status paths.
// It must not be called outside the GitLabClient implementation; all MR
// operations route through GitLabClientForState.
func runGlabMRView(host, owner, project string, iid int, args ...string) ([]byte, error) {
	client, err := DefaultGitLabClient()
	if err != nil {
		return nil, fmt.Errorf("GitLab provider not available: %w", err)
	}
	// All MR view operations use ViewMRJSON under the hood
	return client.ViewMRJSON(host, owner, project, iid)
}
