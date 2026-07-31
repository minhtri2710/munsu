// Package delivery implements delivery operations.
package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// DeliveryState represents the lifecycle state of a
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
	// Set by PrepareDelivery after verifying provider identity, immutable head,
	// and terminal green required checks. This is the terminal lifecycle state.
	DeliveryStateDelivered DeliveryState = "delivered"
)

// ProviderSnapshot captures a single point-in-time view of a PR/MR from the
// provider. It is the single-query seam used by both amendment verification
// and reconciliation, eliminating races between separate capture/status queries.
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

// Meta field keys for amendment lifecycle.
const (
	MetaDeliveryState     = "delivery_state"
	MetaIdentityRevision  = "pr_identity_revision"
	MetaAmendExpectedHead = "amend_expected_head"
	MetaAmendStartedAt    = "amend_started_at"
	MetaAmendHistory      = "amendment_history"
)

// PrMetaFields returns the canonical identity meta key names used for CAS.
func PrMetaFields() []string {
	return []string{
		"pr_provider", "pr_owner", "pr_repo", "pr_number", "pr_url",
		"pr_base_ref", "pr_head_ref", "pr_head_sha", "pr_timestamp",
	}
}

// identityChecks builds a CAS check map from a domain.DeliveryIdentity.
func identityChecks(id *domain.DeliveryIdentity) map[string]string {
	return map[string]string{
		"pr_provider": id.Provider,
		"pr_owner":    id.Owner,
		"pr_repo":     id.Repo,
		"pr_number":   fmt.Sprintf("%d", id.Number),
		"pr_url":      id.URL,
		"pr_base_ref": id.BaseRef,
		"pr_head_ref": id.HeadRef,
		"pr_head_sha": id.HeadSHA,
	}
}

// identityUpdates builds an update map from a domain.DeliveryIdentity.
func identityUpdates(id *domain.DeliveryIdentity) map[string]string {
	m := id.ToMeta()
	m[MetaDeliveryState] = string(DeliveryStateReviewReady)
	return m
}

// FetchProviderSnapshot queries the provider for a point-in-time snapshot of a
// PR/MR. This is the single-query seam that replaces separate CaptureIdentity
// and QueryDeliveryMergeStatus calls, eliminating race conditions.
// Fail-closed on provider absence or ambiguous state.
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

// --- Amendment lifecycle ---

// BeginAmendment transitions a delivery from review-ready to amending.
// It CAS-checks the stored identity (provider, repo, PR, head SHA), revision,
// and delivery_state == review-ready, then sets delivery_state = amending and
// records the amendment intent (expected head, started timestamp, revision).
//
// Returns the updated meta on success. Fail-closed if state is not review-ready
// or if stored identity doesn't match the expected values.
func BeginAmendment(homeDir, taskID string) (map[string]string, error) {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("begin amendment: reading meta: %w", err)
	}

	currentState := meta[MetaDeliveryState]
	if currentState != "" && currentState != string(DeliveryStateReviewReady) {
		return nil, fmt.Errorf("begin amendment: cannot amend from state %q (expected %q)", currentState, DeliveryStateReviewReady)
	}

	// Build the stored identity from meta
	ident, err := domain.IdentityFromMeta(meta)
	if err != nil {
		return nil, fmt.Errorf("begin amendment: reading delivery identity: %w", err)
	}
	if ident == nil {
		return nil, fmt.Errorf("begin amendment: no delivery identity in meta")
	}
	if err := domain.ValidateIdentity(ident); err != nil {
		return nil, fmt.Errorf("begin amendment: incomplete identity: %w", err)
	}

	currentRev := meta[MetaIdentityRevision]
	nextRev := incrementRevision(currentRev)

	// CAS: check current state, identity head SHA, and revision
	checks := identityChecks(ident)
	// delivery_state may be missing (unset) — allow empty check for missing
	if currentState != "" {
		checks[MetaDeliveryState] = currentState
	}
	checks[MetaIdentityRevision] = currentRev

	updates := map[string]string{
		MetaDeliveryState:     string(DeliveryStateAmending),
		MetaAmendExpectedHead: ident.HeadSHA,
		MetaAmendStartedAt:    time.Now().UTC().Format(time.RFC3339),
		MetaIdentityRevision:  nextRev,
		MetaGitAuthContext:    "amendment",
	}

	result, err := home.CompareAndSwapMeta(homeDir, taskID, checks, updates)
	if err != nil {
		return nil, fmt.Errorf("begin amendment: %w", err)
	}

	return result, nil
}

// AcceptAmendment transitions from amending to review-ready after verifying
// the provider snapshot. It verifies:
//   - The stored identity matches (provider, repo, PR, base, head ref)
//   - The expected old head equals the currently stored head
//   - The provider snapshot reports a new head SHA
//   - The old head is an ancestor of the new head in the retained worktree
//   - No force-push, rewritten ancestry, or branch replacement
//
// On success, atomically updates the identity and appends an audit record.
// Returns the updated identity and audit record.
func AcceptAmendment(homeDir, taskID, worktreePath string) (*domain.DeliveryIdentity, *AmendRecord, error) {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return nil, nil, fmt.Errorf("accept amendment: reading meta: %w", err)
	}

	if meta[MetaDeliveryState] != string(DeliveryStateAmending) {
		return nil, nil, fmt.Errorf("accept amendment: expected state %q but got %q", DeliveryStateAmending, meta[MetaDeliveryState])
	}

	expectedHead := meta[MetaAmendExpectedHead]
	if expectedHead == "" {
		return nil, nil, fmt.Errorf("accept amendment: no amend_expected_head in meta")
	}

	// Read stored identity
	stored, err := domain.IdentityFromMeta(meta)
	if err != nil {
		return nil, nil, fmt.Errorf("accept amendment: reading stored identity: %w", err)
	}
	if stored == nil {
		return nil, nil, fmt.Errorf("accept amendment: no stored identity")
	}

	// Verify expected head equals stored head
	if stored.HeadSHA != expectedHead {
		return nil, nil, fmt.Errorf("accept amendment: expected head SHA %q does not match stored head %q (stale CAS check)", expectedHead, stored.HeadSHA)
	}

	// Fetch provider snapshot
	snap, err := FetchProviderSnapshot(stored.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("accept amendment: provider snapshot: %w", err)
	}

	// Verify same provider, repo, PR, base, head ref
	if err := verifySnapshotIdentity(stored, snap); err != nil {
		return nil, nil, fmt.Errorf("accept amendment: %w", err)
	}

	// Verify provider reports a new head
	if snap.HeadSHA == "" {
		return nil, nil, fmt.Errorf("accept amendment: provider returned empty head SHA")
	}
	if snap.HeadSHA == stored.HeadSHA {
		return nil, nil, fmt.Errorf("accept amendment: head SHA unchanged (%s), no amendment needed", stored.HeadSHA)
	}

	// Verify old head is ancestor of new head (no force-push)
	if err := verifyAncestry(worktreePath, expectedHead, snap.HeadSHA); err != nil {
		return nil, nil, fmt.Errorf("accept amendment: ancestry check: %w", err)
	}

	// Build new identity
	newIdent := &domain.DeliveryIdentity{
		Provider:   stored.Provider,
		Owner:      stored.Owner,
		Repo:       stored.Repo,
		Number:     stored.Number,
		URL:        stored.URL,
		BaseRef:    snap.BaseRef,
		HeadRef:    snap.HeadRef,
		HeadSHA:    snap.HeadSHA,
		CapturedAt: snap.ObservedAt,
	}

	// Build audit record
	record := &AmendRecord{
		OldHeadSHA:       expectedHead,
		NewHeadSHA:       snap.HeadSHA,
		PRIdentity:       fmt.Sprintf("%s/%s/%s#%d", stored.Provider, stored.Owner, stored.Repo, stored.Number),
		ProviderEvidence: fmt.Sprintf("provider %s state=%s head=%s", snap.Provider, snap.State, snap.HeadSHA),
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		Reason:           "amendment",
	}

	// CAS: check identity + amending state + revision
	checks := map[string]string{
		"pr_provider":         stored.Provider,
		"pr_owner":            stored.Owner,
		"pr_repo":             stored.Repo,
		"pr_number":           fmt.Sprintf("%d", stored.Number),
		"pr_url":              stored.URL,
		"pr_head_sha":         expectedHead,
		MetaDeliveryState:     string(DeliveryStateAmending),
		MetaAmendExpectedHead: expectedHead,
		MetaIdentityRevision:  meta[MetaIdentityRevision],
	}

	updates := identityUpdates(newIdent)
	updates[MetaDeliveryState] = string(DeliveryStateReviewReady)
	// Explicitly clear pending amendment fields
	updates[MetaAmendExpectedHead] = ""
	updates[MetaAmendStartedAt] = ""
	updates[MetaIdentityRevision] = incrementRevision(meta[MetaIdentityRevision])
	// Clear git auth context
	updates[MetaGitAuthContext] = ""
	// Append audit record
	updates[MetaAmendHistory] = appendAmendHistory(meta[MetaAmendHistory], record)

	_, err = home.CompareAndSwapMeta(homeDir, taskID, checks, updates)
	if err != nil {
		return nil, nil, fmt.Errorf("accept amendment: cas: %w", err)
	}

	return newIdent, record, nil
}

// --- Reconciliation ---

// ReconcileIdentity performs provider-aware reconciliation of stale delivery
// metadata. It queries the provider for the current state and, if the stored
// head differs, attempts a CAS update when the identity is still valid
// (same provider/repo/PR/base/head-ref) and the old head is an ancestor of
// the new head.
//
// Supports both open and merged PRs. For merged PRs, sets delivery_state=merged.
// For open PRs with advanced heads, sets delivery_state=review-ready.
//
// This is the recovery route for PR #339 and similar cases. It requires only
// the stored identity and provider access — no manual meta edits or --force.
func ReconcileIdentity(homeDir, taskID, worktreePath string) (*domain.DeliveryIdentity, *AmendRecord, error) {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return nil, nil, fmt.Errorf("reconcile: reading meta: %w", err)
	}

	stored, err := domain.IdentityFromMeta(meta)
	if err != nil {
		return nil, nil, fmt.Errorf("reconcile: reading stored identity: %w", err)
	}
	if stored == nil {
		return nil, nil, fmt.Errorf("reconcile: no delivery identity in meta")
	}

	// Fetch provider snapshot
	snap, err := FetchProviderSnapshot(stored.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("reconcile: provider snapshot: %w", err)
	}

	// Verify same provider, repo, PR, base, head ref
	if err := verifySnapshotIdentity(stored, snap); err != nil {
		return nil, nil, fmt.Errorf("reconcile: identity mismatch: %w", err)
	}

	// If stored and provider heads match, nothing to do
	if stored.HeadSHA == snap.HeadSHA {
		return stored, nil, nil
	}

	// Verify old head is ancestor of new head (rejects force-push)
	if err := verifyAncestry(worktreePath, stored.HeadSHA, snap.HeadSHA); err != nil {
		return nil, nil, fmt.Errorf("reconcile: ancestry check: %w", err)
	}

	// Build new identity
	newIdent := &domain.DeliveryIdentity{
		Provider:   stored.Provider,
		Owner:      stored.Owner,
		Repo:       stored.Repo,
		Number:     stored.Number,
		URL:        stored.URL,
		BaseRef:    snap.BaseRef,
		HeadRef:    snap.HeadRef,
		HeadSHA:    snap.HeadSHA,
		CapturedAt: snap.ObservedAt,
	}

	// Determine new delivery state
	newState := string(DeliveryStateReviewReady)
	if snap.Merged {
		newState = string(DeliveryStateMerged)
	}

	// Build audit record
	record := &AmendRecord{
		OldHeadSHA:       stored.HeadSHA,
		NewHeadSHA:       snap.HeadSHA,
		PRIdentity:       fmt.Sprintf("%s/%s/%s#%d", stored.Provider, stored.Owner, stored.Repo, stored.Number),
		ProviderEvidence: fmt.Sprintf("provider %s state=%s head=%s mergedSHA=%s", snap.Provider, snap.State, snap.HeadSHA, snap.MergedSHA),
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		Reason:           "reconciliation",
	}

	// CAS: verify stored identity still matches
	checks := identityChecks(stored)
	checks[MetaIdentityRevision] = meta[MetaIdentityRevision]

	updates := identityUpdates(newIdent)
	updates[MetaDeliveryState] = newState
	// Clear any pending amendment state explicitly
	updates[MetaAmendExpectedHead] = ""
	updates[MetaAmendStartedAt] = ""
	updates[MetaIdentityRevision] = incrementRevision(meta[MetaIdentityRevision])
	// Clear git auth context (reconciliation ends any amendment context)
	updates[MetaGitAuthContext] = ""
	updates[MetaAmendHistory] = appendAmendHistory(meta[MetaAmendHistory], record)

	_, err = home.CompareAndSwapMeta(homeDir, taskID, checks, updates)
	if err != nil {
		return nil, nil, fmt.Errorf("reconcile: cas: %w", err)
	}

	return newIdent, record, nil
}

// --- Helpers ---

// verifySnapshotIdentity checks that the stored identity matches the provider
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

// incrementRevision increments a revision string. The revision is a non-negative
// integer. Returns "1" if the input is empty or invalid.
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

// PrMetaChecks builds a CAS check map for all canonical PR meta fields
// from a meta map. Returns an error if any required field is missing.
func PrMetaChecks(meta map[string]string) (map[string]string, error) {
	checks := make(map[string]string)
	for _, key := range PrMetaFields() {
		v, ok := meta[key]
		if !ok || v == "" {
			return nil, fmt.Errorf("missing required meta field %q for CAS", key)
		}
		checks[key] = v
	}
	return checks, nil
}
