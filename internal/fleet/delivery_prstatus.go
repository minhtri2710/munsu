package fleet

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// QueryDeliveryMergeStatus fetches the merge status of a delivery identity from
// the provider its task's captured forge step names. Reads go through the
// step's resolved client; a forge that is not Ready, or that does not match the
// identity's provider, fails closed and no other provider is consulted.
var QueryDeliveryMergeStatus = func(forge taskauthority.DeliveryStep, ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
	if ident == nil {
		return nil, fmt.Errorf("delivery identity is nil")
	}
	client, err := forgeClientForIdentity(forge, *ident)
	if err != nil {
		return nil, err
	}
	if client.github != nil {
		ghURL, err := domain.ParseGHURL(ident.URL)
		if err != nil {
			return nil, fmt.Errorf("invalid GitHub URL in identity: %w", err)
		}
		data, err := client.github.ViewPRJSON(ghURL.Owner, ghURL.Repo, ghURL.Num, "state,headRefOid,mergeCommit")
		if err != nil {
			return nil, err
		}
		return parsePRMergeStatus(data)
	}
	return fetchGLMergeStatus(client.gitlab, ident)
}

// landing is the answer to "did this delivery land", one per provider read.
type landing int

const (
	landingUnknown landing = iota
	landingLanded
	landingOpen
	landingClosed
)

// classifyLanding owns "the delivery landed": Deliver's outcome, teardown's
// merge proof and merge-status all decide through it. Landed needs the
// provider to report merged with merge-commit evidence and the exact head the
// delivery identity pins; merged evidence missing either is unknown, and the
// error says why.
func classifyLanding(status domain.PRMergeStatus, ident domain.DeliveryIdentity) (landing, error) {
	switch {
	case status.Merged:
		if status.HeadSHA == "" {
			return landingUnknown, fmt.Errorf("provider returned empty head SHA for merged PR #%d", ident.Number)
		}
		if !validGitObjectID(status.MergedSHA) {
			return landingUnknown, fmt.Errorf("provider returned no merge-result evidence for merged PR #%d", ident.Number)
		}
		if status.HeadSHA != ident.HeadSHA {
			return landingUnknown, fmt.Errorf("PR head SHA mismatch: stored %s, provider reports %s; the worktree branch may have moved", ident.HeadSHA, status.HeadSHA)
		}
		return landingLanded, nil
	case status.Closed:
		return landingClosed, nil
	case status.State == "OPEN":
		return landingOpen, nil
	}
	return landingUnknown, fmt.Errorf("PR #%d is in an unexpected state %q: merged=%v closed=%v", ident.Number, status.State, status.Merged, status.Closed)
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

// parsePRMergeStatus parses the PR merge status from gh CLI JSON output.
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
