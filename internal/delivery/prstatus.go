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
// Uses `gh pr view --json state,merged,headRefOid,mergedSha` under the hood.
var QueryPRMergeStatus = func(ghURL ghurl.GHURL) (*PRMergeStatus, error) {
	args := []string{
		"pr", "view",
		fmt.Sprintf("%d", ghURL.Num),
		"--repo", fmt.Sprintf("%s/%s", ghURL.Owner, ghURL.Repo),
		"--json", "state,merged,headRefOid,mergedSha",
	}
	cmd := exec.Command("gh", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh pr view: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh pr view: %w", err)
	}

	var status PRMergeStatus
	if err := json.Unmarshal(out, &status); err != nil {
		return nil, fmt.Errorf("parsing gh pr view output: %w", err)
	}

	switch status.State {
	case "OPEN":
		status.Closed = false
	case "MERGED":
		status.Closed = false
		status.Merged = true
	case "CLOSED":
		status.Closed = true
		status.Merged = false
	}

	return &status, nil
}
