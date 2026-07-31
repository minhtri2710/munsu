package fleet

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// MergeOutcome represents the result of a merge attempt after provider reconciliation.
type MergeOutcome string

const (
	// MergeOutcomeMerged: provider confirms the PR is now merged.
	MergeOutcomeMerged MergeOutcome = "merged"
	// MergeOutcomeAlreadyMerged: provider reports the PR was already merged
	// (external merge or previous successful attempt).
	MergeOutcomeAlreadyMerged MergeOutcome = "already-merged"
	// MergeOutcomeFailed: provider reports the PR is closed but not merged.
	MergeOutcomeFailed MergeOutcome = "failed"
	// MergeOutcomeOpen: PR is still open after the merge attempt;
	// a new attempt with an updated identity may be permitted.
	MergeOutcomeOpen MergeOutcome = "open"
	// MergeOutcomeRemoteUnknown: the provider result was ambiguous or
	// unreachable. The same mutation attempt must never be repeated.
	MergeOutcomeRemoteUnknown MergeOutcome = "remote-unknown"
)

// MergeDeliveryResult captures the full outcome of a merge delivery reconciliation.
type MergeDeliveryResult struct {
	Outcome       MergeOutcome `json:"outcome"`
	ProviderState string       `json:"providerState,omitempty"`
	MergedSHA     string       `json:"mergedSHA,omitempty"`
	HeadSHA       string       `json:"headSHA,omitempty"`
	RemoteKnown   bool         `json:"remoteKnown"`   // true when provider response was unambiguous
	Escalated     bool         `json:"escalated"`     // true when persistent uncertainty requires operator attention
	StoredState   string       `json:"storedState,omitempty"`
	Detail        string       `json:"detail,omitempty"`
	PRNumber      int          `json:"prNumber,omitempty"`
	MergeMethod   string       `json:"mergeMethod,omitempty"`
}

// IsError returns true when the outcome should produce a non-zero exit code.
// Partial outcomes (Open, RemoteUnknown) and failures (Failed) are errors.
// Escalated outcomes are always errors regardless of the base outcome.
func (r *MergeDeliveryResult) IsError() bool {
	if r == nil {
		return true
	}
	if r.Escalated {
		return true
	}
	switch r.Outcome {
	case MergeOutcomeOpen, MergeOutcomeRemoteUnknown, MergeOutcomeFailed:
		return true
	default:
		return false
	}
}

// Render returns a human-readable summary of the merge delivery result,
// leading with the remote truth. The output includes a machine-readable
// AXI block at the end for agent consumption.
func (r *MergeDeliveryResult) Render() string {
	if r == nil {
		return ""
	}

	var b strings.Builder

	// --- Remote truth line (always first) ---
	switch {
	case r.MergedSHA != "":
		fmt.Fprintf(&b, "Remote truth: merged, SHA=%s\n", r.MergedSHA)
	case r.ProviderState != "" && r.HeadSHA != "":
		fmt.Fprintf(&b, "Remote truth: %s, head=%s\n", strings.ToLower(r.ProviderState), r.HeadSHA)
	case r.ProviderState != "":
		fmt.Fprintf(&b, "Remote truth: %s\n", strings.ToLower(r.ProviderState))
	default:
		fmt.Fprintf(&b, "Remote truth: unreachable\n")
	}

	// --- Outcome line ---
	switch r.Outcome {
	case MergeOutcomeMerged:
		if r.PRNumber > 0 {
			fmt.Fprintf(&b, "PR merged: #%d", r.PRNumber)
			if r.MergeMethod != "" {
				fmt.Fprintf(&b, " (%s)", r.MergeMethod)
			}
			b.WriteString("\n")
		} else {
			b.WriteString("PR merged\n")
		}
	case MergeOutcomeAlreadyMerged:
		if r.PRNumber > 0 {
			fmt.Fprintf(&b, "PR already merged: #%d\n", r.PRNumber)
		} else {
			b.WriteString("PR already merged\n")
		}
	case MergeOutcomeOpen:
		fmt.Fprintf(&b, "%s\n", r.Detail)
		b.WriteString("Next: re-run pr-check after pushing new changes, then retry merge\n")
	case MergeOutcomeRemoteUnknown:
		if r.Detail != "" {
			fmt.Fprintf(&b, "%s\n", r.Detail)
		}
		b.WriteString("Same mutation will not be repeated. Escalate to operator.\n")
	case MergeOutcomeFailed:
		fmt.Fprintf(&b, "%s\n", r.Detail)
	default:
		if r.Detail != "" {
			fmt.Fprintf(&b, "%s\n", r.Detail)
		}
	}

	// --- AXI machine-readable block ---
	b.WriteString("\nmerge-delivery:\n")
	fmt.Fprintf(&b, "  outcome: %s\n", r.Outcome)
	if r.MergedSHA != "" {
		fmt.Fprintf(&b, "  merged-sha: %s\n", r.MergedSHA)
	}
	if r.HeadSHA != "" {
		fmt.Fprintf(&b, "  head-sha: %s\n", r.HeadSHA)
	}
	fmt.Fprintf(&b, "  remote-known: %t\n", r.RemoteKnown)
	b.WriteString(fmt.Sprintf("  escalated: %t\n", r.Escalated))

	return b.String()
}

// ReconcileMergeDelivery reconciles the provider's remote truth after a merge
// attempt. It queries the provider for the current state of the PR/MR and
// classifies the outcome into one of the MergeOutcome values.
//
// Provider confirms merged:
//   - Outcome = Merged (or AlreadyMerged if stored state was already terminal)
//   - delivery_state transitions to merged
//
// Provider reports PR is still open:
//   - Outcome = Open; a new attempt with updated identity is permitted
//   - delivery_state transitions to review-ready
//
// Provider is ambiguous or unreachable:
//   - Outcome = RemoteUnknown; the same mutation is never repeated
//   - delivery_state transitions to remote-unknown
//   - If already in remote-unknown, Escalated=true for operator attention
//
// Provider reports PR is closed but not merged:
//   - Outcome = Failed; terminal failure
var ReconcileMergeDelivery = reconcileMergeDeliveryImpl

func reconcileMergeDeliveryImpl(homeDir, taskID, prURL string) (*MergeDeliveryResult, error) {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("reconcile merge: reading meta: %w", err)
	}

	storedState := meta[MetaDeliveryState]
	storedIdent, _ := domain.IdentityFromMeta(meta)

	// Fetch provider snapshot
	snap, err := FetchProviderSnapshot(prURL)
	if err != nil {
		// Provider is unavailable or error — this is remote-unknown
		result := &MergeDeliveryResult{
			Outcome:     MergeOutcomeRemoteUnknown,
			RemoteKnown: false,
			StoredState: storedState,
			Detail:      fmt.Sprintf("provider snapshot failed: %v", err),
		}

		// Detect persistent uncertainty: already in remote-unknown
		if storedState == string(DeliveryStateRemoteUnknown) {
			result.Escalated = true
			result.Detail = "persistent remote-unknown: " + err.Error()
		}

		// Persist remote-unknown state (CAS with identity check)
		if writeErr := persistRemoteUnknown(homeDir, taskID, meta, storedIdent, storedState); writeErr != nil {
			return nil, fmt.Errorf("reconcile merge: persisting remote-unknown: %w", writeErr)
		}

		result.StoredState = string(DeliveryStateRemoteUnknown)
		return result, nil
	}

	// Build result fields from provider snapshot
	result := &MergeDeliveryResult{
		ProviderState: snap.State,
		MergedSHA:     snap.MergedSHA,
		HeadSHA:       snap.HeadSHA,
		RemoteKnown:   true,
		StoredState:   storedState,
		PRNumber:      snap.Number,
	}

	if snap.Merged {
		// Provider confirms PR is merged
		if storedState == string(DeliveryStateMerged) || storedState == string(DeliveryStateDelivered) {
			result.Outcome = MergeOutcomeAlreadyMerged
			result.Detail = "PR was already merged (external merge or previous attempt)"
			return result, nil
		}

		result.Outcome = MergeOutcomeMerged
		result.Detail = fmt.Sprintf("provider confirms PR #%d is merged", snap.Number)

		// Persist merged state
		if writeErr := persistMerged(homeDir, taskID, meta, storedIdent, storedState, snap); writeErr != nil {
			return nil, fmt.Errorf("reconcile merge: persisting merged: %w", writeErr)
		}

		result.StoredState = string(DeliveryStateMerged)
		return result, nil
	}

	if snap.State == "OPEN" {
		// PR is still open — merge didn't take effect. A new attempt is permitted.
		result.Outcome = MergeOutcomeOpen
		result.Detail = fmt.Sprintf("PR #%d is still open (head=%s); merge attempt did not take effect", snap.Number, snap.HeadSHA)

		// Don't regress from merged/delivered to review-ready
		if storedState == string(DeliveryStateMerged) || storedState == string(DeliveryStateDelivered) {
			result.Detail += "; stored state is " + storedState + ", not regressing"
			return result, nil
		}

		// Transition to review-ready so the caller can retry with a fresh identity
		if writeErr := persistReviewReady(homeDir, taskID, meta, storedIdent, storedState); writeErr != nil {
			return nil, fmt.Errorf("reconcile merge: persisting review-ready: %w", writeErr)
		}

		result.StoredState = string(DeliveryStateReviewReady)
		return result, nil
	}

	// PR is closed but not merged — terminal failure
	result.Outcome = MergeOutcomeFailed
	result.Detail = fmt.Sprintf("PR #%d is closed but not merged (state=%s)", snap.Number, snap.State)

	return result, nil
}

// persistRemoteUnknown atomically CAS-transitions delivery_state to remote-unknown.
func persistRemoteUnknown(homeDir, taskID string, meta map[string]string, ident *domain.DeliveryIdentity, currentState string) error {
	checks := make(map[string]string)

	// Include identity checks when available
	if ident != nil {
		for k, v := range identityChecks(ident) {
			checks[k] = v
		}
	}

	if currentState != "" {
		checks[MetaDeliveryState] = currentState
	}
	checks[MetaIdentityRevision] = meta[MetaIdentityRevision]

	updates := map[string]string{
		MetaDeliveryState:    string(DeliveryStateRemoteUnknown),
		MetaIdentityRevision: incrementRevision(meta[MetaIdentityRevision]),
	}

	_, err := home.CompareAndSwapMeta(homeDir, taskID, checks, updates)
	return err
}

// persistMerged atomically CAS-transitions delivery_state to merged.
func persistMerged(homeDir, taskID string, meta map[string]string, ident *domain.DeliveryIdentity, currentState string, snap *ProviderSnapshot) error {
	checks := make(map[string]string)

	if ident != nil {
		for k, v := range identityChecks(ident) {
			checks[k] = v
		}
		// Update head SHA from provider snapshot if available
		if snap.HeadSHA != "" && snap.HeadSHA != ident.HeadSHA {
			checks["pr_head_sha"] = ident.HeadSHA
		}
	}

	if currentState != "" {
		checks[MetaDeliveryState] = currentState
	}
	checks[MetaIdentityRevision] = meta[MetaIdentityRevision]

	updates := map[string]string{
		MetaDeliveryState:    string(DeliveryStateMerged),
		MetaIdentityRevision: incrementRevision(meta[MetaIdentityRevision]),
	}

	_, err := home.CompareAndSwapMeta(homeDir, taskID, checks, updates)
	return err
}

// persistReviewReady atomically CAS-transitions delivery_state to review-ready.
func persistReviewReady(homeDir, taskID string, meta map[string]string, ident *domain.DeliveryIdentity, currentState string) error {
	checks := make(map[string]string)

	if ident != nil {
		for k, v := range identityChecks(ident) {
			checks[k] = v
		}
	}

	if currentState != "" {
		checks[MetaDeliveryState] = currentState
	}
	checks[MetaIdentityRevision] = meta[MetaIdentityRevision]

	updates := map[string]string{
		MetaDeliveryState:    string(DeliveryStateReviewReady),
		MetaIdentityRevision: incrementRevision(meta[MetaIdentityRevision]),
	}

	_, err := home.CompareAndSwapMeta(homeDir, taskID, checks, updates)
	return err
}

// MarkMerged atomically transitions delivery_state to merged for a task,
// using CAS to guard against concurrent modifications.
//
// It checks the canonical identity fields (from expected), the current
// delivery_state value, and increments pr_identity_revision when the
// state was not already merged.
//
// Returns nil when delivery_state is already merged (idempotent).
// Returns a CASError when another writer modified meta concurrently.
// Returns an error when meta cannot be read or written.
func MarkMerged(homeDir, taskID string, expected *domain.DeliveryIdentity) error {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return fmt.Errorf("mark merged: reading meta: %w", err)
	}

	// Idempotent: if already merged, nothing to do.
	if meta[MetaDeliveryState] == string(DeliveryStateMerged) {
		return nil
	}

	// Build CAS checks from the expected identity.
	checks := map[string]string{
		"pr_provider": expected.Provider,
		"pr_owner":    expected.Owner,
		"pr_repo":     expected.Repo,
		"pr_number":   fmt.Sprintf("%d", expected.Number),
		"pr_url":      expected.URL,
		"pr_base_ref": expected.BaseRef,
		"pr_head_ref": expected.HeadRef,
		"pr_head_sha": expected.HeadSHA,
	}

	// Include delivery_state in checks only when present in meta.
	// When absent (pre-init or legacy), skip the check — the identity
	// checks are sufficient to guard against concurrent mutation.
	if currentState := meta[MetaDeliveryState]; currentState != "" {
		checks[MetaDeliveryState] = currentState
	}

	// Build updates.
	updates := map[string]string{
		MetaDeliveryState:    string(DeliveryStateMerged),
		MetaIdentityRevision: incrementRevision(meta[MetaIdentityRevision]),
	}

	if _, err := home.CompareAndSwapMeta(homeDir, taskID, checks, updates); err != nil {
		return fmt.Errorf("mark merged: %w", err)
	}

	return nil
}

// MarkMergedFromRecord is a convenience wrapper that builds a domain.DeliveryIdentity
// from a PollRetirementRecord's fields and calls MarkMerged. It is used by
// the supervision recovery path.
//
// The record's identity fields (Provider, Owner, Repo, Number, URL, BaseRef,
// HeadRef, HeadSHA) must be non-empty and consistent.
func MarkMergedFromRecord(homeDir, taskID string, provider, owner, repo string,
	number int, url, baseRef, headRef, headSHA string) error {

	ident := &domain.DeliveryIdentity{
		Provider: provider,
		Owner:    owner,
		Repo:     repo,
		Number:   number,
		URL:      url,
		BaseRef:  baseRef,
		HeadRef:  headRef,
		HeadSHA:  headSHA,
	}
	return MarkMerged(homeDir, taskID, ident)
}
