// Package delivery implements delivery operations.
package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
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

// BeginAmendment transitions a delivery from review-ready to amending. The
// amendment context is an authoritative generation-bound record (Task 7.4):
// it routes through the composed Task Authority, and the amending intent is
// a post-commit projection (delivery_state=amending plus the amendment
// fields, Task 7.6). A projection failure warns and never rolls back the
// authoritative commit; an authority error fails closed.
//
// Returns the updated meta on success. Fail-closed if state is not review-ready
// or if the stored identity is incomplete.
func BeginAmendment(homeDir, taskID string, auth *taskauthority.Authority) (map[string]string, error) {
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

	// Route the git authorization context through the composed Authority
	// (Task 7.4) before the amending projection so a failure leaves the
	// amendment unstarted.
	if _, err := StoreGitAuthContext(homeDir, auth, taskID, "amendment"); err != nil {
		var projErr *AuthorizationProjectionError
		if errors.As(err, &projErr) {
			fmt.Fprintf(os.Stderr, "Warning: git authorization context projection failed: %v\n", projErr)
		} else {
			return nil, fmt.Errorf("begin amendment: git authorization context: %w", err)
		}
	}

	// The delivery_state=amending transition is a post-commit projection of
	// the authoritative amendment context (Task 7.6); the raw CAS is gone.
	return projectAmendmentBeginMeta(homeDir, taskID, ident)
}

// AcceptAmendment transitions from amending to review-ready after verifying
// the provider snapshot. It verifies:
//   - The stored identity matches (provider, repo, PR, base, head ref)
//   - The expected old head equals the currently stored head
//   - The provider snapshot reports a new head SHA
//   - The old head is an ancestor of the new head in the retained worktree
//   - No force-push, rewritten ancestry, or branch replacement
//
// The git authorization context clear is an authoritative generation-bound
// record (Task 7.4): it routes through the composed Task Authority. The
// amended identity rebinds the generation-bound delivery preparation at the
// new head (Task 7.5 op: the committed prior prepared head is acknowledged
// explicitly, never silently reused) and the delivery_state=review-ready
// transition plus amendment history are reconciled as post-commit
// projections (Task 7.6); the raw CAS is gone.
//
// On success, atomically updates the identity and appends an audit record.
// Returns the updated identity and audit record.
func AcceptAmendment(homeDir, taskID, worktreePath string, auth *taskauthority.Authority) (*domain.DeliveryIdentity, *AmendRecord, error) {
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

	// The amendment rebinds the generation-bound delivery preparation: the
	// authoritative prepared head must equal the amendment expected head
	// (force-with-lease; a changed head is never silently reused).
	agg, err := auth.Get(taskID)
	if err != nil {
		return nil, nil, fmt.Errorf("accept amendment: resolving task generation: %w", err)
	}
	if agg.DeliveryPrepare == nil {
		return nil, nil, fmt.Errorf("accept amendment: no delivery preparation in the authoritative record; run pr-check first")
	}
	if agg.DeliveryPrepare.HeadSHA != expectedHead {
		return nil, nil, fmt.Errorf("accept amendment: authoritative prepared head %q does not match amendment expected head %q", agg.DeliveryPrepare.HeadSHA, expectedHead)
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

	// Route the git authorization context clear through the composed Authority
	// (Task 7.4) before the delivery rebind so a failure leaves the amendment
	// state untouched.
	if _, err := StoreGitAuthContext(homeDir, auth, taskID, ""); err != nil {
		var projErr *AuthorizationProjectionError
		if errors.As(err, &projErr) {
			fmt.Fprintf(os.Stderr, "Warning: git authorization context projection failed: %v\n", projErr)
		} else {
			return nil, nil, fmt.Errorf("accept amendment: git authorization context: %w", err)
		}
	}

	// Rebind the generation-bound delivery preparation at the amended head
	// (Task 7.5 op; the prior prepared head is acknowledged explicitly).
	if _, err := prepareDeliveryRebind(homeDir, auth, taskID, newIdent); err != nil {
		return nil, nil, fmt.Errorf("accept amendment: delivery rebind: %w", err)
	}

	// The delivery_state=review-ready transition plus the amendment history
	// are reconciled as post-commit projections (Task 7.6); the raw CAS is
	// gone.
	if perr := projectAmendResultMeta(homeDir, taskID, newIdent, string(DeliveryStateReviewReady), record); perr != nil {
		return nil, nil, perr
	}

	return newIdent, record, nil
}

// --- Reconciliation ---

// ReconcileIdentity performs provider-aware reconciliation of stale delivery
// metadata. It queries the provider for the current state and, if the stored
// head differs, routes the updated identity through the composed Task
// Authority when the identity is still valid (same provider/repo/PR/base/
// head-ref) and the old head is an ancestor of the new head.
//
// Supports both open and merged PRs. For merged PRs, commits a generation-
// bound merged merge outcome and projects delivery_state=merged. For open PRs
// with advanced heads, rebinds the delivery preparation at the new head and
// projects delivery_state=review-ready. All delivery_state transitions are
// post-commit projections (Task 7.6); the raw CAS is gone.
//
// This is the recovery route for PR #339 and similar cases. It requires only
// the stored identity and provider access — no manual meta edits or --force.
func ReconcileIdentity(homeDir, taskID, worktreePath string, auth *taskauthority.Authority) (*domain.DeliveryIdentity, *AmendRecord, error) {
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

	// The reconcile rebinds the generation-bound delivery preparation: the
	// authoritative prepared head must be acknowledged explicitly (a changed
	// head is never silently reused). A committed remote-unknown outcome
	// forbids further provider-mutating attempts (Task 7.6): the reconcile
	// fails closed and only read reconciliation is permitted.
	agg, err := auth.Get(taskID)
	if err != nil {
		return nil, nil, fmt.Errorf("reconcile: resolving task generation: %w", err)
	}
	if agg.MergeAttempt != nil && agg.MergeAttempt.Outcome == taskauthority.MergeOutcomeRemoteUnknown {
		return nil, nil, fmt.Errorf("reconcile: remote-unknown merge outcome committed; read reconciliation only (same mutation is never repeated)")
	}
	if agg.DeliveryPrepare == nil {
		return nil, nil, fmt.Errorf("reconcile: no delivery preparation in the authoritative record; run pr-check first")
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

	// Route the git authorization context clear through the composed Authority
	// (Task 7.4) before the delivery rebind so a failure leaves the
	// reconciliation state untouched.
	if _, err := StoreGitAuthContext(homeDir, auth, taskID, ""); err != nil {
		var projErr *AuthorizationProjectionError
		if errors.As(err, &projErr) {
			fmt.Fprintf(os.Stderr, "Warning: git authorization context projection failed: %v\n", projErr)
		} else {
			return nil, nil, fmt.Errorf("reconcile: git authorization context: %w", err)
		}
	}

	// Rebind the generation-bound delivery preparation at the reconciled head
	// (Task 7.5 op; the prior prepared head is acknowledged explicitly).
	if _, err := prepareDeliveryRebind(homeDir, auth, taskID, newIdent); err != nil {
		return nil, nil, fmt.Errorf("reconcile: delivery rebind: %w", err)
	}

	// For a merged PR, commit the generation-bound merged merge outcome
	// (Task 7.6): verified merge evidence (identity/head/merged SHA) drives
	// the merged transition; the raw CAS is gone.
	if snap.Merged {
		agg2, err := auth.Get(taskID)
		if err != nil {
			return nil, nil, fmt.Errorf("reconcile: resolving task generation: %w", err)
		}
		if _, err := auth.RecordMergeAttempt(taskauthority.RecordMergeAttemptRequest{
			OperationID:        mustDeliveryOperationID("merge-attempt-" + taskID),
			Actor:              deliveryActor(homeDir),
			TaskID:             taskID,
			ExpectedGeneration: agg2.Generation,
			Outcome:            taskauthority.MergeOutcomeMerged,
			HeadSHA:            newIdent.HeadSHA,
			MergedSHA:          snap.MergedSHA,
			Identity:           snapshotFromIdentity(newIdent),
			ProviderState:      snap.State,
			Detail:             "reconciliation: provider confirms merged",
			Reason:             "delivery reconcile",
		}); err != nil {
			return nil, nil, fmt.Errorf("reconcile: merge outcome: %w", err)
		}
	}

	// The delivery_state transition plus the amendment history are reconciled
	// as post-commit projections (Task 7.6); the raw CAS is gone.
	if perr := projectAmendResultMeta(homeDir, taskID, newIdent, newState, record); perr != nil {
		return nil, nil, perr
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

// prepareDeliveryRebind routes the amended delivery identity rebind through
// the composed Task Authority (Task 7.5 op): the generation-bound delivery
// preparation is re-prepared at the amended head, acknowledging the committed
// prior prepared head (force-with-lease; a changed head is never silently
// reused). No projection runs here — the amendment caller owns the single
// delivery_state + identity projection for the whole amendment, so a partial
// failure leaves the authoritative record retryable (ADR-0007 §7).
func prepareDeliveryRebind(homeDir string, auth *taskauthority.Authority, taskID string, ident *domain.DeliveryIdentity) (taskauthority.DeliveryResult, error) {
	if auth == nil {
		return taskauthority.DeliveryResult{}, fmt.Errorf("amendment delivery rebind requires a composed task authority")
	}
	if err := domain.ValidateIdentity(ident); err != nil {
		return taskauthority.DeliveryResult{}, fmt.Errorf("amendment delivery rebind: invalid identity: %w", err)
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		return taskauthority.DeliveryResult{}, fmt.Errorf("amendment delivery rebind: resolving task generation: %w", err)
	}
	priorHead := ""
	if agg.DeliveryPrepare != nil {
		priorHead = agg.DeliveryPrepare.HeadSHA
	}
	return auth.PrepareDelivery(taskauthority.PrepareDeliveryRequest{
		OperationID:        mustDeliveryOperationID("delivery-amend-" + taskID),
		Actor:              deliveryActor(homeDir),
		TaskID:             taskID,
		ExpectedGeneration: agg.Generation,
		State:              taskauthority.DeliveryPrepareStateReviewReady,
		HeadSHA:            ident.HeadSHA,
		Identity:           snapshotFromIdentity(ident),
		ExpectedPriorHead:  priorHead,
		Reason:             "delivery amendment",
	})
}

// projectAmendmentBeginMeta reconciles the .meta amendment-intent projection
// after the authoritative amendment context commit (Task 7.6): the
// delivery_state=amending transition plus the amendment intent fields,
// mirroring the legacy CAS. A projection failure returns a typed partial
// error and never rolls back the authoritative commit.
func projectAmendmentBeginMeta(homeDir, taskID string, ident *domain.DeliveryIdentity) (map[string]string, error) {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		meta = make(map[string]string)
	}
	meta[MetaDeliveryState] = string(DeliveryStateAmending)
	meta[MetaAmendExpectedHead] = ident.HeadSHA
	meta[MetaAmendStartedAt] = time.Now().UTC().Format(time.RFC3339)
	meta[MetaIdentityRevision] = incrementRevision(meta[MetaIdentityRevision])
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		return nil, &DeliveryProjectionError{TaskID: taskID, ProjectionErr: err}
	}
	return meta, nil
}

// projectAmendResultMeta reconciles the .meta amendment result projection
// after the authoritative amended delivery rebind and, for a merged PR, the
// committed merged merge outcome (Task 7.6): the amended identity keys, the
// delivery_state transition (review-ready or merged), the appended amendment
// history, and the cleared amendment-intent fields, mirroring the legacy
// CAS. A projection failure returns a typed partial error and never rolls
// back the authoritative commit.
func projectAmendResultMeta(homeDir, taskID string, ident *domain.DeliveryIdentity, newState string, record *AmendRecord) error {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		meta = make(map[string]string)
	}
	for k, v := range ident.ToMeta() {
		meta[k] = v
	}
	meta["pr"] = ident.URL
	meta["pr_head"] = ident.HeadSHA
	meta[MetaDeliveryState] = newState
	meta[MetaAmendExpectedHead] = ""
	meta[MetaAmendStartedAt] = ""
	meta[MetaIdentityRevision] = incrementRevision(meta[MetaIdentityRevision])
	if record != nil {
		meta[MetaAmendHistory] = appendAmendHistory(meta[MetaAmendHistory], record)
	}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		return &DeliveryProjectionError{TaskID: taskID, ProjectionErr: err}
	}
	return nil
}
