package fleet

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
)

// QueryPRMergeStatus fetches the current merge status of a PR from the GitHub
// provider. It is the minimal provider query seam used by teardown and
// other lifecycle checks that need to distinguish merged branches from merely
// pushed ones.
//
// When the provider is unreachable or ambiguous, it returns an error so callers
// can fail closed.
//
// Uses gh-axi via the consolidated GitHubClient when the capability is Ready.
// Falls through to gh CLI through the adapter path for fields that gh-axi
// does not expose directly.
var QueryPRMergeStatus = func(ghURL domain.GHURL) (*domain.PRMergeStatus, error) {
	// Check gh-axi capability first
	client, err := DefaultGitHubClient()
	if err == nil {
		// gh-axi is Ready; use the consolidated adapter
		data, err := client.ViewPRJSON(ghURL.Owner, ghURL.Repo, ghURL.Num, "state,headRefOid,mergeCommit")
		if err != nil {
			return nil, err
		}
		return parsePRMergeStatus(data)
	}

	// If gh-axi is not available, try direct gh CLI as fallback for
	// read-only status queries. The capability model controls mutation
	// authority; status reads tolerate degraded paths.
	return queryPRMergeStatusDirect(ghURL)
}

// QueryDeliveryMergeStatus fetches the merge status from the appropriate
// provider based on the delivery identity. Routes to existing QueryPRMergeStatus
// for GitHub PRs, and to the GitLab status path for GitLab MRs.
// Fail-closed on unrecognized provider or when GitLab capability is Failed.
// Falls back to read-only status queries when the provider is Absent or
// Unsupported and the identity allows it.
var QueryDeliveryMergeStatus = func(ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
	if ident == nil {
		return nil, fmt.Errorf("delivery identity is nil")
	}

	switch ident.Provider {
	case "github":
		ghURL, err := domain.ParseGHURL(ident.URL)
		if err != nil {
			return nil, fmt.Errorf("invalid GitHub URL in identity: %w", err)
		}
		return QueryPRMergeStatus(ghURL)
	case "gitlab":
		return queryGLMergeStatus(ident)
	default:
		return nil, fmt.Errorf("unknown provider %q in delivery identity", ident.Provider)
	}
}

// queryGLMergeStatus queries GitLab MR merge status via the typed GitLabClient.
// Fail-closed on Failed; Absent/Unsupported may fall through.
// Returns a domain.PRMergeStatus normalized from GitLab's state model.
func queryGLMergeStatus(ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
	return queryGLMergeStatusForState(ProbeGitLabCapability(), ident)
}

func queryGLMergeStatusForState(state backend.State, ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
	switch state {
	case backend.Ready:
		// Use the typed GitLabClient
		client, err := GitLabClientForState(state)
		if err != nil {
			return nil, fmt.Errorf("GitLab provider: %w", err)
		}
		return fetchGLMergeStatus(client, ident)
	case backend.Failed:
		return nil, fmt.Errorf("GitLab capability failed: cannot query MR status (use --force to override)")
	case backend.Absent, backend.Unsupported:
		// Read-only status; permitted fallback if one is configured.
		if defaultGlabFallback != nil {
			return defaultGlabFallback(ident)
		}
		return nil, fmt.Errorf("GitLab provider not available for MR status query (use --force to override)")
	default:
		return nil, fmt.Errorf("GitLab capability in unknown state: %v", state)
	}
}

// fetchGLMergeStatus queries the GitLab MR status via the typed client.
func fetchGLMergeStatus(client GitLabClient, ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
	// For GitLab MR JSON, we need host and project name separately.
	// The identity stores Repo as the project name and Owner as the namespace.
	// Host is not stored in the identity; we derive it from the URL.
	glURL, err := domain.ParseMRURL(ident.URL)
	if err != nil {
		return nil, fmt.Errorf("parsing GitLab URL from identity: %w", err)
	}
	data, err := client.ViewMRJSON(glURL.Host, glURL.Owner, glURL.Project, glURL.IID)
	if err != nil {
		return nil, err
	}
	return parseGLMergeStatus(data)
}

// parseGLMergeStatus parses GitLab MR JSON into the common domain.PRMergeStatus.
// GitLab JSON uses snake_case: state, sha, merge_commit (diff_merge_commit).
func parseGLMergeStatus(data []byte) (*domain.PRMergeStatus, error) {
	var raw struct {
		State          string `json:"state"`            // opened, merged, closed
		SHA            string `json:"sha"`              // diff head SHA
		MergeCommitSHA string `json:"merge_commit_sha"` // flat string, null until merged
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing glab mr view JSON: %w", err)
	}

	// Fail closed on empty or unknown state
	if raw.State == "" {
		return nil, fmt.Errorf("glab mr view returned empty state")
	}

	// Fail closed on empty head SHA
	if raw.SHA == "" {
		return nil, fmt.Errorf("glab mr view returned empty sha")
	}

	normalizedState := normalizeGlabState(raw.State)

	mergedSHA := strings.TrimSpace(raw.MergeCommitSHA)
	if normalizedState == "MERGED" && !validGitObjectID(mergedSHA) {
		return nil, fmt.Errorf("glab mr view returned missing merge commit sha")
	}
	status := &domain.PRMergeStatus{
		State:   normalizedState,
		HeadSHA: raw.SHA,
	}
	if validGitObjectID(mergedSHA) {
		status.MergedSHA = mergedSHA
	}

	switch normalizedState {
	case "OPEN":
		status.Closed = false
		status.Merged = false
	case "MERGED":
		status.Closed = false
		status.Merged = true
	case "CLOSED":
		status.Closed = true
		status.Merged = false
	default:
		// leave flags false
	}

	return status, nil
}

// queryPRMergeStatusDirect uses raw gh CLI to query PR merge status.
// This is the degraded path when gh-axi is not available.
// QueryPRMergeStatus prefers the consolidated gh-axi path first.
func queryPRMergeStatusDirect(ghURL domain.GHURL) (*domain.PRMergeStatus, error) {
	args := []string{
		"pr", "view",
		fmt.Sprintf("%d", ghURL.Num),
		"--repo", fmt.Sprintf("%s/%s", ghURL.Owner, ghURL.Repo),
		"--json", "state,headRefOid,mergeCommit",
	}
	cmd := exec.Command("gh", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh pr view: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh pr view: %w", err)
	}
	return parsePRMergeStatus(out)
}

// parsePRMergeStatus parses the PR merge status from gh CLI JSON output.
// Shared between the consolidated gh-axi path and the degraded direct path.
func parsePRMergeStatus(data []byte) (*domain.PRMergeStatus, error) {
	var raw struct {
		State       string `json:"state"`
		HeadRefOid  string `json:"headRefOid"`
		MergeCommit *struct {
			Oid string `json:"oid"`
		} `json:"mergeCommit"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing gh pr view output: %w", err)
	}

	status := &domain.PRMergeStatus{
		State:   raw.State,
		HeadSHA: raw.HeadRefOid,
	}
	if status.State == "MERGED" {
		if raw.MergeCommit == nil || !validGitObjectID(raw.MergeCommit.Oid) {
			return nil, fmt.Errorf("gh pr view returned missing or invalid merge commit OID")
		}
		status.MergedSHA = raw.MergeCommit.Oid
	}
	switch status.State {
	case "OPEN":
		status.Closed = false
		status.Merged = false
	case "MERGED":
		status.Closed = false
		status.Merged = true
	case "CLOSED":
		status.Closed = true
		status.Merged = false
	default:
		// leave flags false; callers treat unexpected carefully
	}

	return status, nil
}
