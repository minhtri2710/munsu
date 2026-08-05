// Package delivery implements delivery operations.
package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	Provider   string `json:"provider"`
	Owner      string `json:"owner"`
	Repo       string `json:"repo"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
	BaseRef    string `json:"baseRef"`
	HeadRef    string `json:"headRef"`
	HeadSHA    string `json:"headSHA"`
	State      string `json:"state"` // OPEN, MERGED, CLOSED
	Merged     bool   `json:"merged"`
	MergedSHA  string `json:"mergedSHA,omitempty"` // merge commit SHA (nonempty only when merged)
	ObservedAt string `json:"observedAt"`          // ISO 8601
}

// AmendRecord is a single audit record for an identity amendment.
// Stored as a JSON array in the meta field `amendment_history`.
type AmendRecord struct {
	OldHeadSHA       string `json:"oldHeadSHA"`
	NewHeadSHA       string `json:"newHeadSHA"`
	PRIdentity       string `json:"prIdentity"`       // e.g. "github/minhtri2710/munsu#42"
	ProviderEvidence string `json:"providerEvidence"` // provider-reported head at amendment time
	Timestamp        string `json:"timestamp"`
	Reason           string `json:"reason"` // e.g. "amendment", "reconciliation"
}

// Meta field keys for the delivery lifecycle projection.
const (
	MetaDeliveryState     = "delivery_state"
	MetaIdentityRevision  = "pr_identity_revision"
	MetaAmendExpectedHead = "amend_expected_head"
	MetaAmendStartedAt    = "amend_started_at"
	MetaAmendHistory      = "amendment_history"
	// MetaLegacyMergeAuth is the legacy .meta projection key of the
	// retired merge authorization record. The canonical read path treats a
	// meta-only value as legacy evidence that never authorizes delivery.
	MetaLegacyMergeAuth = "merge_authorization"
)

// FetchProviderSnapshot queries the provider for a point-in-time snapshot of a
// PR/MR through the typed provider clients. Read-only; fail-closed on
// provider absence or ambiguous state.
var FetchProviderSnapshot = fetchProviderSnapshotImpl

func fetchProviderSnapshotImpl(prURL string) (*ProviderSnapshot, error) {
	provider, _, _, _, _, err := ParseProviderURL(prURL)
	if err != nil {
		return nil, fmt.Errorf("unrecognized PR/MR URL: %w", err)
	}

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

	data, err := client.ViewPRJSON(ghURL.Owner, ghURL.Repo, ghURL.Num, "state,headRefOid,headRefName,baseRefName,mergeCommit")
	if err != nil {
		return nil, err
	}

	var raw struct {
		State       string `json:"state"`
		HeadRefOid  string `json:"headRefOid"`
		HeadRefName string `json:"headRefName"`
		BaseRefName string `json:"baseRefName"`
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
		State:      raw.State,
		ObservedAt: time.Now().UTC().Format(time.RFC3339),
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

	switch normalizedState {
	case "MERGED":
		snap.Merged = true
		if raw.MergeCommitSHA != "" {
			snap.MergedSHA = raw.MergeCommitSHA
		}
	}

	return snap, nil
}

// verifySnapshotIdentity checks that a stored identity matches the provider
// snapshot on all critical fields: provider, owner, repo, number, base ref,
// and head ref. Branch replacement is rejected.
func verifySnapshotIdentity(stored *domain.DeliveryIdentity, snap *ProviderSnapshot) error {
	if stored.Provider != snap.Provider {
		return fmt.Errorf("provider mismatch: stored=%q snapshot=%q", stored.Provider, snap.Provider)
	}
	if stored.Owner != snap.Owner {
		return fmt.Errorf("owner mismatch: stored=%q snapshot=%q", stored.Owner, snap.Owner)
	}
	if stored.Repo != snap.Repo {
		return fmt.Errorf("repo mismatch: stored=%q snapshot=%q", stored.Repo, snap.Repo)
	}
	if stored.Number != snap.Number {
		return fmt.Errorf("PR number mismatch: stored=%d snapshot=%d", stored.Number, snap.Number)
	}
	// URL identity must match (canonical comparison)
	if stored.URL != snap.URL {
		return fmt.Errorf("URL mismatch: stored=%q snapshot=%q", stored.URL, snap.URL)
	}
	if stored.BaseRef != snap.BaseRef {
		return fmt.Errorf("base ref mismatch: stored=%q snapshot=%q", stored.BaseRef, snap.BaseRef)
	}
	if stored.HeadRef != snap.HeadRef {
		return fmt.Errorf("head ref mismatch: stored=%q snapshot=%q (branch replacement not allowed)", stored.HeadRef, snap.HeadRef)
	}
	return nil
}

// verifyAncestry checks that oldSHA is an ancestor of newSHA in the given
// git repository. Returns nil if oldSHA is ancestor of newSHA.
// Rejects force-push/rewritten ancestry.
func verifyAncestry(repoPath, oldSHA, newSHA string) error {
	if oldSHA == "" || newSHA == "" {
		return fmt.Errorf("empty SHA in ancestry check")
	}
	if oldSHA == newSHA {
		return fmt.Errorf("SHAs are identical, no movement")
	}
	// Ensure the worktree exists
	if _, err := os.Stat(repoPath); err != nil {
		return fmt.Errorf("worktree %s does not exist: %w", repoPath, err)
	}
	// Check if oldSHA exists in the repo
	catCmd := exec.Command("git", "cat-file", "-e", oldSHA)
	catCmd.Dir = repoPath
	if err := catCmd.Run(); err != nil {
		return fmt.Errorf("old SHA %q not found in local repo (may have been garbage-collected or force-pushed)", oldSHA)
	}
	// Check ancestry: `git merge-base --is-ancestor <old> <new>`
	cmd := exec.Command("git", "merge-base", "--is-ancestor", oldSHA, newSHA)
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return fmt.Errorf("old SHA %q is not an ancestor of new SHA %q (force-push or rewritten ancestry)", oldSHA[:minInt(12, len(oldSHA))], newSHA[:minInt(12, len(newSHA))])
		}
		return fmt.Errorf("git merge-base --is-ancestor: %w", err)
	}
	return nil
}

// appendAmendHistory appends an AmendRecord to the existing history JSON.
// The history is stored as a JSON array. Returns the serialized array.
func appendAmendHistory(existing string, record *AmendRecord) string {
	var history []*AmendRecord
	if existing != "" {
		json.Unmarshal([]byte(existing), &history)
	}
	history = append(history, record)
	data, _ := json.Marshal(history)
	return string(data)
}

// incrementRevision increments a revision string. The revision is a
// non-negative integer. Returns "1" if the input is empty or invalid.
func incrementRevision(rev string) string {
	var n int
	for _, c := range rev {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else {
			break
		}
	}
	return fmt.Sprintf("%d", n+1)
}
