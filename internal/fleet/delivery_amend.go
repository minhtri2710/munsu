// Package delivery implements delivery operations.
package fleet

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
)

// This file retains the read-only delivery vocabulary and provider snapshot
// helpers. The amendment mutation lifecycle was removed with the legacy
// delivery path (Packet #414 B); delivery execution now runs exclusively
// through the journaled Deliver operation. No delivery mutation semantics
// remain here.

// DeliveryState represents the lifecycle state of a delivery projection.
// The projection is display-only; canonical delivery truth lives in the
// Task Authority delivery authorization/outcome evidence.
type DeliveryState string

const (
	// DeliveryStateReviewReady: PR/MR is open, identity captured, awaiting review.
	DeliveryStateReviewReady DeliveryState = "review-ready"
	// DeliveryStateAmending: amendment in progress, identity is being refreshed.
	DeliveryStateAmending DeliveryState = "amending"
	// DeliveryStateMerged: PR/MR is merged, awaiting parent verification.
	DeliveryStateMerged DeliveryState = "merged"
	// DeliveryStateRemoteUnknown: provider result was ambiguous or unreachable;
	// the same mutation attempt must never be repeated. Operator attention required.
	DeliveryStateRemoteUnknown DeliveryState = "remote-unknown"
	// DeliveryStateDelivered: parent-verified delivery complete.
	DeliveryStateDelivered DeliveryState = "delivered"
)

// ProviderSnapshot captures a single point-in-time view of a PR/MR from the
// provider. It is the read-only snapshot seam used by the retained helper
// reads; the journaled delivery path uses the typed DeliveryProvider
// observation instead.
type ProviderSnapshot struct {
	Provider   string            `json:"provider"`
	Owner      string            `json:"owner"`
	Repo       string            `json:"repo"`
	Number     int               `json:"number"`
	URL        string            `json:"url"`
	BaseRef    string            `json:"baseRef"`
	HeadRef    string            `json:"headRef"`
	HeadSHA    string            `json:"headSHA"`
	State      string            `json:"state"` // OPEN, MERGED, CLOSED
	Checks     []domain.CheckRun `json:"checks,omitempty"`
	Reviews    []domain.Review   `json:"reviews,omitempty"`
	Merged     bool              `json:"merged"`
	MergedSHA  string            `json:"mergedSHA,omitempty"` // merge commit SHA (nonempty only when merged)
	ObservedAt string            `json:"observedAt"`          // ISO 8601
}

// Mergeable reports whether the provider snapshot satisfies the domain delivery
// acceptance rule before a delivery request is journaled.
func (s ProviderSnapshot) Mergeable() bool {
	checks := make([]domain.CheckRun, len(s.Checks))
	copy(checks, s.Checks)
	return domain.PR{
		Number:  s.Number,
		Status:  domain.PRStatus(strings.ToLower(s.State)),
		Checks:  checks,
		Reviews: s.Reviews,
	}.CanMerge()
}

// MetaDeliveryState is the meta field key for the delivery lifecycle projection.
const MetaDeliveryState = "delivery_state"

// FetchProviderSnapshot queries the provider for a point-in-time snapshot of a
// PR/MR through the typed provider clients. Read-only; fail-closed on
// provider absence or ambiguous state.
var FetchProviderSnapshot = fetchProviderSnapshotImpl

func fetchProviderSnapshotImpl(prURL string) (*ProviderSnapshot, error) {
	provider, _, _, _, _, err := ParseProviderURL(prURL)
	if err != nil {
		return nil, fmt.Errorf("unrecognized PR/MR URL: %w", err)
	}
	return fetchProviderSnapshotForProvider(provider, prURL)
}

func fetchProviderSnapshotForProvider(provider, prURL string) (*ProviderSnapshot, error) {
	switch provider {
	case "github":
		return fetchGitHubProviderSnapshot(prURL)
	case "gitlab":
		return fetchGitLabProviderSnapshot(prURL)
	default:
		return nil, fmt.Errorf("unknown provider %q for URL %s", provider, prURL)
	}
}

func fetchGitHubProviderSnapshot(prURL string) (*ProviderSnapshot, error) {
	ghURL, err := domain.ParseGHURL(prURL)
	if err != nil {
		return nil, fmt.Errorf("invalid GitHub URL: %w", err)
	}

	client, err := DefaultGitHubClient()
	if err != nil {
		return nil, fmt.Errorf("GitHub provider not available: %w", err)
	}

	data, err := client.ViewPRJSON(ghURL.Owner, ghURL.Repo, ghURL.Num, "state,headRefOid,headRefName,baseRefName,mergeCommit,statusCheckRollup,reviews")
	if err != nil {
		return nil, err
	}

	var raw struct {
		State             string `json:"state"`
		HeadRefOid        string `json:"headRefOid"`
		HeadRefName       string `json:"headRefName"`
		BaseRefName       string `json:"baseRefName"`
		StatusCheckRollup []struct {
			State      string `json:"state"`
			Conclusion string `json:"conclusion"`
			Status     string `json:"status"`
		} `json:"statusCheckRollup"`
		Reviews []struct {
			State string `json:"state"`
		} `json:"reviews"`
		MergeCommit *struct {
			Oid string `json:"oid"`
		} `json:"mergeCommit"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing gh pr view output: %w", err)
	}

	// Fail closed on empty critical fields
	if raw.State == "" {
		return nil, fmt.Errorf("gh pr view returned empty state")
	}
	if raw.HeadRefOid == "" {
		return nil, fmt.Errorf("gh pr view returned empty headRefOid")
	}
	if raw.HeadRefName == "" || raw.BaseRefName == "" {
		return nil, fmt.Errorf("gh pr view returned empty headRefName or baseRefName")
	}

	snap := &ProviderSnapshot{
		Provider:   "github",
		Owner:      ghURL.Owner,
		Repo:       ghURL.Repo,
		Number:     ghURL.Num,
		URL:        ghURL.FullURL(),
		BaseRef:    raw.BaseRefName,
		HeadRef:    raw.HeadRefName,
		HeadSHA:    raw.HeadRefOid,
		State:      strings.ToUpper(raw.State),
		ObservedAt: time.Now().UTC().Format(time.RFC3339),
	}
	for _, check := range raw.StatusCheckRollup {
		status := strings.ToLower(check.Conclusion)
		if status == "" {
			status = strings.ToLower(check.State)
		}
		mapped := domain.CheckPending
		switch status {
		case "success", "passed":
			mapped = domain.CheckPassed
		case "failure", "failed", "error":
			mapped = domain.CheckFailed
		case "skipped":
			mapped = domain.CheckSkipped
		}
		snap.Checks = append(snap.Checks, domain.CheckRun{Status: mapped})
	}
	for _, review := range raw.Reviews {
		snap.Reviews = append(snap.Reviews, domain.Review{State: domain.ReviewState(strings.ToLower(review.State))})
	}

	switch raw.State {
	case "MERGED":
		snap.Merged = true
		if raw.MergeCommit != nil && raw.MergeCommit.Oid != "" {
			snap.MergedSHA = raw.MergeCommit.Oid
		}
	}

	return snap, nil
}

func fetchGitLabProviderSnapshot(mrURL string) (*ProviderSnapshot, error) {
	glURL, err := domain.ParseMRURL(mrURL)
	if err != nil {
		return nil, fmt.Errorf("invalid MR URL: %w", err)
	}

	client, err := DefaultGitLabClient()
	if err != nil {
		return nil, fmt.Errorf("GitLab provider not available: %w", err)
	}

	// Single query: parse identity and status from one ViewMRJSON call
	data, err := client.ViewMRJSON(glURL.Host, glURL.Owner, glURL.Project, glURL.IID)
	if err != nil {
		return nil, err
	}

	var raw struct {
		State          string `json:"state"`
		SHA            string `json:"sha"`
		SourceBranch   string `json:"source_branch"`
		TargetBranch   string `json:"target_branch"`
		Approved       bool   `json:"approved"`
		PipelineStatus string `json:"pipeline_status"`
		MergeCommitSHA string `json:"merge_commit_sha"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing glab mr view JSON: %w", err)
	}

	if raw.State == "" || raw.SHA == "" || raw.SourceBranch == "" || raw.TargetBranch == "" {
		return nil, fmt.Errorf("glab mr view returned empty state, sha, source_branch, or target_branch")
	}

	normalizedState := normalizeGlabState(raw.State)

	snap := &ProviderSnapshot{
		Provider:   "gitlab",
		Owner:      glURL.Owner,
		Repo:       glURL.Project,
		Number:     glURL.IID,
		URL:        mrURL,
		BaseRef:    raw.TargetBranch,
		HeadRef:    raw.SourceBranch,
		HeadSHA:    raw.SHA,
		State:      normalizedState,
		ObservedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if raw.PipelineStatus != "" {
		status := domain.CheckPending
		switch strings.ToLower(raw.PipelineStatus) {
		case "success", "passed":
			status = domain.CheckPassed
		case "failed", "failure", "error":
			status = domain.CheckFailed
		case "skipped":
			status = domain.CheckSkipped
		}
		snap.Checks = []domain.CheckRun{{Status: status}}
	}
	if raw.Approved {
		snap.Reviews = []domain.Review{{State: domain.ReviewApproved}}
	}

	switch normalizedState {
	case "MERGED":
		snap.Merged = true
		if raw.MergeCommitSHA != "" {
			snap.MergedSHA = raw.MergeCommitSHA
		}
	}

	return snap, nil
}
