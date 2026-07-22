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

	// ViewMRJSON fetches MR metadata as JSON bytes for the requested fields.
	// Uses glab CLI through the consolidated adapter when glab is Ready.
	ViewMRJSON(host, owner, project string, iid int, fields string) ([]byte, error)
}

// glabClient implements GitLabClient backed by glab CLI.
// When glab is Ready, all operations route through glab.
type glabClient struct{}

// compile-time check
var _ GitLabClient = (*glabClient)(nil)

// ProbeGitLabCapability checks whether glab is available on PATH.
// Returns Ready if found, Absent if not found.
// Fail-closed: only Ready permits glab operations; all other states
// cause callers to reject the operation.
func ProbeGitLabCapability() capability.State {
	_, err := glabLookPath()
	if err != nil {
		return capability.Absent
	}
	return capability.Ready
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
// Parses the glab structured output for the "state" field.
func (c *glabClient) ViewMRState(host, owner, project string, iid int) (string, error) {
	glabPath, err := glabLookPath()
	if err != nil {
		return "", fmt.Errorf("glab not found on PATH: %w", err)
	}
	args := []string{
		"mr", "view",
		fmt.Sprintf("%s/%s!%d", owner, project, iid),
	}
	if host != "" && host != "gitlab.com" {
		args = append(args, "--hostname", host)
	}
	out, err := exec.Command(glabPath, args...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("glab mr view: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("glab mr view: %w", err)
	}

	state := parseGlabState(string(out))
	if state == "" {
		return "", fmt.Errorf("glab mr view: could not determine MR state from output")
	}
	return state, nil
}

// parseGlabState extracts the "state" field from glab's YAML-like output.
// Example input line: "state: opened"
func parseGlabState(output string) string {
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

// ViewMRJSON fetches MR metadata as JSON via glab CLI.
func (c *glabClient) ViewMRJSON(host, owner, project string, iid int, fields string) ([]byte, error) {
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
	args = append(args, "-F", fields)

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
func (c *glabClient) CaptureIdentity(mrURL string) (*DeliveryIdentity, error) {
	glURL, err := glurl.ParseMRURL(mrURL)
	if err != nil {
		return nil, fmt.Errorf("invalid MR URL: %w", err)
	}

	// Fetch MR metadata via glab for head SHA, source branch, target branch
	data, err := c.ViewMRJSON(glURL.Host, glURL.Owner, glURL.Project, glURL.IID, "sha,sourceBranch,targetBranch")
	if err != nil {
		return nil, err
	}

	var result struct {
		SHA          string `json:"sha"`
		SourceBranch string `json:"sourceBranch"`
		TargetBranch string `json:"targetBranch"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing glab mr view output: %w", err)
	}

	if result.SHA == "" {
		return nil, fmt.Errorf("glab mr view returned empty sha")
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
