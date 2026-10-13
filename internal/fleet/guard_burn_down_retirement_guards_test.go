//go:build integration

package fleet

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// seedDeliveryOutcome seeds a delivery outcome for taskID with the given status and mergedSHA.
func seedDeliveryOutcome(t *testing.T, auth *taskauthority.Canonical, taskID string, status taskauthority.DeliveryOutcomeStatus, mergedSHA string) {
	t.Helper()
	mustRecordApprovingVerdict(t, auth, taskID, strings.Repeat("a", 40))
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	ident := domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      "test-owner",
		Repo:       "test-repo",
		Number:     1,
		URL:        "https://github.com/test-owner/test-repo/pull/1",
		BaseRef:    "main",
		HeadRef:    "feature",
		HeadSHA:    strings.Repeat("a", 40),
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
	}
	authReq := taskauthority.CanonicalDeliveryAuthorizationRequest{
		HomeID:        auth.HomeID(),
		TaskID:        mustTaskID(t, taskID),
		Precondition:  domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Kind:          taskauthority.DeliveryAuthorizationProviderMerge,
		Identity:      ident,
		Preconditions: []taskauthority.DeliveryPrecondition{taskauthority.DeliveryPreconditionPRMergeable},
		Words:         deliveryWords(),
	}
	authOp := mustOp(t, "op-auth-"+taskID, authReq)
	authRes, err := auth.AuthorizeDelivery(authOp, authReq)
	if err != nil {
		t.Fatal(err)
	}
	outReq := taskauthority.CanonicalDeliveryOutcomeRequest{
		HomeID:                   auth.HomeID(),
		TaskID:                   mustTaskID(t, taskID),
		Precondition:             domain.Of(uint64(agg.Generation), uint64(authRes.Authorization.Revision)),
		AuthorizationOperationID: authRes.Authorization.OperationID,
		Status:                   status,
		Detail:                   "test outcome",
		MergedSHA:                mergedSHA,
	}
	outOp := mustOp(t, "op-out-"+taskID, outReq)
	if _, err := auth.CommitDeliveryOutcome(outOp, outReq); err != nil {
		t.Fatal(err)
	}
}

// canonicalRetireTask commits a retirement transition for taskID.
func canonicalRetireTask(t *testing.T, auth *taskauthority.Canonical, taskID string) {
	t.Helper()
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalRetireRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Reason:       "retirement",
	}
	opID, err := domain.NewOperationID(taskRetireOperationID(taskID, agg.Generation))
	if err != nil {
		t.Fatal(err)
	}
	op, err := domain.NewOperation(opID, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Retire(op, req); err != nil {
		t.Fatal(err)
	}
}

// ----------------------------------------------------------------------------
// Group B: retirement_task.go — currentOwnershipConflict
// ----------------------------------------------------------------------------

func TestCurrentOwnershipConflict_AcquiredEndpointConflict(t *testing.T) {
	current := &taskauthority.Aggregate{
		TaskID:           "task-conflict-1",
		Generation:       2,
		AcquiredEndpoint: &taskauthority.AcquiredEndpoint{Handle: "@1", LeaseID: "lease-acq"},
	}
	ev := &taskauthority.RetirementEvidence{
		Generation: 1,
		Acquired:   &taskauthority.AcquiredEndpoint{Handle: "@1", LeaseID: "lease-other"},
	}
	err := currentOwnershipConflict(current, ev)
	if err == nil {
		t.Fatal("expected acquired endpoint conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "still holds acquired endpoint") {
		t.Fatalf("currentOwnershipConflict err = %v, want still holds acquired endpoint", err)
	}
}

func TestCurrentOwnershipConflict_AcquiredReusedByBoundEndpoint(t *testing.T) {
	current := &taskauthority.Aggregate{
		TaskID:     "task-conflict-2",
		Generation: 2,
		Endpoint:   &taskauthority.EndpointBinding{Handle: "@1", LeaseID: "lease-ep"},
	}
	ev := &taskauthority.RetirementEvidence{
		Generation: 1,
		Acquired:   &taskauthority.AcquiredEndpoint{Handle: "@1", LeaseID: "lease-other"},
	}
	err := currentOwnershipConflict(current, ev)
	if err == nil {
		t.Fatal("expected bound endpoint reuses acquired identity conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "reuses the retired acquired identity") {
		t.Fatalf("currentOwnershipConflict err = %v, want reuses the retired acquired identity", err)
	}
}

func TestCurrentOwnershipConflict_BoundEndpointConflict(t *testing.T) {
	current := &taskauthority.Aggregate{
		TaskID:     "task-conflict-3",
		Generation: 2,
		Endpoint:   &taskauthority.EndpointBinding{Handle: "@1", LeaseID: "lease-ep"},
	}
	ev := &taskauthority.RetirementEvidence{
		Generation: 1,
		Endpoint:   &taskauthority.EndpointBinding{Handle: "@1", LeaseID: "lease-other"},
	}
	err := currentOwnershipConflict(current, ev)
	if err == nil {
		t.Fatal("expected endpoint ownership conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "still owns endpoint") {
		t.Fatalf("currentOwnershipConflict err = %v, want still owns endpoint", err)
	}
}

func TestCurrentOwnershipConflict_WorktreeConflict(t *testing.T) {
	current := &taskauthority.Aggregate{
		TaskID:     "task-conflict-4",
		Generation: 2,
		Worktree:   &taskauthority.WorktreeBinding{Path: "/path/wt", LeaseID: "lease-wt"},
	}
	ev := &taskauthority.RetirementEvidence{
		Generation: 1,
		Worktree:   &taskauthority.WorktreeBinding{Path: "/path/wt", LeaseID: "lease-other"},
	}
	err := currentOwnershipConflict(current, ev)
	if err == nil {
		t.Fatal("expected worktree ownership conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "still owns worktree") {
		t.Fatalf("currentOwnershipConflict err = %v, want still owns worktree", err)
	}
}

// ----------------------------------------------------------------------------
// Group C: retirement_task.go — resolveRetiredCleanupEvidence
// ----------------------------------------------------------------------------

func TestResolveRetiredCleanupEvidence_GenerationMismatch(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "resolve-gen-mismatch"
	auth := mergeTestAuth(t, homeDir, taskID)
	canonicalRetireTask(t, auth, taskID)

	// Tamper current.json so that Retirement.Generation is 99 (does not match 1).
	taskDocPath := filepath.Join(homeDir, "state", "task-authority", "tasks", taskID, "current.json")
	data, err := os.ReadFile(taskDocPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	aggRaw := raw["aggregate"].(map[string]any)
	retRaw := aggRaw["retirement"].(map[string]any)
	retRaw["generation"] = 99
	tampered, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskDocPath, tampered, 0644); err != nil {
		t.Fatal(err)
	}

	_, err = resolveRetiredCleanupEvidence(auth, mustTaskID(t, taskID), taskauthority.Generation(1))
	if err == nil {
		t.Fatal("expected generation mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "does not match retired generation") {
		t.Fatalf("resolveRetiredCleanupEvidence err = %v, want does not match retired generation", err)
	}
}

func TestResolveRetiredCleanupEvidence_OperationMismatch(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "resolve-op-mismatch"
	auth := mergeTestAuth(t, homeDir, taskID)
	canonicalRetireTask(t, auth, taskID)

	// Tamper current.json so that Retirement.OperationID does not match taskRetireOperationID.
	taskDocPath := filepath.Join(homeDir, "state", "task-authority", "tasks", taskID, "current.json")
	data, err := os.ReadFile(taskDocPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	aggRaw := raw["aggregate"].(map[string]any)
	retRaw := aggRaw["retirement"].(map[string]any)
	retRaw["operation_id"] = "op-bogus-retire"
	tampered, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskDocPath, tampered, 0644); err != nil {
		t.Fatal(err)
	}

	_, err = resolveRetiredCleanupEvidence(auth, mustTaskID(t, taskID), taskauthority.Generation(1))
	if err == nil {
		t.Fatal("expected operation identity mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "does not match the stable retirement identity") {
		t.Fatalf("resolveRetiredCleanupEvidence err = %v, want does not match the stable retirement identity", err)
	}
}

func TestResolveRetiredCleanupEvidence_RetiredNoEvidence(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "resolve-no-evidence"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	// Retire generation 1 without binding resources -> agg.Retirement is nil.
	canonicalRetireTask(t, auth, taskID)

	// Abort cleanup and reopen to generation 2.
	abortCleanupFor(t, auth, homeDir, taskID, taskauthority.Generation(1))
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	reopenReq := taskauthority.CanonicalReopenRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Reason:       "reopen",
	}
	reopenOp := mustOp(t, "op-reopen-"+taskID, reopenReq)
	if _, err := auth.Reopen(reopenOp, reopenReq); err != nil {
		t.Fatal(err)
	}

	_, err = resolveRetiredCleanupEvidence(auth, mustTaskID(t, taskID), taskauthority.Generation(1))
	if err == nil {
		t.Fatal("expected no preserved retirement evidence error, got nil")
	}
	if !strings.Contains(err.Error(), "has no preserved retirement evidence") {
		t.Fatalf("resolveRetiredCleanupEvidence err = %v, want has no preserved retirement evidence", err)
	}
}

// ----------------------------------------------------------------------------
// Group D: retirement_task.go — retireTaskAuthoritatively
// ----------------------------------------------------------------------------

func TestRetireTaskAuthoritatively_NilAuthority(t *testing.T) {
	_, err := retireTaskAuthoritatively(Options{ID: "t1"}, nil)
	if err == nil {
		t.Fatal("expected nil authority error, got nil")
	}
	if !strings.Contains(err.Error(), "retirement requires a composed task authority") {
		t.Fatalf("retireTaskAuthoritatively err = %v, want retirement requires a composed task authority", err)
	}
}

// The delivery-authorization rows below drive retireTaskAuthoritatively, the
// one owner of the refusal, over a real home with canonical delivery state.
const unansweredText = "has no outcome yet, so a merge may be in flight; check munsu delivery merge-status t1, re-run munsu delivery pr-merge t1 <pr-url> to finish it, or retire anyway with munsu teardown --force t1"

func retireDeliveryTask(t *testing.T, c *taskauthority.Canonical, homeDir string, force bool) error {
	t.Helper()
	_, err := retireTaskAuthoritatively(Options{HomeDir: homeDir, ID: "t1", Force: force}, c)
	return err
}

func assertRefusedNotRetired(t *testing.T, c *taskauthority.Canonical, err error, want ...string) {
	t.Helper()
	for _, w := range want {
		if err == nil || !strings.Contains(err.Error(), w) {
			t.Fatalf("retirement err = %v, want it to contain %q", err, w)
		}
	}
	agg, gerr := c.Get(mustFleetTaskID(t, "t1"))
	if gerr != nil {
		t.Fatal(gerr)
	}
	if agg.Phase == taskauthority.PhaseRetired || agg.CleanupClaim != nil {
		t.Fatalf("refused retirement mutated the task: phase=%q claim=%+v", agg.Phase, agg.CleanupClaim)
	}
}

func assertRetired(t *testing.T, c *taskauthority.Canonical, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("retirement: %v", err)
	}
	agg, gerr := c.Get(mustFleetTaskID(t, "t1"))
	if gerr != nil {
		t.Fatal(gerr)
	}
	if agg.Phase != taskauthority.PhaseRetired {
		t.Fatalf("phase = %q, want retired", agg.Phase)
	}
}

// authorizeDeliveryDirect issues a delivery authorization for t1 with no
// journal, as an in-flight Deliver leaves it before its outcome.
func authorizeDeliveryDirect(t *testing.T, c *taskauthority.Canonical, opID string) {
	t.Helper()
	agg, err := c.Get(mustFleetTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalDeliveryAuthorizationRequest{
		HomeID:        c.HomeID(),
		TaskID:        mustFleetTaskID(t, "t1"),
		Precondition:  domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Kind:          taskauthority.DeliveryAuthorizationProviderMerge,
		Identity:      deliveryTestIdentity(),
		Preconditions: []taskauthority.DeliveryPrecondition{taskauthority.DeliveryPreconditionPRMergeable},
		Words:         deliveryWords(),
	}
	if _, err := c.AuthorizeDelivery(mustFleetOperation(t, opID, req), req); err != nil {
		t.Fatalf("AuthorizeDelivery: %v", err)
	}
}

// reauthorizedDelivery leaves t1 with a released retryable delivery and a new
// authorization issued after it: the index still points at the old outcome.
func reauthorizedDelivery(t *testing.T) (*taskauthority.Canonical, string) {
	t.Helper()
	c, homeDir := newFleetCanonical(t)
	mustWorkingDeliveryTask(t, c, "t1", deliveryTestGitHubForge)
	installScriptedProviderFor(t, "open")
	res, err := Deliver(homeDir, "t1", deliverRequest())
	if err != nil || res.Status != taskauthority.DeliveryOutcomeRetryable {
		t.Fatalf("Deliver = %+v, %v, want a retryable outcome", res, err)
	}
	authorizeDeliveryDirect(t, c, "op-reauthorize-t1")
	return c, homeDir
}

// holdDeliveryLock holds the home's delivery lock for the rest of the test.
func holdDeliveryLock(t *testing.T, homeDir string) {
	t.Helper()
	lk, err := mustHome(t, homeDir).Lock(deliveryLockScope)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lk.Release() })
}

// deliveryReauthorizedBlock also leaves t2 crashed mid-merge: t1's teardown
// recovers only t1's journals, so t2's stays for its own delivery.
func deliveryReauthorizedBlock(t *testing.T) {
	c, homeDir := reauthorizedDelivery(t)
	mustWorkingDeliveryTask(t, c, "t2", deliveryTestGitHubForge)
	runDeliveryCrashHelper(t, homeDir, "t2", "mutating", "open-then-merged")
	assertRefusedNotRetired(t, c, retireDeliveryTask(t, c, homeDir, false), "delivery authorization op-reauthorize-t1 "+unansweredText)
	if cur, err := c.DeliveryCurrency(mustFleetTaskID(t, "t2")); err != nil || !unansweredAuthorization(cur) {
		t.Fatalf("t2 delivery = %+v, %v, want its journal left to its own delivery", cur, err)
	}
}

func deliveryReauthorizedForceAllow(t *testing.T) {
	c, homeDir := reauthorizedDelivery(t)
	assertRetired(t, c, retireDeliveryTask(t, c, homeDir, true))
}

func deliveryLockHeldOwnBlock(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	mustWorkingDeliveryTask(t, c, "t1", deliveryTestGitHubForge)
	authorizeDeliveryDirect(t, c, "op-inflight-t1")
	holdDeliveryLock(t, homeDir)
	assertRefusedNotRetired(t, c, retireDeliveryTask(t, c, homeDir, false), "delivery authorization op-inflight-t1 "+unansweredText)
}

func deliveryLockHeldForceAllow(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	mustWorkingDeliveryTask(t, c, "t1", deliveryTestGitHubForge)
	authorizeDeliveryDirect(t, c, "op-inflight-t1")
	holdDeliveryLock(t, homeDir)
	assertRetired(t, c, retireDeliveryTask(t, c, homeDir, true))
}

// crashedDeliveryHeldBlock: the merge ran and a matching delivery hold refuses
// the outcome commit, so recovery cannot answer the authorization.
func crashedDeliveryHeldBlock(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	mustWorkingDeliveryTask(t, c, "t1", deliveryTestGitHubForge)
	deliverWithHoldAt(t, c, homeDir, "t1", "mutating")
	assertRefusedNotRetired(t, c, retireDeliveryTask(t, c, homeDir, false), unansweredText, "(delivery recovery failed: ", string(taskauthority.DeliveryCurrencyMatchingHold))
}

// crashedDeliveryHealsAllow: a delivery crashed at the mutating boundary and
// the provider reports merged; teardown recovers the completed outcome first,
// advancing the revision it retires at.
func crashedDeliveryHealsAllow(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	mustWorkingDeliveryTask(t, c, "t1", deliveryTestGitHubForge)
	runDeliveryCrashHelper(t, homeDir, "t1", "mutating", "open-then-merged")
	installScriptedProviderFor(t, "merged")
	assertRetired(t, c, retireDeliveryTask(t, c, homeDir, false))
	out, err := c.DeliveryOutcome(mustFleetTaskID(t, "t1"))
	if err != nil || out.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("outcome = %+v, %v, want the recovered completed outcome", out, err)
	}
}

// unrelatedTaskNoWaitAllow: t2's delivery is in flight and holds the delivery
// lock; t1 has no delivery of its own, so it retires without waiting for it.
func unrelatedTaskNoWaitAllow(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	mustWorkingDeliveryTask(t, c, "t1", deliveryTestGitHubForge)
	holdDeliveryLock(t, homeDir)
	start := time.Now()
	assertRetired(t, c, retireDeliveryTask(t, c, homeDir, false))
	if waited := time.Since(start); waited > time.Second {
		t.Fatalf("teardown waited %s for another task's delivery lock", waited)
	}
}

// retireDuringLockWait starts a retirement of t1 while the test holds the
// delivery lock, applies change once the retirement has read the task and is
// waiting for the lock, then releases the lock and returns its error.
func retireDuringLockWait(t *testing.T, c *taskauthority.Canonical, homeDir string, opts Options, change func()) error {
	t.Helper()
	lk, err := mustHome(t, homeDir).Lock(deliveryLockScope)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := retireTaskAuthoritatively(opts, c)
		done <- err
	}()
	time.Sleep(700 * time.Millisecond)
	change()
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
	return <-done
}

// assertGeneration2Untouched asserts the reopened generation 2 is queued and
// was not retired.
func assertGeneration2Untouched(t *testing.T, c *taskauthority.Canonical) {
	t.Helper()
	agg, err := c.Get(mustFleetTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Generation != 2 || agg.Phase != taskauthority.PhaseQueued || agg.CleanupClaim != nil {
		t.Fatalf("reopened generation disturbed: generation=%s phase=%q claim=%+v", agg.Generation, agg.Phase, agg.CleanupClaim)
	}
}

// completeT1 completes t1 to done at its current precondition.
func completeT1(t *testing.T, c *taskauthority.Canonical) {
	t.Helper()
	agg, err := c.Get(mustFleetTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	complete := taskauthority.CanonicalCompleteRequest{
		HomeID: c.HomeID(), TaskID: mustFleetTaskID(t, "t1"), Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), To: taskauthority.PhaseDone, Reason: "done",
	}
	if _, err := c.Complete(mustFleetOperation(t, "op-complete-t1", complete), complete); err != nil {
		t.Fatal(err)
	}
}

// healingDeliveryTask leaves t1 with a delivery crashed at the mutating
// boundary that recovery answers (the provider reports merged), so its teardown
// waits for the delivery lock and would retire once recovery ran.
func healingDeliveryTask(t *testing.T) (*taskauthority.Canonical, string) {
	t.Helper()
	c, homeDir := newFleetCanonical(t)
	mustWorkingDeliveryTask(t, c, "t1", deliveryTestGitHubForge)
	runDeliveryCrashHelper(t, homeDir, "t1", "mutating", "open-then-merged")
	installScriptedProviderFor(t, "merged")
	return c, homeDir
}

// reopenDuringLockWaitPinnedBlock: a teardown pinned to generation 1 waits for
// the delivery lock while the task reopens to generation 2.
func reopenDuringLockWaitPinnedBlock(t *testing.T) {
	c, homeDir := healingDeliveryTask(t)
	completeT1(t, c)
	gen1 := taskauthority.Generation(1)
	err := retireDuringLockWait(t, c, homeDir, Options{HomeDir: homeDir, ID: "t1", ExpectedGeneration: &gen1}, func() { reopenQueued(t, c, "t1") })
	var conflict *RetirementTargetConflictError
	if !errors.As(err, &conflict) || conflict.Target != 1 || conflict.Current != 2 {
		t.Fatalf("retirement err = %v, want the target conflict for generation 1 vs 2", err)
	}
	assertGeneration2Untouched(t, c)
}

// reopenDuringLockWaitUnpinnedBlock: an unpinned teardown bound to generation 1
// waits for the delivery lock while the task reopens to generation 2.
func reopenDuringLockWaitUnpinnedBlock(t *testing.T) {
	c, homeDir := healingDeliveryTask(t)
	completeT1(t, c)
	err := retireDuringLockWait(t, c, homeDir, Options{HomeDir: homeDir, ID: "t1"}, func() { reopenQueued(t, c, "t1") })
	var stale *RetirementStaleTeardownError
	if !errors.As(err, &stale) || stale.PriorGeneration != 1 || stale.CurrentGeneration != 2 {
		t.Fatalf("retirement err = %v, want the stale teardown error for generation 1 vs 2", err)
	}
	assertGeneration2Untouched(t, c)
}

// changeDuringLockWaitBlock: the task completes, in the same generation, while
// the teardown waits for the delivery lock.
func changeDuringLockWaitBlock(t *testing.T) {
	c, homeDir := healingDeliveryTask(t)
	err := retireDuringLockWait(t, c, homeDir, Options{HomeDir: homeDir, ID: "t1"}, func() { completeT1(t, c) })
	assertRefusedNotRetired(t, c, err, "task t1 changed while teardown waited for the delivery lock; retry munsu teardown t1")
}

// deliveryReleasedAllow: the provider was unreachable before the merge, so
// Deliver failed closed and released its authorization with no outcome.
func deliveryReleasedAllow(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	mustWorkingDeliveryTask(t, c, "t1", deliveryTestGitHubForge)
	installScriptedProviderFor(t, "unreachable")
	var failClosed *DeliveryFailClosedError
	if _, err := Deliver(homeDir, "t1", deliverRequest()); !errors.As(err, &failClosed) || failClosed.ReleaseErr != nil {
		t.Fatalf("Deliver err = %v, want a released fail-closed delivery", err)
	}
	assertRetired(t, c, retireDeliveryTask(t, c, homeDir, false))
}

// deliveryOwnOutcomeAllow: a retryable outcome is committed against the
// current authorization and its release has not committed.
func deliveryOwnOutcomeAllow(t *testing.T) {
	homeDir := t.TempDir()
	c := mergeTestAuth(t, homeDir, "t1")
	seedDeliveryOutcome(t, c, "t1", taskauthority.DeliveryOutcomeRetryable, "")
	assertRetired(t, c, retireDeliveryTask(t, c, homeDir, false))
}

// ----------------------------------------------------------------------------
// Group E: retirement_task.go — revalidateRetirementCleanup
// ----------------------------------------------------------------------------

func TestRevalidateRetirementCleanup_ClaimNotActive(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "reval-no-claim"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)

	_, err := revalidateRetirementCleanup(auth, mustTaskID(t, taskID), nil, taskauthority.Generation(1), nil)
	if err == nil {
		t.Fatal("expected claim not active error, got nil")
	}
	if !strings.Contains(err.Error(), "is not active and owned by this retirement") {
		t.Fatalf("revalidateRetirementCleanup err = %v, want is not active and owned by this retirement", err)
	}
}

func TestRevalidateRetirementCleanup_EvidenceOperationMismatch(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "reval-op-mismatch"
	auth := mergeTestAuth(t, homeDir, taskID)
	canonicalRetireTask(t, auth, taskID)
	if err := beginRetirementCleanup(auth, mustTaskID(t, taskID), taskauthority.Generation(1)); err != nil {
		t.Fatal(err)
	}

	ev := &taskauthority.RetirementEvidence{
		Generation:  1,
		OperationID: "op-different-retire",
	}
	_, err := revalidateRetirementCleanup(auth, mustTaskID(t, taskID), ev, taskauthority.Generation(1), nil)
	if err == nil {
		t.Fatal("expected retirement evidence operation mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "retirement evidence changed since cleanup began") {
		t.Fatalf("revalidateRetirementCleanup err = %v, want retirement evidence changed", err)
	}
}

func TestRevalidateRetirementCleanup_AcquiredEvidenceMismatch(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "reval-acq-mismatch"
	auth := mergeTestAuth(t, homeDir, taskID)
	canonicalRetireTask(t, auth, taskID)
	if err := beginRetirementCleanup(auth, mustTaskID(t, taskID), taskauthority.Generation(1)); err != nil {
		t.Fatal(err)
	}
	cur, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}

	ev := &taskauthority.RetirementEvidence{
		Generation:  1,
		OperationID: cur.Retirement.OperationID,
		Acquired:    &taskauthority.AcquiredEndpoint{Handle: "@mismatch", LeaseID: "l1"},
	}
	_, err = revalidateRetirementCleanup(auth, mustTaskID(t, taskID), ev, taskauthority.Generation(1), nil)
	if err == nil {
		t.Fatal("expected acquired endpoint evidence mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "retirement acquired-endpoint evidence changed") {
		t.Fatalf("revalidateRetirementCleanup err = %v, want retirement acquired-endpoint evidence changed", err)
	}
}

func TestRevalidateRetirementCleanup_EndpointEvidenceMismatch(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "reval-ep-mismatch"
	auth := mergeTestAuth(t, homeDir, taskID)
	canonicalRetireTask(t, auth, taskID)
	if err := beginRetirementCleanup(auth, mustTaskID(t, taskID), taskauthority.Generation(1)); err != nil {
		t.Fatal(err)
	}
	cur, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}

	ev := &taskauthority.RetirementEvidence{
		Generation:  1,
		OperationID: cur.Retirement.OperationID,
		Endpoint:    &taskauthority.EndpointBinding{Handle: "@mismatch", LeaseID: "lease-ep", FenceToken: "fence-ep"},
	}
	_, err = revalidateRetirementCleanup(auth, mustTaskID(t, taskID), ev, taskauthority.Generation(1), nil)
	if err == nil {
		t.Fatal("expected endpoint evidence mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "retirement endpoint evidence changed") {
		t.Fatalf("revalidateRetirementCleanup err = %v, want retirement endpoint evidence changed", err)
	}
}

func TestRevalidateRetirementCleanup_WorktreeEvidenceMismatch(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "reval-wt-mismatch"
	auth := mergeTestAuth(t, homeDir, taskID)
	canonicalRetireTask(t, auth, taskID)
	if err := beginRetirementCleanup(auth, mustTaskID(t, taskID), taskauthority.Generation(1)); err != nil {
		t.Fatal(err)
	}
	cur, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}

	ev := &taskauthority.RetirementEvidence{
		Generation:  1,
		OperationID: cur.Retirement.OperationID,
		Worktree:    &taskauthority.WorktreeBinding{Path: "/mismatched/path", LeaseID: "lease-wt", FenceToken: "fence-wt"},
	}
	_, err = revalidateRetirementCleanup(auth, mustTaskID(t, taskID), ev, taskauthority.Generation(1), nil)
	if err == nil {
		t.Fatal("expected worktree evidence mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "retirement worktree evidence changed") {
		t.Fatalf("revalidateRetirementCleanup err = %v, want retirement worktree evidence changed", err)
	}
}

func TestRevalidateRetirementCleanup_RevisionAdvanced(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "reval-rev-adv"
	auth := mergeTestAuth(t, homeDir, taskID)
	canonicalRetireTask(t, auth, taskID)
	if err := beginRetirementCleanup(auth, mustTaskID(t, taskID), taskauthority.Generation(1)); err != nil {
		t.Fatal(err)
	}
	cur, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}

	initial := &taskauthority.Aggregate{
		Generation: cur.Generation,
		Revision:   cur.Revision - 1,
	}
	_, err = revalidateRetirementCleanup(auth, mustTaskID(t, taskID), cur.Retirement, cur.Generation, initial)
	if err == nil {
		t.Fatal("expected revision advanced error, got nil")
	}
	if !strings.Contains(err.Error(), "advanced from revision") {
		t.Fatalf("revalidateRetirementCleanup err = %v, want advanced from revision", err)
	}
}

// ----------------------------------------------------------------------------
// Group F: retirement_task.go — topologyAwareMergeCheck
// ----------------------------------------------------------------------------

type emptyMergedSHATeardown struct {
	fakeTeardown
}

func (emptyMergedSHATeardown) QueryMergeStatus(_ taskauthority.DeliveryStep, ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
	return &domain.PRMergeStatus{
		Merged:    true,
		State:     "MERGED",
		HeadSHA:   ident.HeadSHA,
		MergedSHA: "", // empty merged SHA triggers guard
	}, nil
}

func TestTopologyAwareMergeCheck_EmptyMergedSHA(t *testing.T) {
	ident := &domain.DeliveryIdentity{
		Provider: "github",
		Owner:    "test-owner",
		Repo:     "test-repo",
		Number:   1,
		URL:      "https://github.com/test-owner/test-repo/pull/1",
		HeadSHA:  "1111111111111111111111111111111111111111",
	}
	opts := contractedMergeCheckOptions(t, "task-merge-check")
	backend := emptyMergedSHATeardown{}

	_, err := topologyAwareMergeCheck(opts, "", ident, backend, nil)
	if err == nil {
		t.Fatal("expected empty merged SHA error, got nil")
	}
	if !strings.Contains(err.Error(), "provider returned no merge-result evidence for merged PR") {
		t.Fatalf("topologyAwareMergeCheck err = %v, want provider returned no merge-result evidence", err)
	}
}
