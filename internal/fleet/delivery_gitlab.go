package fleet

import (
	"encoding/json"
	"fmt"
	"net/url"
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

// GitLabClient defines the GitLab operations used by delivery surfaces.
// All operations go through the consolidated authority path backed by glab.
// When the GitLab capability is Absent or Failed, callers must fail closed.
type GitLabClient interface {
	// CaptureIdentity captures a full domain.DeliveryIdentity from a GitLab MR URL.
	CaptureIdentity(mrURL string) (*domain.DeliveryIdentity, error)

	// ViewMRJSON fetches MR metadata through the typed GitLab API.
	ViewMRJSON(host, owner, project string, iid int) ([]byte, error)
	// ChangesRequested reports whether any reviewer requested changes, so the
	// delivery observation honors domain.PR.CanMerge's refusal on
	// ReviewChangesRequested. Approval is never read from GitLab (ADR-0025).
	ChangesRequested(host, owner, project string, iid int) (bool, error)

	// MergeMR performs the typed GitLab merge mutation with the pinned request.
	MergeMR(host, owner, project string, iid int, request DeliveryMergeRequest) error
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

	// Verify API subcommand is available
	helpOut, err := runner.Run("api", "--help")
	if err != nil || !strings.Contains(string(helpOut), "api") {
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

// gitlabMergeSquashValue maps the Fleet merge method to the GitLab merge API
// parameter. GitLab's merge endpoint does not provide a rebase merge method.
func gitlabMergeSquashValue(method string) (string, error) {
	switch method {
	case "merge":
		return "false", nil
	case "squash":
		return "true", nil
	default:
		return "", fmt.Errorf("GitLab merge method %q is unsupported (merge, squash)", method)
	}
}

// ValidateMergeRequest verifies the pinned identity constraints before the
// irreversible API call. The base ref is checked here as the compensating
// observation for the endpoint's lack of an expected-target parameter.
func (p *gitlabDeliveryProvider) ValidateMergeRequest(ident domain.DeliveryIdentity, request DeliveryMergeRequest) error {
	if request.HeadSHA == "" || request.HeadSHA != ident.HeadSHA || request.BaseRef == "" || request.BaseRef != ident.BaseRef {
		return fmt.Errorf("GitLab merge constraints do not match the delivery identity")
	}
	_, err := gitlabMergeSquashValue(request.Method)
	return err
}

func (p *gitlabDeliveryProvider) Merge(ident domain.DeliveryIdentity, request DeliveryMergeRequest) error {
	if err := p.ValidateMergeRequest(ident, request); err != nil {
		return err
	}
	if p.client == nil {
		return fmt.Errorf("GitLab delivery capability is not composed")
	}
	glURL, err := domain.ParseMRURL(ident.URL)
	if err != nil {
		return fmt.Errorf("invalid MR URL in identity: %w", err)
	}
	return p.client.MergeMR(glURL.Host, glURL.Owner, glURL.Project, glURL.IID, request)
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
	baseRef, err := parseGLTargetBranch(data)
	if err != nil {
		return DeliveryProviderObservation{}, err
	}
	obs := DeliveryProviderObservation{
		State:     status.State,
		HeadSHA:   status.HeadSHA,
		MergedSHA: status.MergedSHA,
		BaseRef:   baseRef,
	}
	if status.State == "OPEN" {
		pr, mergeStatusOK, err := readGitLabOpenMR(p.client, glURL, data, status.HeadSHA)
		if err != nil {
			return DeliveryProviderObservation{}, err
		}
		if mergeStatusOK && pr.CanMerge() {
			obs.Mergeability = DeliveryMergeabilityAllowed
		} else {
			obs.Mergeability = DeliveryMergeabilityDenied
		}
	}
	return obs, nil
}

// readGitLabOpenMR reads every acceptance input of an open MR at headSHA:
// reviewer objections and head pipeline, as the domain.PR that
// domain.PR.CanMerge decides on. mergeStatusOK reports the separate detailed_merge_status "mergeable"
// fence, which guards merge conflicts and blocked states CanMerge does not
// model; when it is false the other inputs are not read.
func readGitLabOpenMR(client GitLabClient, glURL domain.GLURL, data []byte, headSHA string) (pr domain.PR, mergeStatusOK bool, err error) {
	mergeStatus, ok := parseGLDetailedMergeStatus(data)
	if !ok || mergeStatus != "mergeable" {
		return domain.PR{}, false, nil
	}
	pipeline, pipelineOK := parseGLPipeline(data)
	if !pipelineOK || pipeline.SHA != headSHA {
		return domain.PR{}, false, fmt.Errorf("GitLab MR observation is missing pipeline SHA evidence for the current head")
	}
	changesRequested, err := client.ChangesRequested(glURL.Host, glURL.Owner, glURL.Project, glURL.IID)
	if err != nil {
		return domain.PR{}, false, err
	}
	pr = domain.PR{
		Number: glURL.IID,
		Status: domain.PROpen,
		Checks: []domain.CheckRun{{Status: mapCheckStatus(pipeline.Status)}},
	}
	if changesRequested {
		pr.Reviews = []domain.Review{{State: domain.ReviewChangesRequested}}
	}
	return pr, true, nil
}

// parseGLTargetBranch reads the MR target branch (the GitLab name for the
// base ref) from the same MR JSON the merge status is parsed from. It is read
// here rather than in domain.PRMergeStatus because only the delivery
// observation fences on it.
func parseGLTargetBranch(data []byte) (string, error) {
	var raw struct {
		TargetBranch string `json:"target_branch"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("parsing glab mr view JSON: %w", err)
	}
	return raw.TargetBranch, nil
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

// ChangesRequested fetches the merge request's reviewers (every page) and
// reports whether any requested changes. Only "requested_changes" is a
// merge-blocking verdict; a reviewer's "approved" carries no weight.
func (c *glabClient) ChangesRequested(host, owner, project string, iid int) (bool, error) {
	path := fmt.Sprintf("/projects/%s/merge_requests/%d/reviewers", url.PathEscape(owner+"/"+project), iid)
	args := []string{"api", path, "--paginate"}
	if host != "" && host != "gitlab.com" {
		args = append(args, "--hostname", host)
	}
	data, err := c.runner.Run(args...)
	if err != nil {
		return false, err
	}
	var raw []struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return false, fmt.Errorf("parsing GitLab reviewer state: %w", err)
	}
	for _, r := range raw {
		if strings.ToLower(strings.TrimSpace(r.State)) == "requested_changes" {
			return true, nil
		}
	}
	return false, nil
}

// MergeMR invokes the GitLab merge endpoint through the typed glab api path.
// The source sha is an expected-head constraint evaluated by GitLab as part of
// the irreversible mutation; the target branch is checked immediately before
// this call by the delivery provider fence.
func (c *glabClient) MergeMR(host, owner, project string, iid int, request DeliveryMergeRequest) error {
	squash, err := gitlabMergeSquashValue(request.Method)
	if err != nil {
		return err
	}
	if request.HeadSHA == "" {
		return fmt.Errorf("GitLab merge requires a pinned head SHA")
	}
	path := fmt.Sprintf("/projects/%s/merge_requests/%d/merge", url.PathEscape(owner+"/"+project), iid)
	args := []string{
		"api", path,
		"--method", "PUT",
		"--field", "sha=" + request.HeadSHA,
		"--field", "squash=" + squash,
	}
	if host != "" && host != "gitlab.com" {
		args = append(args, "--hostname", host)
	}
	_, err = c.runner.Run(args...)
	return err
}

func parseGLDetailedMergeStatus(data []byte) (string, bool) {
	var raw struct {
		Status string `json:"detailed_merge_status"`
	}
	if json.Unmarshal(data, &raw) != nil || raw.Status == "" {
		return "", false
	}
	return raw.Status, true
}

type glPipelineEvidence struct {
	Status string
	SHA    string
}

func parseGLPipeline(data []byte) (glPipelineEvidence, bool) {
	var raw struct {
		Pipeline *struct {
			Status string `json:"status"`
			SHA    string `json:"sha"`
		} `json:"head_pipeline"`
	}
	if json.Unmarshal(data, &raw) != nil || raw.Pipeline == nil || raw.Pipeline.Status == "" || raw.Pipeline.SHA == "" {
		return glPipelineEvidence{}, false
	}
	return glPipelineEvidence{Status: raw.Pipeline.Status, SHA: raw.Pipeline.SHA}, true
}

func (c *glabClient) ViewMRJSON(host, owner, project string, iid int) ([]byte, error) {
	path := fmt.Sprintf("/projects/%s/merge_requests/%d", url.PathEscape(owner+"/"+project), iid)
	args := []string{"api", path}
	if host != "" && host != "gitlab.com" {
		args = append(args, "--hostname", host)
	}
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
