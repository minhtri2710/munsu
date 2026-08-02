package fleet

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// This file routes merge and git authorization writes through the composed
// Task Authority (Task 7.4): the authoritative records commit as
// generation-bound Aggregate records, and the .meta keys are reconciled as
// post-commit projections. Production callers always supply the Authority;
// nil fails closed. A projection failure returns a typed partial error and
// never rolls back the authoritative commit (ADR-0007 §7).

// AuthorizationProjectionError is the typed partial outcome of an
// authorization whose authoritative commit succeeded but whose .meta
// projection could not be written. The authoritative state is never rolled
// back; the projection can be retried independently and replays idempotently.
type AuthorizationProjectionError struct {
	TaskID        string
	ProjectionErr error
}

func (e *AuthorizationProjectionError) Error() string {
	return fmt.Sprintf("authorization committed for %s but projection failed: %v", e.TaskID, e.ProjectionErr)
}

func (e *AuthorizationProjectionError) Unwrap() error { return e.ProjectionErr }

// mustDeliveryOperationID mints a stable random Task Operation identity for
// one delivery authorization invocation (ADR-0007 §6: composition supplies an
// invocation identity without making operators invent IDs).
func mustDeliveryOperationID(prefix string) string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%x", prefix, buf[:])
}

// AuthorizeMerge routes the merge authorization through the composed Task
// Authority targeting the exact resolved task home (cross-home delivery). It
// validates the request identity against the stored delivery identity, then
// commits the generation-bound merge authorization record (immutable head SHA
// + provider identity snapshot) fenced to the aggregate generation, and
// reconciles the .meta merge_authorization projection. A changed head makes a
// stale prior authorization invalid: the operation conflicts unless the
// committed prior head is acknowledged (never silent reuse), and same-op
// replay is idempotent.
func AuthorizeMerge(homeDir string, auth *taskauthority.Authority, taskID string, expected *domain.DeliveryIdentity) (taskauthority.AuthorizationResult, error) {
	if auth == nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("merge authorization requires a composed task authority")
	}
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("authorize merge: reading meta: %w", err)
	}
	stored, err := domain.IdentityFromMeta(meta)
	if err != nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("authorize merge: reading stored identity: %w", err)
	}
	if stored == nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("authorize merge: no delivery identity for task %s; run pr-check first", taskID)
	}
	if expected == nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("authorize merge: expected identity is nil")
	}
	if stored.HeadSHA != expected.HeadSHA {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("authorize merge: identity mismatch: stored head SHA %q differs from provided %q", stored.HeadSHA, expected.HeadSHA)
	}

	agg, err := auth.Get(taskID)
	if err != nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("authorize merge: resolving task generation: %w", err)
	}
	priorHead := ""
	if agg.MergeAuthorization != nil {
		priorHead = agg.MergeAuthorization.HeadSHA
	}
	res, err := auth.AuthorizeMerge(taskauthority.AuthorizeMergeRequest{
		OperationID:        mustDeliveryOperationID("merge-authorize-" + taskID),
		Actor:              deliveryActor(homeDir),
		TaskID:             taskID,
		ExpectedGeneration: agg.Generation,
		HeadSHA:            stored.HeadSHA,
		Identity:           snapshotFromIdentity(stored),
		ExpectedPriorHead:  priorHead,
		Reason:             "merge authorization",
	})
	if err != nil {
		return taskauthority.AuthorizationResult{}, err
	}
	committed, err := auth.Get(taskID)
	if err != nil {
		return res, &AuthorizationProjectionError{TaskID: taskID, ProjectionErr: err}
	}
	if err := projectMergeAuthorization(homeDir, taskID, committed.MergeAuthorization); err != nil {
		return res, err
	}
	return res, nil
}

// RecordExternalMerge routes the external merge evidence record through the
// composed Task Authority. It validates the stored delivery identity against
// the provided identity, then commits the generation-bound external merge
// evidence record (bounded: one record per generation; same-op replay
// idempotent) and reconciles the .meta external_merge projection. The
// delivery_state=merged transition the legacy function also performed now
// commits via the Authority (Task 7.6): the verified external merge evidence
// drives a generation-bound merged merge outcome, and delivery_state is a
// post-commit projection.
func RecordExternalMerge(homeDir string, auth *taskauthority.Authority, taskID, mergedSHA string, expected *domain.DeliveryIdentity) (taskauthority.AuthorizationResult, error) {
	if auth == nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("external merge record requires a composed task authority")
	}
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("record external merge: reading meta: %w", err)
	}
	stored, err := domain.IdentityFromMeta(meta)
	if err != nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("record external merge: reading stored identity: %w", err)
	}
	if stored == nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("record external merge: no delivery identity for task %s; run pr-check first", taskID)
	}
	if expected == nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("record external merge: expected identity is nil")
	}
	storedSnap := snapshotFromIdentity(stored)
	if err := identityMatchesSnapshot(&storedSnap, expected); err != nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("record external merge: identity mismatch: %w", err)
	}

	agg, err := auth.Get(taskID)
	if err != nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("record external merge: resolving task generation: %w", err)
	}
	var res taskauthority.AuthorizationResult
	if agg.ExternalMerge == nil {
		priorHead := ""
		if agg.MergeAuthorization != nil {
			priorHead = agg.MergeAuthorization.HeadSHA
		}
		res, err = auth.RecordExternalMerge(taskauthority.RecordExternalMergeRequest{
			OperationID:        mustDeliveryOperationID("merge-ext-record-" + taskID),
			Actor:              deliveryActor(homeDir),
			TaskID:             taskID,
			ExpectedGeneration: agg.Generation,
			MergedSHA:          mergedSHA,
			Identity:           storedSnap,
			ExpectedPriorHead:  priorHead,
			Reason:             "external merge",
		})
		if err != nil {
			return taskauthority.AuthorizationResult{}, err
		}
		committed, err := auth.Get(taskID)
		if err != nil {
			return res, &AuthorizationProjectionError{TaskID: taskID, ProjectionErr: err}
		}
		if err := projectExternalMerge(homeDir, taskID, committed.ExternalMerge); err != nil {
			return res, err
		}
	}

	// The merged-state transition (deferred from Task 7.4) now commits via
	// the Authority: the verified external merge evidence (identity/head/
	// merged SHA) drives the generation-bound merged merge outcome, and
	// delivery_state=merged is a post-commit projection. An already-merged
	// re-attempt is idempotent.
	if _, err := StoreMergeAttempt(homeDir, auth, taskID, MergeOutcomeMerged, stored, mergedSHA, "MERGED", "external merge"); err != nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("record external merge: merged transition: %w", err)
	}
	return res, nil
}

// StoreGitCapabilityTier routes the launch capability tier through the
// composed Task Authority and reconciles the .meta projection. The tier is
// bound to the exact generation and worktree binding and is immutable within
// the generation; re-setting the current tier is an in-value no-op.
func StoreGitCapabilityTier(homeDir string, auth *taskauthority.Authority, taskID string, tier taskauthority.GitCapabilityTier) (taskauthority.AuthorizationResult, error) {
	if auth == nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("git capability tier requires a composed task authority")
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("store git capability tier: resolving task generation: %w", err)
	}
	res, err := auth.SetGitCapabilityTier(taskauthority.SetGitCapabilityTierRequest{
		OperationID:        mustDeliveryOperationID("git-tier-" + taskID),
		Actor:              deliveryActor(homeDir),
		TaskID:             taskID,
		ExpectedGeneration: agg.Generation,
		Tier:               tier,
		ExpectedPriorTier:  agg.GitCapabilityTier,
		Reason:             "launch capability tier",
	})
	if err != nil {
		return taskauthority.AuthorizationResult{}, err
	}
	if err := projectGitCapabilityTier(homeDir, taskID, string(tier)); err != nil {
		return res, err
	}
	return res, nil
}

// StoreGitAuthContext routes the git authorization context (amendment,
// retirement, or cleared) through the composed Task Authority and reconciles
// the .meta projection. The context binds the exact generation and worktree
// binding with the expected prior context revalidated inside the operation;
// setting the current context is an in-value no-op.
func StoreGitAuthContext(homeDir string, auth *taskauthority.Authority, taskID, context string) (taskauthority.AuthorizationResult, error) {
	if auth == nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("git authorization context requires a composed task authority")
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("store git auth context: resolving task generation: %w", err)
	}
	res, err := auth.SetGitAuthContext(taskauthority.SetGitAuthContextRequest{
		OperationID:          mustDeliveryOperationID("git-auth-context-" + taskID),
		Actor:                deliveryActor(homeDir),
		TaskID:               taskID,
		ExpectedGeneration:   agg.Generation,
		Context:              context,
		ExpectedPriorContext: agg.GitAuthContext,
		Reason:               "git authorization context",
	})
	if err != nil {
		return taskauthority.AuthorizationResult{}, err
	}
	if err := projectGitAuthContext(homeDir, taskID, context); err != nil {
		return res, err
	}
	return res, nil
}

// StoreGitMutationAuthorization routes an elevated git mutation authorization
// through the composed Task Authority and reconciles the .meta projection.
// The authorization binds the exact generation, worktree binding, and the
// expected prior context; write-tier operations are never authorized.
func StoreGitMutationAuthorization(homeDir string, auth *taskauthority.Authority, taskID string, op taskauthority.GitOperation, expected taskauthority.GitExpectedState, authorizer, context string) (taskauthority.AuthorizationResult, error) {
	if auth == nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("git mutation authorization requires a composed task authority")
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("store git mutation authorization: resolving task generation: %w", err)
	}
	res, err := auth.AuthorizeGitMutation(taskauthority.AuthorizeGitMutationRequest{
		OperationID:          mustDeliveryOperationID("git-mutation-auth-" + taskID),
		Actor:                deliveryActor(homeDir),
		TaskID:               taskID,
		ExpectedGeneration:   agg.Generation,
		Op:                   op,
		ExpectedState:        expected,
		Authorizer:           authorizer,
		Context:              context,
		ExpectedPriorContext: agg.GitAuthContext,
		Reason:               "elevated git mutation",
	})
	if err != nil {
		return taskauthority.AuthorizationResult{}, err
	}
	committed, err := auth.Get(taskID)
	if err != nil {
		return res, &AuthorizationProjectionError{TaskID: taskID, ProjectionErr: err}
	}
	if err := projectGitMutationAuthorization(homeDir, taskID, committed.GitMutationAuthorization); err != nil {
		return res, err
	}
	return res, nil
}

// ClearStoredGitMutationAuthorization routes the git mutation authorization
// clear through the composed Task Authority and reconciles the .meta
// projection (cleared). Clearing an absent authorization is an in-value
// no-op.
func ClearStoredGitMutationAuthorization(homeDir string, auth *taskauthority.Authority, taskID string) (taskauthority.AuthorizationResult, error) {
	if auth == nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("clear git mutation authorization requires a composed task authority")
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		return taskauthority.AuthorizationResult{}, fmt.Errorf("clear git mutation authorization: resolving task generation: %w", err)
	}
	priorOp := taskauthority.GitOperation("")
	if agg.GitMutationAuthorization != nil {
		priorOp = agg.GitMutationAuthorization.Operation
	}
	res, err := auth.ClearGitMutationAuthorization(taskauthority.ClearGitMutationAuthorizationRequest{
		OperationID:        mustDeliveryOperationID("git-mutation-clear-" + taskID),
		Actor:              deliveryActor(homeDir),
		TaskID:             taskID,
		ExpectedGeneration: agg.Generation,
		ExpectedPriorOp:    priorOp,
		Reason:             "git mutation completed",
	})
	if err != nil {
		return taskauthority.AuthorizationResult{}, err
	}
	if err := projectGitMutationAuthorization(homeDir, taskID, nil); err != nil {
		return res, err
	}
	return res, nil
}

// --- Projection helpers (caller-owned, ADR-0007 §7) ---

func projectMetaField(homeDir, taskID, key, value string) error {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		meta = make(map[string]string)
	}
	meta[key] = value
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		return &AuthorizationProjectionError{TaskID: taskID, ProjectionErr: err}
	}
	return nil
}

// projectMergeAuthorization reconciles the .meta merge_authorization
// projection after the authoritative commit.
func projectMergeAuthorization(homeDir, taskID string, rec *taskauthority.MergeAuthorization) error {
	if rec == nil {
		return projectMetaField(homeDir, taskID, MetaMergeAuthorization, "")
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return &AuthorizationProjectionError{TaskID: taskID, ProjectionErr: err}
	}
	return projectMetaField(homeDir, taskID, MetaMergeAuthorization, string(data))
}

// projectExternalMerge reconciles the .meta external_merge projection after
// the authoritative commit.
func projectExternalMerge(homeDir, taskID string, rec *taskauthority.ExternalMergeRecord) error {
	if rec == nil {
		return projectMetaField(homeDir, taskID, MetaExternalMerge, "")
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return &AuthorizationProjectionError{TaskID: taskID, ProjectionErr: err}
	}
	return projectMetaField(homeDir, taskID, MetaExternalMerge, string(data))
}

// projectGitCapabilityTier reconciles the .meta git_capability_tier
// projection after the authoritative commit.
func projectGitCapabilityTier(homeDir, taskID, tier string) error {
	return projectMetaField(homeDir, taskID, MetaGitCapabilityTier, tier)
}

// projectGitAuthContext reconciles the .meta git_auth_context projection
// after the authoritative commit.
func projectGitAuthContext(homeDir, taskID, context string) error {
	return projectMetaField(homeDir, taskID, MetaGitAuthContext, context)
}

// projectGitMutationAuthorization reconciles the .meta
// git_mutation_authorization projection after the authoritative commit (nil
// clears the key).
func projectGitMutationAuthorization(homeDir, taskID string, rec *taskauthority.GitMutationAuthorization) error {
	if rec == nil {
		return projectMetaField(homeDir, taskID, MetaGitMutationAuth, "")
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return &AuthorizationProjectionError{TaskID: taskID, ProjectionErr: err}
	}
	return projectMetaField(homeDir, taskID, MetaGitMutationAuth, string(data))
}
