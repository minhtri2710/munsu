package taskauthority

import (
	"strings"
)

// This file owns the generation-bound merge and git authorization records
// and their named semantic operations (Task 7.4). Every operation commits
// the record inside one Store transaction with the Expected Generation fence
// revalidated inside the transaction, exactly one Revision advance, a typed
// audit event, and the durable idempotency receipt. Authorization writes
// never mutate task .meta directly: .meta is a post-commit projection.

// --- Merge authorization ---

// ProviderIdentitySnapshot captures the point-in-time provider identity a
// merge authorization binds: provider, repository, PR number/URL, base and
// head refs, and the head SHA valid when the authorization was created.
type ProviderIdentitySnapshot struct {
	Provider string `json:"provider"`
	Owner    string `json:"owner"`
	Repo     string `json:"repo"`
	Number   int    `json:"number"`
	URL      string `json:"url"`
	BaseRef  string `json:"baseRef"`
	HeadRef  string `json:"headRef"`
	HeadSHA  string `json:"headSHA"`
}

// MergeAuthorization is the generation-bound record authorizing a merge
// against an exact Task Generation, provider identity, and immutable head
// SHA. It lives inside the Aggregate, so the generation binding is
// structural. A changed head makes the prior authorization stale: the read
// path fails closed with a typed stale error and a fresh authorization must
// acknowledge the prior authorized head (force-with-lease semantics).
type MergeAuthorization struct {
	HeadSHA          string                   `json:"head_sha"`
	ProviderSnapshot ProviderIdentitySnapshot `json:"provider_snapshot"`
	AuthorizedAt     int64                    `json:"authorized_at"`
	Authorizer       string                   `json:"authorizer"`
}

// ExternalMergeRecord is the generation-bound evidence record that the PR/MR
// was merged by an external actor without munsu performing the merge. It
// records external merge truth without fabricating munsu approval.
type ExternalMergeRecord struct {
	MergedSHA   string `json:"merged_sha"`
	MergedAt    int64  `json:"merged_at"`
	MergeSource string `json:"merge_source"`
}

// --- Git authorization ---

// GitCapabilityTier defines the level of git mutation authority a task has.
// The tier is set at launch time and is immutable for the duration of the
// launch. Each tier is a superset of the tiers below it.
type GitCapabilityTier string

const (
	// GitTierRead permits only read-only operations (status, log, diff, fetch).
	GitTierRead GitCapabilityTier = "read"
	// GitTierWrite permits add, commit, normal push, and branch creation.
	GitTierWrite GitCapabilityTier = "write"
	// GitTierRewrite permits force-with-lease, rebase, reset, and amend.
	GitTierRewrite GitCapabilityTier = "rewrite"
	// GitTierCleanup permits branch deletion and remote ref deletion.
	GitTierCleanup GitCapabilityTier = "cleanup"
	// GitTierAdmin permits unrestricted force (never auto-granted).
	GitTierAdmin GitCapabilityTier = "admin"
)

// GitOperation identifies the specific elevated git operation being authorized.
type GitOperation string

const (
	GitOpForceWithLease GitOperation = "force-with-lease"
	GitOpBranchDelete   GitOperation = "branch-delete"
	GitOpPushDelete     GitOperation = "push-delete"
	GitOpRebase         GitOperation = "rebase"
	GitOpReset          GitOperation = "reset"
	GitOpAmendCommit    GitOperation = "amend-commit"
	GitOpCherryPick     GitOperation = "cherry-pick"
	GitOpRevert         GitOperation = "revert"
	GitOpClean          GitOperation = "clean"
)

// GitExpectedState captures the expected state of a ref before mutation: the
// old SHA must match the current state of the ref before the mutation is
// authorized (force-with-lease expected-state comparison).
type GitExpectedState struct {
	Ref    string `json:"ref"`     // e.g., "refs/heads/mu/task-123"
	OldSHA string `json:"old_sha"` // expected current SHA of the ref
	NewSHA string `json:"new_sha"` // expected new SHA of the ref
}

// GitMutationAuthorization is the generation-bound durable authorization
// record for one specific elevated git mutation. It binds the operation to an
// exact expected state and context (amendment, retirement, or standalone),
// providing an auditable trail for every elevated git mutation.
type GitMutationAuthorization struct {
	Operation     GitOperation     `json:"operation"`
	ExpectedState GitExpectedState `json:"expected_state"`
	AuthorizedAt  int64            `json:"authorized_at"`
	Authorizer    string           `json:"authorizer"`
	Context       string           `json:"context"` // "amendment", "retirement", "standalone"
}

// OperationRequiresTier returns the minimum tier required for a git operation.
// Operations not listed require at most GitTierWrite.
func OperationRequiresTier(op GitOperation) GitCapabilityTier {
	switch op {
	case GitOpForceWithLease, GitOpRebase, GitOpReset, GitOpAmendCommit, GitOpCherryPick, GitOpRevert, GitOpClean:
		return GitTierRewrite
	case GitOpBranchDelete, GitOpPushDelete:
		return GitTierCleanup
	default:
		return GitTierWrite
	}
}

// validateAuthorizationDefinition validates the generation-bound merge and
// git authorization records of one Aggregate.
func validateAuthorizationDefinition(agg Aggregate) error {
	if agg.MergeAuthorization != nil {
		if err := validateMergeAuthorizationRecord(*agg.MergeAuthorization); err != nil {
			return err
		}
	}
	if agg.ExternalMerge != nil {
		if err := validateExternalMergeRecord(*agg.ExternalMerge); err != nil {
			return err
		}
	}
	if agg.GitCapabilityTier != "" {
		if err := validateGitCapabilityTier(GitCapabilityTier(agg.GitCapabilityTier)); err != nil {
			return err
		}
	}
	if err := validateGitAuthContext(agg.GitAuthContext); err != nil {
		return err
	}
	if agg.GitMutationAuthorization != nil {
		if err := validateGitMutationAuthorizationRecord(*agg.GitMutationAuthorization); err != nil {
			return err
		}
	}
	return nil
}

// validateMergeAuthorizationRecord checks a generation-bound merge
// authorization: a safe non-empty immutable head SHA, a complete provider
// identity snapshot whose head agrees with the record head, and a bound
// authorizer.
func validateMergeAuthorizationRecord(rec MergeAuthorization) error {
	if err := validateHeadSHA(rec.HeadSHA); err != nil {
		return err
	}
	if err := validateProviderIdentitySnapshot(rec.ProviderSnapshot); err != nil {
		return err
	}
	if rec.ProviderSnapshot.HeadSHA != rec.HeadSHA {
		return validationError("merge authorization head %q does not match identity snapshot head %q", rec.HeadSHA, rec.ProviderSnapshot.HeadSHA)
	}
	if strings.TrimSpace(rec.Authorizer) == "" {
		return validationError("merge authorization missing authorizer")
	}
	if rec.AuthorizedAt <= 0 {
		return validationError("merge authorization missing authorized timestamp")
	}
	return nil
}

// validateExternalMergeRecord checks a generation-bound external merge
// evidence record.
func validateExternalMergeRecord(rec ExternalMergeRecord) error {
	if strings.TrimSpace(rec.MergedSHA) == "" || strings.ContainsAny(rec.MergedSHA, `/\\`) {
		return validationError("external merge record missing safe merged SHA")
	}
	if rec.MergedAt <= 0 {
		return validationError("external merge record missing merged timestamp")
	}
	if strings.TrimSpace(rec.MergeSource) == "" {
		return validationError("external merge record missing merge source")
	}
	return nil
}

// validateGitCapabilityTier accepts the five known capability tiers.
func validateGitCapabilityTier(tier GitCapabilityTier) error {
	switch tier {
	case GitTierRead, GitTierWrite, GitTierRewrite, GitTierCleanup, GitTierAdmin:
		return nil
	}
	return validationError("invalid git capability tier %q", tier)
}

// validateGitAuthContext accepts the amendment and retirement contexts and
// the empty (cleared) context.
func validateGitAuthContext(ctx string) error {
	switch ctx {
	case "", "amendment", "retirement":
		return nil
	}
	return validationError("invalid git authorization context %q", ctx)
}

// validateGitMutationAuthorizationRecord checks one generation-bound elevated
// git mutation authorization: a known elevated operation, the exact expected
// state, a bound authorizer, and a known context.
func validateGitMutationAuthorizationRecord(rec GitMutationAuthorization) error {
	if err := validateElevatedGitOperation(rec.Operation); err != nil {
		return err
	}
	if err := validateGitExpectedState(rec.ExpectedState, rec.Operation); err != nil {
		return err
	}
	if err := validateGitAuthContext(rec.Context); err != nil {
		return err
	}
	if strings.TrimSpace(rec.Authorizer) == "" {
		return validationError("git mutation authorization missing authorizer")
	}
	if rec.AuthorizedAt <= 0 {
		return validationError("git mutation authorization missing authorized timestamp")
	}
	return nil
}

// validateElevatedGitOperation rejects write-tier operations (and unknown
// operations), which never require authorization.
func validateElevatedGitOperation(op GitOperation) error {
	switch op {
	case GitOpForceWithLease, GitOpBranchDelete, GitOpPushDelete, GitOpRebase, GitOpReset, GitOpAmendCommit, GitOpCherryPick, GitOpRevert, GitOpClean:
		return nil
	}
	return validationError("git mutation operation %q does not require authorization (tier %s)", op, OperationRequiresTier(op))
}

// validateGitExpectedState checks the expected state of one elevated git
// mutation: the ref and old SHA are required, and the new SHA is required
// except for branch/push deletion.
func validateGitExpectedState(state GitExpectedState, op GitOperation) error {
	if strings.TrimSpace(state.Ref) == "" {
		return validationError("git mutation authorization missing expected ref")
	}
	if strings.TrimSpace(state.OldSHA) == "" {
		return validationError("git mutation authorization missing expected old SHA")
	}
	if state.NewSHA == "" && op != GitOpBranchDelete && op != GitOpPushDelete {
		return validationError("git mutation authorization expected new SHA is required for operation %q", op)
	}
	return nil
}

// validateHeadSHA accepts a safe non-empty head identity (no path separators).
func validateHeadSHA(head string) error {
	if head == "" || head != strings.TrimSpace(head) || strings.ContainsAny(head, `/\\`) {
		return validationError("invalid head SHA %q", head)
	}
	return nil
}

// validateProviderIdentitySnapshot checks a provider identity snapshot: all
// identity fields required and safe.
func validateProviderIdentitySnapshot(snap ProviderIdentitySnapshot) error {
	if strings.TrimSpace(snap.Provider) == "" {
		return validationError("provider identity snapshot missing provider")
	}
	if strings.TrimSpace(snap.Owner) == "" {
		return validationError("provider identity snapshot missing owner")
	}
	if strings.TrimSpace(snap.Repo) == "" {
		return validationError("provider identity snapshot missing repo")
	}
	if snap.Number <= 0 {
		return validationError("provider identity snapshot missing PR number")
	}
	if strings.TrimSpace(snap.URL) == "" || strings.ContainsAny(snap.URL, `\`) {
		return validationError("provider identity snapshot missing safe URL")
	}
	if strings.TrimSpace(snap.BaseRef) == "" {
		return validationError("provider identity snapshot missing base ref")
	}
	if strings.TrimSpace(snap.HeadRef) == "" {
		return validationError("provider identity snapshot missing head ref")
	}
	if err := validateHeadSHA(snap.HeadSHA); err != nil {
		return err
	}
	return nil
}

// --- AuthorizeMerge ---

// AuthorizeMergeRequest is the immutable request payload of one
// generation-bound merge authorization. It carries the exact Task Generation
// fence, the stable Task Operation identity, the actor, the provider identity
// snapshot, the immutable head SHA being authorized, and the expected prior
// authorized head ("" for the first authorization). The Operation ID is
// excluded from the intent digest, so a retry that changes the head or the
// identity under the same ID detects a conflict.
type AuthorizeMergeRequest struct {
	OperationID        string
	Actor              Actor
	TaskID             string
	ExpectedGeneration Generation
	HeadSHA            string
	Identity           ProviderIdentitySnapshot
	ExpectedPriorHead  string
	Reason             string
}

// AuthorizationResult is the caller-visible outcome of one authorization
// operation. Replayed is true when the Operation ID was already committed
// with the same intent.
type AuthorizationResult struct {
	TaskID     string
	Generation Generation
	Revision   Revision
	Phase      Phase
	Replayed   bool
}

// AuthorizeMerge is the named semantic operation that commits the
// generation-bound merge authorization record in one Store transaction: the
// Expected Generation fence is revalidated inside the transaction, the prior
// authorized head binding is enforced (a changed head invalidates the stale
// authorization — a fresh authorization must acknowledge the committed prior
// head, and reusing the Operation ID with a changed head is a typed
// non-retryable conflict; never silent reuse), the Revision advances by
// exactly one, a typed merge-authorization audit event commits, and the
// durable idempotency receipt pins the intent. Same-op replay is idempotent;
// a stale or missing task fails closed.
func (a *Authority) AuthorizeMerge(req AuthorizeMergeRequest) (AuthorizationResult, error) {
	if err := req.ExpectedGeneration.Validate(); err != nil {
		return AuthorizationResult{}, err
	}
	if err := validateAuthorizeMergeRequest(req); err != nil {
		return AuthorizationResult{}, err
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID             string                   `json:"task_id"`
		ExpectedGeneration uint64                   `json:"expected_generation"`
		HeadSHA            string                   `json:"head_sha"`
		Identity           ProviderIdentitySnapshot `json:"identity"`
		ExpectedPriorHead  string                   `json:"expected_prior_head,omitempty"`
		Reason             string                   `json:"reason,omitempty"`
	}{req.TaskID, uint64(req.ExpectedGeneration), req.HeadSHA, req.Identity, req.ExpectedPriorHead, req.Reason})
	if err != nil {
		return AuthorizationResult{}, err
	}
	receipt, err := a.store.Update(op, func(tx *Tx) error {
		cur, ok := tx.Current(req.TaskID)
		if !ok {
			return conflictError(ErrNotFound, "task %s not found", req.TaskID)
		}
		if cur.Generation != req.ExpectedGeneration {
			return conflictError(ErrConflict, "task %s is at generation %s, expected %s", req.TaskID, cur.Generation, req.ExpectedGeneration)
		}
		prior := ""
		if cur.MergeAuthorization != nil {
			prior = cur.MergeAuthorization.HeadSHA
		}
		if prior != req.ExpectedPriorHead {
			return conflictError(ErrConflict, "task %s generation %s merge authorization prior head %q does not match expected %q: a changed head invalidates the stale authorization (re-authorize explicitly)", req.TaskID, cur.Generation, prior, req.ExpectedPriorHead)
		}
		updated := cur.clone()
		rec := MergeAuthorization{
			HeadSHA:          req.HeadSHA,
			ProviderSnapshot: req.Identity,
			AuthorizedAt:     a.now().UnixNano(),
			Authorizer:       req.Actor.ID,
		}
		updated.MergeAuthorization = &rec
		updated.Revision++
		if err := tx.PutAggregate(updated); err != nil {
			return err
		}
		return tx.AppendAudit(AuditEvent{
			OperationID: op.ID,
			Actor:       op.Actor,
			Kind:        AuditMergeAuthorization,
			TaskID:      cur.TaskID,
			Generation:  cur.Generation,
			Reason:      req.Reason,
			At:          a.now().UnixNano(),
		})
	})
	if err != nil {
		return AuthorizationResult{}, err
	}
	return authorizeMergeResultFromReceipt(receipt), nil
}

func authorizeMergeResultFromReceipt(receipt Receipt) AuthorizationResult {
	return AuthorizationResult{
		TaskID:     receipt.TaskID,
		Generation: receipt.Generation,
		Revision:   receipt.Revision,
		Phase:      receipt.Phase,
		Replayed:   receipt.Replayed,
	}
}

// validateAuthorizeMergeRequest validates one merge authorization request:
// a safe head and a complete provider identity snapshot whose head agrees
// with the requested head.
func validateAuthorizeMergeRequest(req AuthorizeMergeRequest) error {
	if err := validateHeadSHA(req.HeadSHA); err != nil {
		return err
	}
	if err := validateProviderIdentitySnapshot(req.Identity); err != nil {
		return err
	}
	if req.Identity.HeadSHA != req.HeadSHA {
		return validationError("merge authorization head %q does not match identity snapshot head %q", req.HeadSHA, req.Identity.HeadSHA)
	}
	return nil
}

// --- RecordExternalMerge ---

// RecordExternalMergeRequest is the immutable request payload of one
// generation-bound external merge evidence commit.
type RecordExternalMergeRequest struct {
	OperationID        string
	Actor              Actor
	TaskID             string
	ExpectedGeneration Generation
	MergedSHA          string
	Identity           ProviderIdentitySnapshot
	ExpectedPriorHead  string
	Reason             string
}

// RecordExternalMerge is the named semantic operation that commits the
// generation-bound external merge evidence record in one Store transaction
// (generation fence, one Revision advance, typed audit, durable receipt).
// The record is bounded: a generation accepts one external merge evidence
// record, so a second record under a fresh Operation ID fails closed even
// with identical content; same-op replay is idempotent. The identity binds
// the exact provider/PR/head the evidence describes; a changed head
// invalidates a stale prior merge authorization exactly as AuthorizeMerge
// does. (The delivery_state=merged transition that the legacy fleet function
// also performed is Task 7.6 and is not part of this record.)
func (a *Authority) RecordExternalMerge(req RecordExternalMergeRequest) (AuthorizationResult, error) {
	if err := req.ExpectedGeneration.Validate(); err != nil {
		return AuthorizationResult{}, err
	}
	if err := validateRecordExternalMergeRequest(req); err != nil {
		return AuthorizationResult{}, err
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID             string                   `json:"task_id"`
		ExpectedGeneration uint64                   `json:"expected_generation"`
		MergedSHA          string                   `json:"merged_sha"`
		Identity           ProviderIdentitySnapshot `json:"identity"`
		ExpectedPriorHead  string                   `json:"expected_prior_head,omitempty"`
		Reason             string                   `json:"reason,omitempty"`
	}{req.TaskID, uint64(req.ExpectedGeneration), req.MergedSHA, req.Identity, req.ExpectedPriorHead, req.Reason})
	if err != nil {
		return AuthorizationResult{}, err
	}
	receipt, err := a.store.Update(op, func(tx *Tx) error {
		cur, ok := tx.Current(req.TaskID)
		if !ok {
			return conflictError(ErrNotFound, "task %s not found", req.TaskID)
		}
		if cur.Generation != req.ExpectedGeneration {
			return conflictError(ErrConflict, "task %s is at generation %s, expected %s", req.TaskID, cur.Generation, req.ExpectedGeneration)
		}
		if cur.ExternalMerge != nil {
			return conflictError(ErrConflict, "task %s generation %s already has an external merge record; one record per generation", req.TaskID, cur.Generation)
		}
		prior := ""
		if cur.MergeAuthorization != nil {
			prior = cur.MergeAuthorization.HeadSHA
		}
		if prior != req.ExpectedPriorHead {
			return conflictError(ErrConflict, "task %s generation %s merge authorization prior head %q does not match expected %q: a changed head invalidates the stale authorization", req.TaskID, cur.Generation, prior, req.ExpectedPriorHead)
		}
		updated := cur.clone()
		rec := ExternalMergeRecord{
			MergedSHA:   req.MergedSHA,
			MergedAt:    a.now().UnixNano(),
			MergeSource: "external",
		}
		updated.ExternalMerge = &rec
		updated.Revision++
		if err := tx.PutAggregate(updated); err != nil {
			return err
		}
		return tx.AppendAudit(AuditEvent{
			OperationID: op.ID,
			Actor:       op.Actor,
			Kind:        AuditMergeAuthorization,
			TaskID:      cur.TaskID,
			Generation:  cur.Generation,
			Reason:      req.Reason,
			At:          a.now().UnixNano(),
		})
	})
	if err != nil {
		return AuthorizationResult{}, err
	}
	return authorizeMergeResultFromReceipt(receipt), nil
}

func validateRecordExternalMergeRequest(req RecordExternalMergeRequest) error {
	if strings.TrimSpace(req.MergedSHA) == "" || strings.ContainsAny(req.MergedSHA, `/\\`) {
		return validationError("external merge record missing safe merged SHA")
	}
	if err := validateProviderIdentitySnapshot(req.Identity); err != nil {
		return err
	}
	return nil
}

// --- SetGitCapabilityTier ---

// SetGitCapabilityTierRequest is the immutable request payload of one
// generation-bound git capability tier binding.
type SetGitCapabilityTierRequest struct {
	OperationID        string
	Actor              Actor
	TaskID             string
	ExpectedGeneration Generation
	Tier               GitCapabilityTier
	ExpectedPriorTier  string
	Reason             string
}

// SetGitCapabilityTier is the named semantic operation that binds the git
// capability tier to the exact generation and worktree binding: it requires
// the task to carry a worktree binding (the tier is worktree mutation
// authority), enforces the expected prior tier pre-state (else typed
// conflict), and commits the tier with one Revision advance and a typed
// git-authorization audit event. The tier is immutable within a generation:
// a different tier on an already-bound generation conflicts, and re-setting
// the current tier is an in-value no-op that does not advance the Revision.
func (a *Authority) SetGitCapabilityTier(req SetGitCapabilityTierRequest) (AuthorizationResult, error) {
	if err := req.ExpectedGeneration.Validate(); err != nil {
		return AuthorizationResult{}, err
	}
	if err := validateGitCapabilityTier(req.Tier); err != nil {
		return AuthorizationResult{}, err
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID             string `json:"task_id"`
		ExpectedGeneration uint64 `json:"expected_generation"`
		Tier               string `json:"tier"`
		ExpectedPriorTier  string `json:"expected_prior_tier,omitempty"`
		Reason             string `json:"reason,omitempty"`
	}{req.TaskID, uint64(req.ExpectedGeneration), string(req.Tier), req.ExpectedPriorTier, req.Reason})
	if err != nil {
		return AuthorizationResult{}, err
	}
	receipt, err := a.store.Update(op, func(tx *Tx) error {
		cur, ok := tx.Current(req.TaskID)
		if !ok {
			return conflictError(ErrNotFound, "task %s not found", req.TaskID)
		}
		if cur.Generation != req.ExpectedGeneration {
			return conflictError(ErrConflict, "task %s is at generation %s, expected %s", req.TaskID, cur.Generation, req.ExpectedGeneration)
		}
		if cur.Worktree == nil {
			return conflictError(ErrPrecondition, "task %s generation %s has no worktree binding; the git capability tier binds a bound worktree", req.TaskID, cur.Generation)
		}
		if cur.GitCapabilityTier != req.ExpectedPriorTier {
			return conflictError(ErrConflict, "task %s generation %s git capability tier %q does not match expected prior %q", req.TaskID, cur.Generation, cur.GitCapabilityTier, req.ExpectedPriorTier)
		}
		if cur.GitCapabilityTier == string(req.Tier) {
			return nil // in-value no-op: already bound to this tier
		}
		if cur.GitCapabilityTier != "" {
			return conflictError(ErrConflict, "task %s generation %s git capability tier is immutable within the generation (already %q)", req.TaskID, cur.Generation, cur.GitCapabilityTier)
		}
		updated := cur.clone()
		updated.GitCapabilityTier = string(req.Tier)
		updated.Revision++
		if err := tx.PutAggregate(updated); err != nil {
			return err
		}
		return tx.AppendAudit(AuditEvent{
			OperationID: op.ID,
			Actor:       op.Actor,
			Kind:        AuditGitAuthorization,
			TaskID:      cur.TaskID,
			Generation:  cur.Generation,
			Reason:      req.Reason,
			At:          a.now().UnixNano(),
		})
	})
	if err != nil {
		return AuthorizationResult{}, err
	}
	return a.authorizationResultFromReceipt(req.TaskID, receipt)
}

// --- SetGitAuthContext ---

// SetGitAuthContextRequest is the immutable request payload of one
// generation-bound git authorization context binding (amendment, retirement,
// or cleared).
type SetGitAuthContextRequest struct {
	OperationID          string
	Actor                Actor
	TaskID               string
	ExpectedGeneration   Generation
	Context              string
	ExpectedPriorContext string
	Reason               string
}

// SetGitAuthContext is the named semantic operation that binds the git
// authorization context (amendment, retirement, or cleared) to the exact
// generation and worktree binding: it requires a worktree binding, enforces
// the expected prior context pre-state (else typed conflict), and commits
// the context with one Revision advance and a typed git-authorization audit
// event. Setting the context to its current value is an in-value no-op that
// does not advance the Revision.
func (a *Authority) SetGitAuthContext(req SetGitAuthContextRequest) (AuthorizationResult, error) {
	if err := req.ExpectedGeneration.Validate(); err != nil {
		return AuthorizationResult{}, err
	}
	if err := validateGitAuthContext(req.Context); err != nil {
		return AuthorizationResult{}, err
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID               string `json:"task_id"`
		ExpectedGeneration   uint64 `json:"expected_generation"`
		Context              string `json:"context"`
		ExpectedPriorContext string `json:"expected_prior_context,omitempty"`
		Reason               string `json:"reason,omitempty"`
	}{req.TaskID, uint64(req.ExpectedGeneration), req.Context, req.ExpectedPriorContext, req.Reason})
	if err != nil {
		return AuthorizationResult{}, err
	}
	receipt, err := a.store.Update(op, func(tx *Tx) error {
		cur, ok := tx.Current(req.TaskID)
		if !ok {
			return conflictError(ErrNotFound, "task %s not found", req.TaskID)
		}
		if cur.Generation != req.ExpectedGeneration {
			return conflictError(ErrConflict, "task %s is at generation %s, expected %s", req.TaskID, cur.Generation, req.ExpectedGeneration)
		}
		if cur.Worktree == nil {
			return conflictError(ErrPrecondition, "task %s generation %s has no worktree binding; the git authorization context binds a bound worktree", req.TaskID, cur.Generation)
		}
		if cur.GitAuthContext != req.ExpectedPriorContext {
			return conflictError(ErrConflict, "task %s generation %s git authorization context %q does not match expected prior %q", req.TaskID, cur.Generation, cur.GitAuthContext, req.ExpectedPriorContext)
		}
		if cur.GitAuthContext == req.Context {
			return nil // in-value no-op: already in this context
		}
		updated := cur.clone()
		updated.GitAuthContext = req.Context
		updated.Revision++
		if err := tx.PutAggregate(updated); err != nil {
			return err
		}
		return tx.AppendAudit(AuditEvent{
			OperationID: op.ID,
			Actor:       op.Actor,
			Kind:        AuditGitAuthorization,
			TaskID:      cur.TaskID,
			Generation:  cur.Generation,
			Reason:      req.Reason,
			At:          a.now().UnixNano(),
		})
	})
	if err != nil {
		return AuthorizationResult{}, err
	}
	return a.authorizationResultFromReceipt(req.TaskID, receipt)
}

// --- AuthorizeGitMutation / ClearGitMutationAuthorization ---

// AuthorizeGitMutationRequest is the immutable request payload of one
// generation-bound elevated git mutation authorization.
type AuthorizeGitMutationRequest struct {
	OperationID          string
	Actor                Actor
	TaskID               string
	ExpectedGeneration   Generation
	Op                   GitOperation
	ExpectedState        GitExpectedState
	Authorizer           string
	Context              string
	ExpectedPriorContext string
	Reason               string
}

// AuthorizeGitMutation is the named semantic operation that commits the
// generation-bound elevated git mutation authorization record in one Store
// transaction: it requires a worktree binding, enforces the expected prior
// context pre-state (the authorization is only valid against the context the
// caller observed, else typed conflict), advances the Revision by exactly
// one, and emits a typed git-authorization audit event. Write-tier
// operations are never authorized; same-op replay is idempotent.
func (a *Authority) AuthorizeGitMutation(req AuthorizeGitMutationRequest) (AuthorizationResult, error) {
	if err := req.ExpectedGeneration.Validate(); err != nil {
		return AuthorizationResult{}, err
	}
	if err := validateAuthorizeGitMutationRequest(req); err != nil {
		return AuthorizationResult{}, err
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID               string           `json:"task_id"`
		ExpectedGeneration   uint64           `json:"expected_generation"`
		Op                   GitOperation     `json:"op"`
		ExpectedState        GitExpectedState `json:"expected_state"`
		Authorizer           string           `json:"authorizer"`
		Context              string           `json:"context"`
		ExpectedPriorContext string           `json:"expected_prior_context,omitempty"`
		Reason               string           `json:"reason,omitempty"`
	}{req.TaskID, uint64(req.ExpectedGeneration), req.Op, req.ExpectedState, req.Authorizer, req.Context, req.ExpectedPriorContext, req.Reason})
	if err != nil {
		return AuthorizationResult{}, err
	}
	receipt, err := a.store.Update(op, func(tx *Tx) error {
		cur, ok := tx.Current(req.TaskID)
		if !ok {
			return conflictError(ErrNotFound, "task %s not found", req.TaskID)
		}
		if cur.Generation != req.ExpectedGeneration {
			return conflictError(ErrConflict, "task %s is at generation %s, expected %s", req.TaskID, cur.Generation, req.ExpectedGeneration)
		}
		if cur.Worktree == nil {
			return conflictError(ErrPrecondition, "task %s generation %s has no worktree binding; a git mutation authorization binds a bound worktree", req.TaskID, cur.Generation)
		}
		if cur.GitAuthContext != req.ExpectedPriorContext {
			return conflictError(ErrConflict, "task %s generation %s git authorization context %q does not match expected prior %q", req.TaskID, cur.Generation, cur.GitAuthContext, req.ExpectedPriorContext)
		}
		updated := cur.clone()
		rec := GitMutationAuthorization{
			Operation:     req.Op,
			ExpectedState: req.ExpectedState,
			AuthorizedAt:  a.now().UnixNano(),
			Authorizer:    req.Authorizer,
			Context:       req.Context,
		}
		updated.GitMutationAuthorization = &rec
		updated.Revision++
		if err := tx.PutAggregate(updated); err != nil {
			return err
		}
		return tx.AppendAudit(AuditEvent{
			OperationID: op.ID,
			Actor:       op.Actor,
			Kind:        AuditGitAuthorization,
			TaskID:      cur.TaskID,
			Generation:  cur.Generation,
			Reason:      req.Reason,
			At:          a.now().UnixNano(),
		})
	})
	if err != nil {
		return AuthorizationResult{}, err
	}
	return a.authorizationResultFromReceipt(req.TaskID, receipt)
}

func validateAuthorizeGitMutationRequest(req AuthorizeGitMutationRequest) error {
	if err := validateElevatedGitOperation(req.Op); err != nil {
		return err
	}
	if err := validateGitExpectedState(req.ExpectedState, req.Op); err != nil {
		return err
	}
	if err := validateGitAuthContext(req.Context); err != nil {
		return err
	}
	if strings.TrimSpace(req.Authorizer) == "" {
		return validationError("git mutation authorization missing authorizer")
	}
	return nil
}

// ClearGitMutationAuthorizationRequest is the immutable request payload of
// one generation-bound git mutation authorization clear.
type ClearGitMutationAuthorizationRequest struct {
	OperationID        string
	Actor              Actor
	TaskID             string
	ExpectedGeneration Generation
	ExpectedPriorOp    GitOperation
	Reason             string
}

// ClearGitMutationAuthorization is the named semantic operation that removes
// the generation-bound elevated git mutation authorization in one Store
// transaction. The expected prior operation binds the pre-state: expecting a
// record that is absent, or a different operation than the committed record,
// is a typed conflict. Clearing an absent authorization is an in-value no-op
// that does not advance the Revision.
func (a *Authority) ClearGitMutationAuthorization(req ClearGitMutationAuthorizationRequest) (AuthorizationResult, error) {
	if err := req.ExpectedGeneration.Validate(); err != nil {
		return AuthorizationResult{}, err
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID             string       `json:"task_id"`
		ExpectedGeneration uint64       `json:"expected_generation"`
		ExpectedPriorOp    GitOperation `json:"expected_prior_op,omitempty"`
		Reason             string       `json:"reason,omitempty"`
	}{req.TaskID, uint64(req.ExpectedGeneration), req.ExpectedPriorOp, req.Reason})
	if err != nil {
		return AuthorizationResult{}, err
	}
	receipt, err := a.store.Update(op, func(tx *Tx) error {
		cur, ok := tx.Current(req.TaskID)
		if !ok {
			return conflictError(ErrNotFound, "task %s not found", req.TaskID)
		}
		if cur.Generation != req.ExpectedGeneration {
			return conflictError(ErrConflict, "task %s is at generation %s, expected %s", req.TaskID, cur.Generation, req.ExpectedGeneration)
		}
		if cur.Worktree == nil {
			return conflictError(ErrPrecondition, "task %s generation %s has no worktree binding; a git mutation authorization binds a bound worktree", req.TaskID, cur.Generation)
		}
		if cur.GitMutationAuthorization == nil {
			if req.ExpectedPriorOp != "" {
				return conflictError(ErrConflict, "task %s generation %s has no git mutation authorization to clear (expected prior operation %q)", req.TaskID, cur.Generation, req.ExpectedPriorOp)
			}
			return nil // in-value no-op: already cleared
		}
		if cur.GitMutationAuthorization.Operation != req.ExpectedPriorOp {
			return conflictError(ErrConflict, "task %s generation %s git mutation authorization is for operation %q, not expected prior %q", req.TaskID, cur.Generation, cur.GitMutationAuthorization.Operation, req.ExpectedPriorOp)
		}
		updated := cur.clone()
		updated.GitMutationAuthorization = nil
		updated.Revision++
		if err := tx.PutAggregate(updated); err != nil {
			return err
		}
		return tx.AppendAudit(AuditEvent{
			OperationID: op.ID,
			Actor:       op.Actor,
			Kind:        AuditGitAuthorization,
			TaskID:      cur.TaskID,
			Generation:  cur.Generation,
			Reason:      req.Reason,
			At:          a.now().UnixNano(),
		})
	})
	if err != nil {
		return AuthorizationResult{}, err
	}
	return a.authorizationResultFromReceipt(req.TaskID, receipt)
}

// authorizationResultFromReceipt returns the caller-visible outcome of one
// authorization operation from the committed receipt. When the operation
// committed no staged change (an in-value no-op such as re-setting the same
// git auth context), the outcome is read back from the committed aggregate.
func (a *Authority) authorizationResultFromReceipt(taskID string, receipt Receipt) (AuthorizationResult, error) {
	if receipt.TaskID != "" || receipt.Generation != 0 || receipt.Revision != 0 {
		return authorizeMergeResultFromReceipt(receipt), nil
	}
	agg, err := a.Get(taskID)
	if err != nil {
		return AuthorizationResult{}, err
	}
	return AuthorizationResult{
		TaskID:     agg.TaskID,
		Generation: agg.Generation,
		Revision:   agg.Revision,
		Phase:      agg.Phase,
	}, nil
}
