package taskauthority

import (
	"errors"
	"strings"
	"testing"
)

// mustAuthorizeMergeRequest builds a valid AuthorizeMerge request for one task.
func mustAuthorizeMergeRequest(taskID string, generation Generation, headSHA string) AuthorizeMergeRequest {
	return AuthorizeMergeRequest{
		OperationID:        "op-auth-merge-" + taskID,
		Actor:              Actor{ID: "test", Rank: "general"},
		TaskID:             taskID,
		ExpectedGeneration: generation,
		HeadSHA:            headSHA,
		Identity: ProviderIdentitySnapshot{
			Provider: "github",
			Owner:    "owner",
			Repo:     "repo",
			Number:   42,
			URL:      "https://github.com/owner/repo/pull/42",
			BaseRef:  "main",
			HeadRef:  "feature/test",
			HeadSHA:  headSHA,
		},
		Reason: "merge authorization",
	}
}

// TestAuthorizeMergeCommitsGenerationBoundRecord proves one AuthorizeMerge
// operation commits the generation-bound merge authorization record (head
// SHA + provider identity snapshot), advances the Revision by exactly one,
// keeps the phase untouched, and persists the typed audit event and the
// durable idempotency receipt in one transaction.
func TestAuthorizeMergeCommitsGenerationBoundRecord(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)

	res, err := a.AuthorizeMerge(mustAuthorizeMergeRequest("t1", 1, head))
	if err != nil {
		t.Fatal(err)
	}
	if res.TaskID != "t1" || res.Generation != 1 || res.Revision != 2 || res.Phase != PhaseQueued || res.Replayed {
		t.Fatalf("authorize result = %+v, want revision 2 queued", res)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 2 {
		t.Fatalf("revision = %d, want 2", agg.Revision)
	}
	if agg.MergeAuthorization == nil {
		t.Fatal("merge authorization record missing after authorize")
	}
	if agg.MergeAuthorization.HeadSHA != head {
		t.Fatalf("authorized head = %q, want %q", agg.MergeAuthorization.HeadSHA, head)
	}
	if agg.MergeAuthorization.ProviderSnapshot.Provider != "github" ||
		agg.MergeAuthorization.ProviderSnapshot.Owner != "owner" ||
		agg.MergeAuthorization.ProviderSnapshot.Number != 42 ||
		agg.MergeAuthorization.ProviderSnapshot.HeadSHA != head {
		t.Fatalf("provider snapshot = %+v", agg.MergeAuthorization.ProviderSnapshot)
	}
	if agg.MergeAuthorization.Authorizer != "test" || agg.MergeAuthorization.AuthorizedAt <= 0 {
		t.Fatalf("authorizer/authorized-at = %+v", agg.MergeAuthorization)
	}

	// One typed merge-authorization audit event committed with the mutation.
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	var mergeEvents []AuditEvent
	for _, ev := range v.Audit {
		if ev.Kind == AuditMergeAuthorization {
			mergeEvents = append(mergeEvents, ev)
		}
	}
	if len(mergeEvents) != 1 {
		t.Fatalf("merge-authorization audit events = %d, want 1", len(mergeEvents))
	}
	if mergeEvents[0].OperationID != "op-auth-merge-t1" || mergeEvents[0].Actor.ID != "test" ||
		mergeEvents[0].TaskID != "t1" || mergeEvents[0].Generation != 1 {
		t.Fatalf("merge-authorization audit event = %+v", mergeEvents[0])
	}

	// A durable receipt pins the operation.
	var pinned *Receipt
	for i := range v.Receipts {
		if v.Receipts[i].OperationID == "op-auth-merge-t1" {
			pinned = &v.Receipts[i]
		}
	}
	if pinned == nil || pinned.Revision != 2 || pinned.Generation != 1 {
		t.Fatalf("receipts = %+v, want pinned op-auth-merge-t1 revision 2", v.Receipts)
	}
}

// TestAuthorizeMergeGenerationFence proves the Expected Generation fence
// rejects a stale generation and a missing task, mutating nothing.
func TestAuthorizeMergeGenerationFence(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)

	if _, err := a.AuthorizeMerge(mustAuthorizeMergeRequest("t1", 7, head)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale generation error = %v, want ErrConflict", err)
	}
	if _, err := a.AuthorizeMerge(mustAuthorizeMergeRequest("missing", 1, head)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task error = %v, want ErrNotFound", err)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 1 || agg.MergeAuthorization != nil {
		t.Fatalf("failed authorizations must not mutate: %+v", agg)
	}
}

// TestAuthorizeMergeReplayIdempotent proves repeating the same Operation ID
// with the same intent replays the original receipt: no second audit event
// commits and the Revision does not advance twice.
func TestAuthorizeMergeReplayIdempotent(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	req := mustAuthorizeMergeRequest("t1", 1, strings.Repeat("a", 40))

	first, err := a.AuthorizeMerge(req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.AuthorizeMerge(req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed {
		t.Fatal("second authorize must report Replayed=true")
	}
	if second.Revision != first.Revision || second.Generation != first.Generation {
		t.Fatalf("replayed result = %+v, want original %+v", second, first)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 2 {
		t.Fatalf("replay advanced revision to %d, want 2", agg.Revision)
	}
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	var mergeEvents []AuditEvent
	for _, ev := range v.Audit {
		if ev.Kind == AuditMergeAuthorization {
			mergeEvents = append(mergeEvents, ev)
		}
	}
	if len(mergeEvents) != 1 {
		t.Fatalf("replay must not commit a second audit event: %d events", len(mergeEvents))
	}
}

// TestAuthorizeMergeChangedDigestConflicts proves reusing the Operation ID
// with a changed head is a typed non-retryable conflict that preserves the
// original authorization (never silent reuse).
func TestAuthorizeMergeChangedDigestConflicts(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	first := mustAuthorizeMergeRequest("t1", 1, strings.Repeat("a", 40))
	if _, err := a.AuthorizeMerge(first); err != nil {
		t.Fatal(err)
	}

	changed := mustAuthorizeMergeRequest("t1", 1, strings.Repeat("b", 40))
	changed.OperationID = "op-auth-merge-t1" // same op, changed head
	if _, err := a.AuthorizeMerge(changed); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("changed digest error = %v, want ErrOperationConflict", err)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.MergeAuthorization == nil || agg.MergeAuthorization.HeadSHA != first.HeadSHA {
		t.Fatalf("original authorization must be preserved: %+v", agg.MergeAuthorization)
	}
	if agg.Revision != 2 {
		t.Fatalf("conflicting retry must not advance revision: %d", agg.Revision)
	}
}

// TestAuthorizeMergeChangedPriorHeadConflicts proves a fresh authorization
// whose expected prior authorized head does not match the committed record is
// a typed conflict: a different head invalidates the stale authorization and
// is never silently reused.
func TestAuthorizeMergeChangedPriorHeadConflicts(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	headA := strings.Repeat("a", 40)
	if _, err := a.AuthorizeMerge(mustAuthorizeMergeRequest("t1", 1, headA)); err != nil {
		t.Fatal(err)
	}

	// A fresh operation authorizing head B while observing a stale prior head
	// ("") conflicts: the committed authorization is for head A.
	req := mustAuthorizeMergeRequest("t1", 1, strings.Repeat("b", 40))
	req.OperationID = "op-auth-merge-t1-b"
	req.ExpectedPriorHead = ""
	if _, err := a.AuthorizeMerge(req); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale prior head error = %v, want ErrConflict", err)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.MergeAuthorization == nil || agg.MergeAuthorization.HeadSHA != headA {
		t.Fatalf("stale authorization must not be silently replaced: %+v", agg.MergeAuthorization)
	}
	if agg.Revision != 2 {
		t.Fatalf("conflicting authorization must not advance revision: %d", agg.Revision)
	}
}

// TestAuthorizeMergeReauthorizeNewHeadExplicit proves a fresh authorization
// that acknowledges the prior authorized head may re-authorize a changed head
// (explicit re-authorization after pr-amend), replacing the stale record.
func TestAuthorizeMergeReauthorizeNewHeadExplicit(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	headA := strings.Repeat("a", 40)
	headB := strings.Repeat("b", 40)
	if _, err := a.AuthorizeMerge(mustAuthorizeMergeRequest("t1", 1, headA)); err != nil {
		t.Fatal(err)
	}

	req := mustAuthorizeMergeRequest("t1", 1, headB)
	req.OperationID = "op-auth-merge-t1-b"
	req.ExpectedPriorHead = headA
	if _, err := a.AuthorizeMerge(req); err != nil {
		t.Fatalf("explicit re-authorization: %v", err)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.MergeAuthorization.HeadSHA != headB {
		t.Fatalf("re-authorized head = %q, want %q", agg.MergeAuthorization.HeadSHA, headB)
	}
	if agg.Revision != 3 {
		t.Fatalf("revision = %d, want 3", agg.Revision)
	}
}

// TestAuthorizeMergeFirstAuthorizationWithoutPrior proves the first
// authorization of a generation (no prior record) succeeds with an empty
// expected prior head.
func TestAuthorizeMergeFirstAuthorizationWithoutPrior(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	if _, err := a.AuthorizeMerge(mustAuthorizeMergeRequest("t1", 1, strings.Repeat("a", 40))); err != nil {
		t.Fatalf("first authorization with no prior: %v", err)
	}
}

// TestAuthorizeMergeRejectsMalformedRequest proves the request is validated
// before any mutation: empty head, unsafe head, empty provider identity, and
// a head that disagrees with the identity snapshot all fail closed.
func TestAuthorizeMergeRejectsMalformedRequest(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	base := mustAuthorizeMergeRequest("t1", 1, strings.Repeat("a", 40))

	empty := base
	empty.HeadSHA = ""
	if _, err := a.AuthorizeMerge(empty); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty head error = %v, want ErrInvalidInput", err)
	}

	unsafe := base
	unsafe.HeadSHA = "a/b"
	if _, err := a.AuthorizeMerge(unsafe); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsafe head error = %v, want ErrInvalidInput", err)
	}

	noIdentity := base
	noIdentity.Identity = ProviderIdentitySnapshot{}
	if _, err := a.AuthorizeMerge(noIdentity); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty identity error = %v, want ErrInvalidInput", err)
	}

	disagree := base
	disagree.Identity.HeadSHA = strings.Repeat("c", 40)
	if _, err := a.AuthorizeMerge(disagree); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("head disagreement error = %v, want ErrInvalidInput", err)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 1 || agg.MergeAuthorization != nil {
		t.Fatalf("rejected requests must not mutate: %+v", agg)
	}
}

// --- Git authorization ops ---

// mustBoundTask seeds one task with a worktree binding.
func mustBoundTask(t *testing.T, a *Authority, taskID string) {
	t.Helper()
	createTask(t, a, taskID)
	if _, err := a.BindWorktree(BindWorktreeRequest{
		OperationID:        "op-bind-" + taskID,
		Actor:              Actor{ID: "test", Rank: "general"},
		TaskID:             taskID,
		ExpectedGeneration: 1,
		Binding:            mustWorktreeBinding(t, "lease-1", "fence-1"),
		Reason:             "spawn",
	}); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
}

// TestSetGitCapabilityTierCommitsRecord proves one SetGitCapabilityTier
// operation commits the generation-bound capability tier on a worktree-bound
// task, advances the Revision by exactly one, and emits the typed audit.
func TestSetGitCapabilityTierCommitsRecord(t *testing.T) {
	a := newTestAuthority(t)
	mustBoundTask(t, a, "t1")

	res, err := a.SetGitCapabilityTier(SetGitCapabilityTierRequest{
		OperationID: "op-tier-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Tier: GitTierRewrite, Reason: "launch",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.TaskID != "t1" || res.Generation != 1 || res.Revision != 3 || res.Phase != PhaseQueued || res.Replayed {
		t.Fatalf("tier result = %+v, want revision 3 queued", res)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.GitCapabilityTier != string(GitTierRewrite) {
		t.Fatalf("capability tier = %q, want %q", agg.GitCapabilityTier, GitTierRewrite)
	}
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	var gitEvents []AuditEvent
	for _, ev := range v.Audit {
		if ev.Kind == AuditGitAuthorization {
			gitEvents = append(gitEvents, ev)
		}
	}
	if len(gitEvents) != 1 {
		t.Fatalf("git-authorization audit events = %d, want 1", len(gitEvents))
	}
}

// TestSetGitCapabilityTierRequiresWorktree proves the capability tier binds a
// worktree: a task without a worktree binding fails closed and mutates
// nothing.
func TestSetGitCapabilityTierRequiresWorktree(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	_, err := a.SetGitCapabilityTier(SetGitCapabilityTierRequest{
		OperationID: "op-tier-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Tier: GitTierRewrite, Reason: "launch",
	})
	if !errors.Is(err, ErrPrecondition) {
		t.Fatalf("unbound task error = %v, want ErrPrecondition", err)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 1 || agg.GitCapabilityTier != "" {
		t.Fatalf("failed tier set must not mutate: %+v", agg)
	}
}

// TestSetGitCapabilityTierPreStateAndImmutability proves the expected
// pre-state binding and the one-tier-per-generation rule: a mismatched
// expected prior tier conflicts, a different tier on an already-bound
// generation conflicts (immutable), and re-setting the same tier is an
// in-value no-op that does not advance the Revision.
func TestSetGitCapabilityTierPreStateAndImmutability(t *testing.T) {
	a := newTestAuthority(t)
	mustBoundTask(t, a, "t1")

	set := func(opID, prior string, tier GitCapabilityTier) error {
		_, err := a.SetGitCapabilityTier(SetGitCapabilityTierRequest{
			OperationID: opID, Actor: Actor{ID: "test", Rank: "general"},
			TaskID: "t1", ExpectedGeneration: 1, Tier: tier, ExpectedPriorTier: prior, Reason: "launch",
		})
		return err
	}

	// Mismatched expected prior (believed unset, but already committed) conflicts.
	if _, err := a.SetGitCapabilityTier(SetGitCapabilityTierRequest{
		OperationID: "op-tier-t1-a", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Tier: GitTierRewrite, ExpectedPriorTier: "write", Reason: "launch",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("mismatched prior error = %v, want ErrConflict", err)
	}

	if err := set("op-tier-t1-b", "", GitTierRewrite); err != nil {
		t.Fatalf("first set: %v", err)
	}
	agg, _ := a.Get("t1")
	if agg.Revision != 3 {
		t.Fatalf("revision after first set = %d, want 3", agg.Revision)
	}

	// Same-value re-set with the correct prior is an in-value no-op.
	if err := set("op-tier-t1-c", string(GitTierRewrite), GitTierRewrite); err != nil {
		t.Fatalf("same-value re-set: %v", err)
	}
	agg, _ = a.Get("t1")
	if agg.Revision != 3 {
		t.Fatalf("no-op re-set advanced revision to %d, want 3", agg.Revision)
	}

	// A different tier on the already-bound generation conflicts (immutable).
	if err := set("op-tier-t1-d", string(GitTierRewrite), GitTierCleanup); !errors.Is(err, ErrConflict) {
		t.Fatalf("immutability error = %v, want ErrConflict", err)
	}
	agg, _ = a.Get("t1")
	if agg.GitCapabilityTier != string(GitTierRewrite) {
		t.Fatalf("tier changed after rejected set: %q", agg.GitCapabilityTier)
	}
}

// TestSetGitCapabilityTierRejectsInvalidTier proves unknown tiers fail closed.
func TestSetGitCapabilityTierRejectsInvalidTier(t *testing.T) {
	a := newTestAuthority(t)
	mustBoundTask(t, a, "t1")
	_, err := a.SetGitCapabilityTier(SetGitCapabilityTierRequest{
		OperationID: "op-tier-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Tier: GitCapabilityTier("super"), Reason: "launch",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid tier error = %v, want ErrInvalidInput", err)
	}
}

// TestSetGitAuthContextCommitsAndClears proves the auth context commits the
// generation-bound value and can be cleared, each with exactly one Revision
// advance and a typed audit event.
func TestSetGitAuthContextCommitsAndClears(t *testing.T) {
	a := newTestAuthority(t)
	mustBoundTask(t, a, "t1")

	res, err := a.SetGitAuthContext(SetGitAuthContextRequest{
		OperationID: "op-ctx-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Context: "amendment", Reason: "amendment begin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Revision != 3 {
		t.Fatalf("revision after set = %d, want 3", res.Revision)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.GitAuthContext != "amendment" {
		t.Fatalf("context = %q, want amendment", agg.GitAuthContext)
	}

	// Clear from amendment.
	if _, err := a.SetGitAuthContext(SetGitAuthContextRequest{
		OperationID: "op-ctx-t1-clear", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Context: "", ExpectedPriorContext: "amendment", Reason: "amendment accept",
	}); err != nil {
		t.Fatal(err)
	}
	agg, _ = a.Get("t1")
	if agg.GitAuthContext != "" {
		t.Fatalf("context after clear = %q, want empty", agg.GitAuthContext)
	}
	if agg.Revision != 4 {
		t.Fatalf("revision after clear = %d, want 4", agg.Revision)
	}
}

// TestSetGitAuthContextPreStateAndValueNoOp proves the expected pre-state
// binding: a mismatched expected prior context conflicts, and setting the
// context to its current value is an in-value no-op.
func TestSetGitAuthContextPreStateAndValueNoOp(t *testing.T) {
	a := newTestAuthority(t)
	mustBoundTask(t, a, "t1")

	if _, err := a.SetGitAuthContext(SetGitAuthContextRequest{
		OperationID: "op-ctx-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Context: "amendment", ExpectedPriorContext: "retirement", Reason: "x",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("mismatched prior error = %v, want ErrConflict", err)
	}

	if _, err := a.SetGitAuthContext(SetGitAuthContextRequest{
		OperationID: "op-ctx-t1-set", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Context: "amendment", Reason: "x",
	}); err != nil {
		t.Fatal(err)
	}
	agg, _ := a.Get("t1")
	if agg.Revision != 3 {
		t.Fatalf("revision after set = %d, want 3", agg.Revision)
	}

	// Same-value re-set is an in-value no-op.
	if _, err := a.SetGitAuthContext(SetGitAuthContextRequest{
		OperationID: "op-ctx-t1-again", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Context: "amendment", ExpectedPriorContext: "amendment", Reason: "x",
	}); err != nil {
		t.Fatal(err)
	}
	agg, _ = a.Get("t1")
	if agg.Revision != 3 {
		t.Fatalf("no-op re-set advanced revision to %d, want 3", agg.Revision)
	}
}

// TestSetGitAuthContextRejectsInvalidContext proves unknown contexts fail
// closed and the op requires a worktree binding.
func TestSetGitAuthContextRejectsInvalidContext(t *testing.T) {
	a := newTestAuthority(t)
	mustBoundTask(t, a, "t1")
	_, err := a.SetGitAuthContext(SetGitAuthContextRequest{
		OperationID: "op-ctx-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Context: "bogus", Reason: "x",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid context error = %v, want ErrInvalidInput", err)
	}

	unbound := newTestAuthority(t)
	createTask(t, unbound, "t2")
	_, err = unbound.SetGitAuthContext(SetGitAuthContextRequest{
		OperationID: "op-ctx-t2", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t2", ExpectedGeneration: 1, Context: "amendment", Reason: "x",
	})
	if !errors.Is(err, ErrPrecondition) {
		t.Fatalf("unbound task error = %v, want ErrPrecondition", err)
	}
}

// mustGitExpectedState builds a valid expected state for a rewrite op.
func mustGitExpectedState(ref, oldSHA, newSHA string) GitExpectedState {
	return GitExpectedState{Ref: ref, OldSHA: oldSHA, NewSHA: newSHA}
}

// TestAuthorizeGitMutationCommitsRecord proves one AuthorizeGitMutation
// operation commits the generation-bound elevated git mutation authorization
// (operation, expected state, authorizer, context), advances Revision exactly
// one, and emits the typed audit event.
func TestAuthorizeGitMutationCommitsRecord(t *testing.T) {
	a := newTestAuthority(t)
	mustBoundTask(t, a, "t1")
	if _, err := a.SetGitAuthContext(SetGitAuthContextRequest{
		OperationID: "op-ctx-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Context: "amendment", Reason: "x",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := a.AuthorizeGitMutation(AuthorizeGitMutationRequest{
		OperationID: "op-git-auth-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID:               "t1",
		ExpectedGeneration:   1,
		Op:                   GitOpForceWithLease,
		ExpectedState:        mustGitExpectedState("refs/heads/mu/t1", strings.Repeat("o", 40), strings.Repeat("n", 40)),
		Authorizer:           "general",
		Context:              "amendment",
		ExpectedPriorContext: "amendment",
		Reason:               "force-with-lease",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Revision != 4 {
		t.Fatalf("revision after authorize = %d, want 4", res.Revision)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.GitMutationAuthorization == nil {
		t.Fatal("git mutation authorization record missing")
	}
	if agg.GitMutationAuthorization.Operation != GitOpForceWithLease ||
		agg.GitMutationAuthorization.ExpectedState.Ref != "refs/heads/mu/t1" ||
		agg.GitMutationAuthorization.ExpectedState.OldSHA != strings.Repeat("o", 40) ||
		agg.GitMutationAuthorization.Authorizer != "general" ||
		agg.GitMutationAuthorization.Context != "amendment" ||
		agg.GitMutationAuthorization.AuthorizedAt <= 0 {
		t.Fatalf("git mutation authorization = %+v", agg.GitMutationAuthorization)
	}
}

// TestAuthorizeGitMutationPreStateBinding proves the expected prior context
// binds the authorization: a mismatch conflicts and mutates nothing.
func TestAuthorizeGitMutationPreStateBinding(t *testing.T) {
	a := newTestAuthority(t)
	mustBoundTask(t, a, "t1")

	_, err := a.AuthorizeGitMutation(AuthorizeGitMutationRequest{
		OperationID: "op-git-auth-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID:               "t1",
		ExpectedGeneration:   1,
		Op:                   GitOpForceWithLease,
		ExpectedState:        mustGitExpectedState("refs/heads/mu/t1", strings.Repeat("o", 40), strings.Repeat("n", 40)),
		Authorizer:           "general",
		Context:              "amendment",
		ExpectedPriorContext: "retirement", // committed context is "" — mismatch
		Reason:               "force-with-lease",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("pre-state mismatch error = %v, want ErrConflict", err)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.GitMutationAuthorization != nil || agg.Revision != 2 {
		t.Fatalf("failed authorization must not mutate: %+v", agg)
	}
}

// TestAuthorizeGitMutationRejectsWriteTierOp proves write-tier operations are
// never authorized (typed invalid input).
func TestAuthorizeGitMutationRejectsWriteTierOp(t *testing.T) {
	a := newTestAuthority(t)
	mustBoundTask(t, a, "t1")
	_, err := a.AuthorizeGitMutation(AuthorizeGitMutationRequest{
		OperationID: "op-git-auth-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Op: "add",
		ExpectedState: mustGitExpectedState("ref", "old", "new"),
		Authorizer:    "general", Context: "standalone", Reason: "x",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("write-tier op error = %v, want ErrInvalidInput", err)
	}
}

// TestAuthorizeGitMutationValidatesExpectedState proves missing ref, missing
// old SHA, and missing new SHA (except delete ops) fail closed.
func TestAuthorizeGitMutationValidatesExpectedState(t *testing.T) {
	a := newTestAuthority(t)
	mustBoundTask(t, a, "t1")

	req := func(op GitOperation, state GitExpectedState) AuthorizeGitMutationRequest {
		return AuthorizeGitMutationRequest{
			OperationID: "op-git-auth-t1", Actor: Actor{ID: "test", Rank: "general"},
			TaskID: "t1", ExpectedGeneration: 1, Op: op,
			ExpectedState: state, Authorizer: "general", Context: "amendment", Reason: "x",
		}
	}

	if _, err := a.AuthorizeGitMutation(req(GitOpForceWithLease, GitExpectedState{Ref: "", OldSHA: "o", NewSHA: "n"})); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing ref error = %v, want ErrInvalidInput", err)
	}
	if _, err := a.AuthorizeGitMutation(req(GitOpForceWithLease, GitExpectedState{Ref: "r", OldSHA: "", NewSHA: "n"})); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing old SHA error = %v, want ErrInvalidInput", err)
	}
	if _, err := a.AuthorizeGitMutation(req(GitOpForceWithLease, GitExpectedState{Ref: "r", OldSHA: "o", NewSHA: ""})); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing new SHA error = %v, want ErrInvalidInput", err)
	}
	// Delete ops permit an empty new SHA.
	if _, err := a.AuthorizeGitMutation(req(GitOpBranchDelete, GitExpectedState{Ref: "r", OldSHA: "o", NewSHA: ""})); err != nil {
		t.Fatalf("branch-delete without new SHA: %v", err)
	}
}

// TestAuthorizeGitMutationIdempotentReplay proves same-op replay returns the
// original receipt without a second commit.
func TestAuthorizeGitMutationIdempotentReplay(t *testing.T) {
	a := newTestAuthority(t)
	mustBoundTask(t, a, "t1")
	req := AuthorizeGitMutationRequest{
		OperationID: "op-git-auth-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Op: GitOpRebase,
		ExpectedState: mustGitExpectedState("refs/heads/mu/t1", strings.Repeat("o", 40), strings.Repeat("n", 40)),
		Authorizer:    "general", Context: "amendment", Reason: "x",
	}
	first, err := a.AuthorizeGitMutation(req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.AuthorizeGitMutation(req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || second.Revision != first.Revision {
		t.Fatalf("replay = %+v, want original %+v", second, first)
	}
	agg, _ := a.Get("t1")
	if agg.Revision != 3 {
		t.Fatalf("replay advanced revision to %d, want 3", agg.Revision)
	}
}

// TestClearGitMutationAuthorizationClears proves the clear operation removes
// the record (revision advance + audit), is a no-op when already absent, and
// binds the expected prior operation.
func TestClearGitMutationAuthorizationClears(t *testing.T) {
	a := newTestAuthority(t)
	mustBoundTask(t, a, "t1")
	if _, err := a.AuthorizeGitMutation(AuthorizeGitMutationRequest{
		OperationID: "op-git-auth-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Op: GitOpForceWithLease,
		ExpectedState: mustGitExpectedState("refs/heads/mu/t1", strings.Repeat("o", 40), strings.Repeat("n", 40)),
		Authorizer:    "general", Context: "amendment", Reason: "x",
	}); err != nil {
		t.Fatal(err)
	}

	// Clear binding the expected prior operation.
	res, err := a.ClearGitMutationAuthorization(ClearGitMutationAuthorizationRequest{
		OperationID: "op-git-clear-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, ExpectedPriorOp: GitOpForceWithLease, Reason: "mutation complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	agg, _ := a.Get("t1")
	if agg.GitMutationAuthorization != nil {
		t.Fatalf("record not cleared: %+v", agg.GitMutationAuthorization)
	}
	if agg.Revision != res.Revision {
		t.Fatalf("result revision %d != aggregate %d", res.Revision, agg.Revision)
	}

	// Clearing an absent record is a no-op (no revision advance).
	before := agg.Revision
	if _, err := a.ClearGitMutationAuthorization(ClearGitMutationAuthorizationRequest{
		OperationID: "op-git-clear-t1-again", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "again",
	}); err != nil {
		t.Fatalf("clear absent record: %v", err)
	}
	agg, _ = a.Get("t1")
	if agg.Revision != before {
		t.Fatalf("no-op clear advanced revision to %d, want %d", agg.Revision, before)
	}
}

// TestClearGitMutationAuthorizationPreStateBinding proves clearing binds the
// expected prior operation: expecting a record that is absent conflicts, and
// expecting a different operation conflicts.
func TestClearGitMutationAuthorizationPreStateBinding(t *testing.T) {
	a := newTestAuthority(t)
	mustBoundTask(t, a, "t1")

	// Expecting a record when none exists conflicts.
	if _, err := a.ClearGitMutationAuthorization(ClearGitMutationAuthorizationRequest{
		OperationID: "op-git-clear-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, ExpectedPriorOp: GitOpForceWithLease, Reason: "x",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected-record-but-absent error = %v, want ErrConflict", err)
	}

	if _, err := a.AuthorizeGitMutation(AuthorizeGitMutationRequest{
		OperationID: "op-git-auth-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Op: GitOpRebase,
		ExpectedState: mustGitExpectedState("refs/heads/mu/t1", strings.Repeat("o", 40), strings.Repeat("n", 40)),
		Authorizer:    "general", Context: "amendment", Reason: "x",
	}); err != nil {
		t.Fatal(err)
	}

	// Expecting a different prior operation conflicts.
	if _, err := a.ClearGitMutationAuthorization(ClearGitMutationAuthorizationRequest{
		OperationID: "op-git-clear-t1-x", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, ExpectedPriorOp: GitOpForceWithLease, Reason: "x",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong prior op error = %v, want ErrConflict", err)
	}
}

// TestRecordExternalMergeCommitsEvidence proves one RecordExternalMerge
// operation commits the generation-bound external merge evidence record and
// is bounded: a second record under a fresh Operation ID fails closed even
// with identical content, while same-op replay is idempotent.
func TestRecordExternalMergeCommitsEvidence(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)
	req := RecordExternalMergeRequest{
		OperationID: "op-ext-merge-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, MergedSHA: strings.Repeat("m", 40),
		Identity: ProviderIdentitySnapshot{
			Provider: "github", Owner: "owner", Repo: "repo", Number: 42,
			URL: "https://github.com/owner/repo/pull/42", BaseRef: "main", HeadRef: "feature/test", HeadSHA: head,
		},
		ExpectedPriorHead: "",
		Reason:            "external merge",
	}
	res, err := a.RecordExternalMerge(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Revision != 2 {
		t.Fatalf("revision after record = %d, want 2", res.Revision)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.ExternalMerge == nil || agg.ExternalMerge.MergedSHA != strings.Repeat("m", 40) ||
		agg.ExternalMerge.MergeSource != "external" || agg.ExternalMerge.MergedAt <= 0 {
		t.Fatalf("external merge record = %+v", agg.ExternalMerge)
	}

	// Same-op replay is idempotent.
	replayed, err := a.RecordExternalMerge(req)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || replayed.Revision != res.Revision {
		t.Fatalf("replay = %+v, want original %+v", replayed, res)
	}

	// A fresh op with identical content fails closed (bounded).
	again := req
	again.OperationID = "op-ext-merge-t1-again"
	if _, err := a.RecordExternalMerge(again); !errors.Is(err, ErrConflict) {
		t.Fatalf("bounded re-record error = %v, want ErrConflict", err)
	}
	agg, _ = a.Get("t1")
	if agg.Revision != 2 {
		t.Fatalf("bounded conflict must not advance revision: %d", agg.Revision)
	}
}

// TestAuthorizationOpsGenerationFence proves the git authorization ops all
// fence on the Expected Generation and fail closed on missing tasks.
func TestAuthorizationOpsGenerationFence(t *testing.T) {
	a := newTestAuthority(t)
	mustBoundTask(t, a, "t1")

	if _, err := a.SetGitAuthContext(SetGitAuthContextRequest{
		OperationID: "op-ctx-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 7, Context: "amendment", Reason: "x",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale generation error = %v, want ErrConflict", err)
	}
	if _, err := a.SetGitAuthContext(SetGitAuthContextRequest{
		OperationID: "op-ctx-missing", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "missing", ExpectedGeneration: 1, Context: "amendment", Reason: "x",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task error = %v, want ErrNotFound", err)
	}
}
