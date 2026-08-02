package fleet

import (
	"errors"
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
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
	RemoteKnown   bool         `json:"remoteKnown"` // true when provider response was unambiguous
	Escalated     bool         `json:"escalated"`   // true when persistent uncertainty requires operator attention
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

// MergeOutcomeProjectionError is the typed partial outcome of a merge attempt
// whose authoritative commit succeeded but whose .meta projection could not be
// written (ADR-0007 §7). The authoritative state is never rolled back; the
// projection can be retried independently and replays idempotently.
type MergeOutcomeProjectionError struct {
	TaskID        string
	ProjectionErr error
}

func (e *MergeOutcomeProjectionError) Error() string {
	return fmt.Sprintf("merge attempt committed for %s but projection failed: %v", e.TaskID, e.ProjectionErr)
}

func (e *MergeOutcomeProjectionError) Unwrap() error { return e.ProjectionErr }

// committedMergeOutcome reads the authoritative committed merge outcome for
// read reconciliation (Task 7.6): "" when no terminal outcome is committed,
// "merged-equivalent" when the provider-verified merged truth stands (a
// committed merged/already-merged attempt or a delivered/done terminal
// record), or "remote-unknown" when a remote-unknown outcome forbids further
// provider mutation. Read reconciliation never mutates.
func committedMergeOutcome(auth *taskauthority.Authority, taskID string) (string, error) {
	agg, err := auth.Get(taskID)
	if err != nil {
		return "", err
	}
	if agg.MergeAttempt == nil {
		if agg.DeliveryTerminal != nil {
			return "merged-equivalent", nil
		}
		return "", nil
	}
	switch agg.MergeAttempt.Outcome {
	case taskauthority.MergeOutcomeMerged, taskauthority.MergeOutcomeAlreadyMerged:
		return "merged-equivalent", nil
	case taskauthority.MergeOutcomeRemoteUnknown:
		return "remote-unknown", nil
	}
	return "", nil
}

// StoreMergeAttempt routes one merge attempt and its provider-verified remote
// outcome through the composed Task Authority (Task 7.6): the generation-bound
// merge attempt record (outcome, exact head SHA, provider identity snapshot,
// merged SHA) commits fenced to the aggregate generation, and the delivery_state
// projection is reconciled as a post-commit projection (ADR-0007 §7). A
// committed remote-unknown outcome refuses further provider-mutating attempts
// with the typed fail-closed ErrMergeMutationRefused; verified merged truth is
// never erased by a later ambiguous or false-negative read (already-merged and
// provider false-negative outcomes are idempotent). A projection failure
// returns a typed partial error and never rolls back the authoritative commit.
// Production callers always supply the Authority; nil fails closed.
func StoreMergeAttempt(homeDir string, auth *taskauthority.Authority, taskID string, outcome MergeOutcome, ident *domain.DeliveryIdentity, mergedSHA, providerState, detail string) (taskauthority.DeliveryResult, error) {
	if auth == nil {
		return taskauthority.DeliveryResult{}, fmt.Errorf("merge attempt requires a composed task authority")
	}
	if err := domain.ValidateIdentity(ident); err != nil {
		return taskauthority.DeliveryResult{}, fmt.Errorf("store merge attempt: invalid identity: %w", err)
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		return taskauthority.DeliveryResult{}, fmt.Errorf("store merge attempt: resolving task generation: %w", err)
	}
	res, err := auth.RecordMergeAttempt(taskauthority.RecordMergeAttemptRequest{
		OperationID:        mustDeliveryOperationID("merge-attempt-" + taskID),
		Actor:              deliveryActor(homeDir),
		TaskID:             taskID,
		ExpectedGeneration: agg.Generation,
		Outcome:            string(outcome),
		HeadSHA:            ident.HeadSHA,
		MergedSHA:          mergedSHA,
		Identity:           snapshotFromIdentity(ident),
		ProviderState:      providerState,
		Detail:             detail,
		Reason:             "merge delivery outcome",
	})
	if err != nil {
		return taskauthority.DeliveryResult{}, err
	}
	// The projection reconciles the committed outcome into delivery_state. A
	// committed attempt advances the identity revision (mirroring the legacy
	// CAS); an idempotent re-attempt (already committed truth) only heals a
	// stale projection without advancing it — verified remote truth is never
	// erased.
	if perr := projectMergeOutcomeMeta(homeDir, taskID, outcome, res.Revision > agg.Revision); perr != nil {
		return res, perr
	}
	return res, nil
}

// projectMergeOutcomeMeta reconciles the .meta delivery_state projection after
// the authoritative merge attempt commit: merged/already-merged map to
// delivery_state=merged, open maps back to review-ready, remote-unknown
// persists the terminal remote-unknown state, and failed commits no
// delivery_state change (the authoritative attempt record is the truth). The
// identity revision advances only when the attempt committed (bump), mirroring
// the legacy CAS; an idempotent re-attempt heals a stale projection without
// advancing the revision and is a no-op when the projection is already
// consistent. A projection failure returns a typed partial error and never
// rolls back the authoritative commit.
func projectMergeOutcomeMeta(homeDir, taskID string, outcome MergeOutcome, bump bool) error {
	var target string
	switch outcome {
	case MergeOutcomeMerged, MergeOutcomeAlreadyMerged:
		target = string(DeliveryStateMerged)
	case MergeOutcomeOpen:
		target = string(DeliveryStateReviewReady)
	case MergeOutcomeRemoteUnknown:
		target = string(DeliveryStateRemoteUnknown)
	default: // failed: the attempt is authoritative; delivery_state is unchanged
		return nil
	}
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		meta = make(map[string]string)
	}
	if !bump && meta[MetaDeliveryState] == target {
		return nil // idempotent re-attempt with a consistent projection
	}
	meta[MetaDeliveryState] = target
	if bump {
		meta[MetaIdentityRevision] = incrementRevision(meta[MetaIdentityRevision])
	}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		return &MergeOutcomeProjectionError{TaskID: taskID, ProjectionErr: err}
	}
	return nil
}

// ReconcileMergeDelivery reconciles the provider's remote truth after a merge
// attempt. It queries the provider for the current state of the PR/MR and
// classifies the outcome into one of the MergeOutcome values. The merge
// attempt and its provider-verified outcome commit through the composed Task
// Authority (Task 7.6); the delivery_state .meta key is a post-commit
// projection. A committed remote-unknown outcome forbids further
// provider-mutating attempts: reconciliation then reads the committed outcome
// (read reconciliation only, typed fail-closed). Verified merged truth is
// never erased by a later ambiguous or false-negative read.
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

func reconcileMergeDeliveryImpl(homeDir, taskID, prURL string, auth *taskauthority.Authority) (*MergeDeliveryResult, error) {
	if auth == nil {
		return nil, fmt.Errorf("reconcile merge requires a composed task authority")
	}
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("reconcile merge: reading meta: %w", err)
	}

	storedState := meta[MetaDeliveryState]
	storedIdent, err := domain.IdentityFromMeta(meta)
	if err != nil {
		return nil, fmt.Errorf("reconcile merge: reading delivery identity: %w", err)
	}
	if storedIdent == nil {
		return nil, fmt.Errorf("reconcile merge: no delivery identity for task %s", taskID)
	}

	// Read the authoritative committed merge outcome (read reconciliation).
	committed, err := committedMergeOutcome(auth, taskID)
	if err != nil {
		return nil, fmt.Errorf("reconcile merge: reading committed outcome: %w", err)
	}

	// Fetch provider snapshot
	snap, err := FetchProviderSnapshot(prURL)
	if err != nil {
		// Provider is unavailable or errored — remote-unknown. Verified
		// remote truth is never erased by an ambiguous read: when a merged
		// outcome is already committed, the reconciliation reads it back and
		// escalates instead of re-mutating.
		switch committed {
		case "merged-equivalent":
			return &MergeDeliveryResult{
				Outcome:     MergeOutcomeAlreadyMerged,
				RemoteKnown: false,
				Escalated:   true,
				StoredState: storedState,
				Detail:      fmt.Sprintf("provider snapshot failed: %v; last verified outcome is merged", err),
			}, nil
		case "remote-unknown":
			return &MergeDeliveryResult{
				Outcome:     MergeOutcomeRemoteUnknown,
				RemoteKnown: false,
				Escalated:   true,
				StoredState: storedState,
				Detail:      "persistent remote-unknown: " + err.Error(),
			}, nil
		}

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

		// Persist the remote-unknown outcome via the Authority. A committed
		// remote-unknown outcome refuses the mutation (typed fail-closed);
		// the reconciliation then reports the committed outcome escalated.
		if _, writeErr := StoreMergeAttempt(homeDir, auth, taskID, MergeOutcomeRemoteUnknown, storedIdent, "", "", result.Detail); writeErr != nil {
			if errors.Is(writeErr, taskauthority.ErrMergeMutationRefused) {
				result.Escalated = true
				result.Detail = "persistent remote-unknown: " + err.Error()
				return result, nil
			}
			return nil, fmt.Errorf("reconcile merge: persisting remote-unknown: %w", writeErr)
		}

		result.StoredState = string(DeliveryStateRemoteUnknown)
		return result, nil
	}

	// Provider responded: a committed remote-unknown outcome forbids further
	// provider-mutating attempts. Only read reconciliation is permitted: the
	// provider read classifies the outcome but nothing mutates.
	if committed == "remote-unknown" {
		result := &MergeDeliveryResult{
			ProviderState: snap.State,
			MergedSHA:     snap.MergedSHA,
			HeadSHA:       snap.HeadSHA,
			RemoteKnown:   true,
			Escalated:     true,
			StoredState:   storedState,
			PRNumber:      snap.Number,
			Detail:        "remote-unknown outcome committed; read reconciliation only (same mutation is never repeated)",
		}
		classifyMergeOutcome(result, snap, storedState)
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

		// Persist the merged outcome via the Authority (verified merge
		// evidence drives the transition; no raw CAS remains).
		if _, writeErr := StoreMergeAttempt(homeDir, auth, taskID, MergeOutcomeMerged, storedIdent, snap.MergedSHA, snap.State, result.Detail); writeErr != nil {
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
		if _, writeErr := StoreMergeAttempt(homeDir, auth, taskID, MergeOutcomeOpen, storedIdent, "", snap.State, result.Detail); writeErr != nil {
			return nil, fmt.Errorf("reconcile merge: persisting review-ready: %w", writeErr)
		}

		result.StoredState = string(DeliveryStateReviewReady)
		return result, nil
	}

	// PR is closed but not merged — terminal failure. The failed attempt
	// commits authoritatively (auditable); delivery_state is unchanged.
	result.Outcome = MergeOutcomeFailed
	result.Detail = fmt.Sprintf("PR #%d is closed but not merged (state=%s)", snap.Number, snap.State)
	if _, writeErr := StoreMergeAttempt(homeDir, auth, taskID, MergeOutcomeFailed, storedIdent, "", snap.State, result.Detail); writeErr != nil {
		return nil, fmt.Errorf("reconcile merge: persisting failed attempt: %w", writeErr)
	}
	return result, nil
}

// classifyMergeOutcome fills the outcome fields of a read-only reconciliation
// result from the provider snapshot without mutating anything.
func classifyMergeOutcome(result *MergeDeliveryResult, snap *ProviderSnapshot, storedState string) {
	if snap.Merged {
		result.Outcome = MergeOutcomeAlreadyMerged
		result.Detail = "PR was already merged (read reconciliation: " + fmt.Sprintf("provider confirms PR #%d is merged", snap.Number) + ")"
		return
	}
	if snap.State == "OPEN" {
		result.Outcome = MergeOutcomeOpen
		result.Detail = fmt.Sprintf("PR #%d is still open (head=%s); read reconciliation only", snap.Number, snap.HeadSHA)
		if storedState == string(DeliveryStateMerged) || storedState == string(DeliveryStateDelivered) {
			result.Detail += "; stored state is " + storedState + ", not regressing"
		}
		return
	}
	result.Outcome = MergeOutcomeFailed
	result.Detail = fmt.Sprintf("PR #%d is closed but not merged (state=%s); read reconciliation only", snap.Number, snap.State)
}

// MarkMerged routes the merged transition for a task through the composed
// Task Authority (Task 7.6): the verified merge evidence (identity/head/PR)
// drives a generation-bound merged merge outcome, and the delivery_state
// projection is reconciled as a post-commit projection. The stored identity
// is revalidated against the expected identity (fail-closed on mismatch) and
// the transition is idempotent: when delivery_state is already merged,
// nothing is written. Used by the crash-safe poll retirement path. Production
// callers always supply the Authority; nil fails closed.
func MarkMerged(homeDir, taskID string, expected *domain.DeliveryIdentity, auth *taskauthority.Authority) error {
	if auth == nil {
		return fmt.Errorf("mark merged requires a composed task authority")
	}
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return fmt.Errorf("mark merged: reading meta: %w", err)
	}

	// Idempotent: if already merged, nothing to do.
	if meta[MetaDeliveryState] == string(DeliveryStateMerged) {
		return nil
	}

	stored, err := domain.IdentityFromMeta(meta)
	if err != nil {
		return fmt.Errorf("mark merged: reading stored identity: %w", err)
	}
	if stored == nil {
		return fmt.Errorf("mark merged: no delivery identity for task %s", taskID)
	}
	if expected == nil {
		return fmt.Errorf("mark merged: expected identity is nil")
	}
	storedSnap := snapshotFromIdentity(stored)
	if err := identityMatchesSnapshot(&storedSnap, expected); err != nil {
		return fmt.Errorf("mark merged: identity mismatch: %w", err)
	}
	// The expected head must equal the stored head: the merged transition
	// binds the verified identity, so a stale expected head must never
	// commit (mirrors the legacy identity CAS).
	if expected.HeadSHA != stored.HeadSHA {
		return fmt.Errorf("mark merged: head SHA mismatch: expected=%q stored=%q", expected.HeadSHA, stored.HeadSHA)
	}

	if _, err := StoreMergeAttempt(homeDir, auth, taskID, MergeOutcomeMerged, stored, "", "MERGED", "provider-verified merged"); err != nil {
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
	number int, url, baseRef, headRef, headSHA string, auth *taskauthority.Authority) error {

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
	return MarkMerged(homeDir, taskID, ident, auth)
}
