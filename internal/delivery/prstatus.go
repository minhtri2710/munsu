package delivery

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/minhtri2710/munsu/internal/ghurl"
)

// PRMergeStatus holds the provider-confirmed merge state of a pull request.
type PRMergeStatus struct {
	Merged    bool   `json:"merged"`
	MergedSHA string `json:"mergedSha,omitempty"`
	Closed    bool   `json:"closed"`
	HeadSHA   string `json:"headRefOid"`
	State     string `json:"state"` // OPEN, MERGED, CLOSED
}

// QueryPRMergeStatus fetches the current merge status of a PR from the provider
// (e.g. GitHub). It is the minimal provider query seam used by teardown and
// other lifecycle checks that need to distinguish merged branches from merely
// pushed ones.
//
// When the provider is unreachable or ambiguous, it returns an error so callers
// can fail closed.
//
// Uses gh-axi via the consolidated GitHubClient when the capability is Ready.
// Falls through to gh CLI through the adapter path for fields that gh-axi
// does not expose directly.
var QueryPRMergeStatus = func(ghURL ghurl.GHURL) (*PRMergeStatus, error) {
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

// queryPRMergeStatusDirect uses raw gh CLI to query PR merge status.
// This is the degraded path when gh-axi is not available.
// QueryPRMergeStatus prefers the consolidated gh-axi path first.
func queryPRMergeStatusDirect(ghURL ghurl.GHURL) (*PRMergeStatus, error) {
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
func parsePRMergeStatus(data []byte) (*PRMergeStatus, error) {
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

	status := &PRMergeStatus{
		State:   raw.State,
		HeadSHA: raw.HeadRefOid,
	}
	if raw.MergeCommit != nil {
		status.MergedSHA = raw.MergeCommit.Oid
	}
	switch status.State {
	case "OPEN":
		status.Closed = false
		status.Merged = false
	case "MERGED":
		status.Closed = false
		status.Merged = true
		// Prefer merge commit oid as merged SHA when present.
		if status.MergedSHA == "" {
			status.MergedSHA = status.HeadSHA
		}
	case "CLOSED":
		status.Closed = true
		status.Merged = false
	default:
		// leave flags false; callers treat unexpected carefully
	}

	return status, nil
}
