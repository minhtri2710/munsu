// Package delivery implements delivery operations.
package fleet

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
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

func validGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.Trim(value, "0") != ""
}

func mapCheckStatus(value string) domain.CheckStatus {
	switch strings.ToLower(value) {
	case "success", "passed":
		return domain.CheckPassed
	case "failure", "failed", "error", "canceled", "cancelled":
		return domain.CheckFailed
	case "skipped":
		return domain.CheckSkipped
	default:
		return domain.CheckPending
	}
}

// FetchProviderSnapshot queries the provider for a point-in-time snapshot of a
// PR/MR through the client its captured forge step resolves to. Read-only;
// fail-closed on provider absence or ambiguous state.
var FetchProviderSnapshot = fetchProviderSnapshotImpl

func fetchProviderSnapshotImpl(forge taskauthority.DeliveryStep, prURL string) (*ProviderSnapshot, error) {
	provider, _, _, _, _, err := domain.ParseProviderURL(prURL)
	if err != nil {
		return nil, fmt.Errorf("unrecognized PR/MR URL: %w", err)
	}
	return fetchProviderSnapshotForProvider(forge, provider, prURL)
}

func fetchProviderSnapshotForProvider(forge taskauthority.DeliveryStep, provider, prURL string) (*ProviderSnapshot, error) {
	switch provider {
	case "github":
		return fetchGitHubProviderSnapshot(forge, prURL)
	case "gitlab":
		return fetchGitLabProviderSnapshot(forge, prURL)
	default:
		return nil, fmt.Errorf("unknown provider %q for URL %s", provider, prURL)
	}
}

func fetchGitHubProviderSnapshot(forge taskauthority.DeliveryStep, prURL string) (*ProviderSnapshot, error) {
	ghURL, err := domain.ParseGHURL(prURL)
	if err != nil {
		return nil, fmt.Errorf("invalid GitHub URL: %w", err)
	}

	resolved, err := forgeClientForIdentity(forge, domain.DeliveryIdentity{Provider: "github"})
	if err != nil {
		return nil, fmt.Errorf("GitHub provider not available: %w", err)
	}
	client := resolved.github
	view, err := readGitHubPRView(client, ghURL)
	if err != nil {
		return nil, err
	}

	// Fail closed on empty critical fields
	if view.State == "" {
		return nil, fmt.Errorf("gh pr view returned empty state")
	}
	if view.HeadRefOid == "" {
		return nil, fmt.Errorf("gh pr view returned empty headRefOid")
	}
	if view.HeadRefName == "" || view.BaseRefName == "" {
		return nil, fmt.Errorf("gh pr view returned empty headRefName or baseRefName")
	}

	snap := &ProviderSnapshot{
		Provider:   "github",
		Owner:      ghURL.Owner,
		Repo:       ghURL.Repo,
		Number:     ghURL.Num,
		URL:        ghURL.FullURL(),
		BaseRef:    view.BaseRefName,
		HeadRef:    view.HeadRefName,
		HeadSHA:    view.HeadRefOid,
		State:      view.State,
		ObservedAt: time.Now().UTC().Format(time.RFC3339),
	}
	switch snap.State {
	case "MERGED":
		if view.MergeCommit == nil || !validGitObjectID(view.MergeCommit.Oid) {
			return nil, fmt.Errorf("gh pr view returned missing merge commit OID")
		}
		snap.Merged = true
		snap.MergedSHA = view.MergeCommit.Oid
	case "CLOSED":
	case "OPEN":
		pr, mergeableOK, err := readGitHubOpenPR(client, ghURL, view)
		if err != nil {
			return nil, err
		}
		if !mergeableOK {
			return nil, fmt.Errorf("GitHub PR is not mergeable (mergeable %q, mergeStateStatus %q)", view.Mergeable, view.MergeStateStatus)
		}
		snap.Checks = pr.Checks
		snap.Reviews = pr.Reviews
	default:
		return nil, fmt.Errorf("gh pr view returned unrecognized state %q", view.State)
	}

	return snap, nil
}

func fetchGitLabProviderSnapshot(forge taskauthority.DeliveryStep, mrURL string) (*ProviderSnapshot, error) {
	glURL, err := domain.ParseMRURL(mrURL)
	if err != nil {
		return nil, fmt.Errorf("invalid MR URL: %w", err)
	}

	resolved, err := forgeClientForIdentity(forge, domain.DeliveryIdentity{Provider: "gitlab"})
	if err != nil {
		return nil, fmt.Errorf("GitLab provider not available: %w", err)
	}
	client := resolved.gitlab

	// Single query: parse identity and status from one ViewMRJSON call
	data, err := client.ViewMRJSON(glURL.Host, glURL.Owner, glURL.Project, glURL.IID)
	if err != nil {
		return nil, err
	}

	status, err := parseGLMergeStatus(data)
	if err != nil {
		return nil, err
	}
	baseRef, err := parseGLTargetBranch(data)
	if err != nil {
		return nil, err
	}
	var raw struct {
		SourceBranch string `json:"source_branch"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing glab mr view JSON: %w", err)
	}
	if raw.SourceBranch == "" || baseRef == "" {
		return nil, fmt.Errorf("glab mr view returned empty source_branch or target_branch")
	}

	snap := &ProviderSnapshot{
		Provider:   "gitlab",
		Owner:      glURL.Owner,
		Repo:       glURL.Project,
		Number:     glURL.IID,
		URL:        mrURL,
		BaseRef:    baseRef,
		HeadRef:    raw.SourceBranch,
		HeadSHA:    status.HeadSHA,
		State:      status.State,
		ObservedAt: time.Now().UTC().Format(time.RFC3339),
	}
	switch status.State {
	case "MERGED":
		snap.Merged = true
		snap.MergedSHA = status.MergedSHA
	case "CLOSED":
	case "OPEN":
		pr, mergeStatusOK, err := readGitLabOpenMR(client, glURL, data, status.HeadSHA)
		if err != nil {
			return nil, err
		}
		if !mergeStatusOK {
			return nil, fmt.Errorf("GitLab MR is not mergeable")
		}
		snap.Checks = pr.Checks
		snap.Reviews = pr.Reviews
	default:
		return nil, fmt.Errorf("glab mr view returned unrecognized state %q", status.State)
	}

	return snap, nil
}
