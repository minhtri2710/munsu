//go:build integration

package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// seedDeliveryOutcome seeds a delivery outcome for taskID with the given status and mergedSHA.
func seedDeliveryOutcome(t *testing.T, auth *taskauthority.Canonical, taskID string, status taskauthority.DeliveryOutcomeStatus, mergedSHA string) {
	t.Helper()
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
// Group A: retirement_poll.go
// ----------------------------------------------------------------------------

func TestListPendingRetirements_InvalidTaskIdentity(t *testing.T) {
	homeDir := t.TempDir()
	recDir := retirementDirPath(homeDir)
	if err := os.MkdirAll(recDir, 0755); err != nil {
		t.Fatal(err)
	}
	rec := &PollRetirementRecord{
		SchemaVersion: PollRetirementSchema,
		TaskID:        "task-different",
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	// Write record to a file whose name does not match sha256(task-different).
	wrongFile := filepath.Join(recDir, "v1-0000000000000000000000000000000000000000000000000000000000000000.json")
	if err := os.WriteFile(wrongFile, data, 0644); err != nil {
		t.Fatal(err)
	}
	_, err = ListPendingRetirements(homeDir)
	if err == nil {
		t.Fatal("expected invalid task identity error, got nil")
	}
	if !strings.Contains(err.Error(), "has invalid task identity") {
		t.Fatalf("ListPendingRetirements err = %v, want has invalid task identity", err)
	}
}

func TestRequireCanonicalCompletedOutcome_NilAuthority(t *testing.T) {
	err := requireCanonicalCompletedOutcome(nil, "some-task")
	if err == nil {
		t.Fatal("expected error for nil authority, got nil")
	}
	if !strings.Contains(err.Error(), "canonical merged truth requires a composed task authority") {
		t.Fatalf("requireCanonicalCompletedOutcome err = %v, want composed task authority error", err)
	}
}

func TestRequireCanonicalCompletedOutcome_NotCompleted(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "task-not-completed"
	auth := mergeTestAuth(t, homeDir, taskID)
	seedDeliveryOutcome(t, auth, taskID, taskauthority.DeliveryOutcomeRetryable, "1111111111111111111111111111111111111111")

	err := requireCanonicalCompletedOutcome(auth, taskID)
	if err == nil {
		t.Fatal("expected non-completed outcome refusal, got nil")
	}
	if !strings.Contains(err.Error(), "a committed completed outcome is required") {
		t.Fatalf("requireCanonicalCompletedOutcome err = %v, want committed completed outcome required", err)
	}
}

func TestRequireRetirementIdentity_NoDeliveryMeta(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "task-no-ident"
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(homeDir, taskID, map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}
	_, err := requireRetirementIdentity(homeDir, taskID)
	if err == nil {
		t.Fatal("expected no delivery identity error, got nil")
	}
	if !strings.Contains(err.Error(), "no delivery identity found for task") {
		t.Fatalf("requireRetirementIdentity err = %v, want no delivery identity found", err)
	}
}

func TestPremiseRetirementRecordPathCannotEscape(t *testing.T) {
	homeDir := t.TempDir()
	expectedDir := retirementDirPath(homeDir)
	absDir, err := filepath.Abs(expectedDir)
	if err != nil {
		t.Fatal(err)
	}

	traversalIDs := []string{
		"../../etc/passwd",
		"..\\..\\windows\\system32",
		"/root",
		"task/with/slashes",
		"",
		" ",
		"....",
	}
	for _, id := range traversalIDs {
		recPath := retirementRecordPath(homeDir, id)
		absPath, err := filepath.Abs(recPath)
		if err != nil {
			t.Fatalf("filepath.Abs(%s): %v", recPath, err)
		}
		if !strings.HasPrefix(absPath, absDir+string(filepath.Separator)) {
			t.Fatalf("retirementRecordPath(%q) = %q escaped %q", id, absPath, absDir)
		}
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
	_, err := retireTaskAuthoritatively(Options{ID: "t1"}, nil, nil)
	if err == nil {
		t.Fatal("expected nil authority error, got nil")
	}
	if !strings.Contains(err.Error(), "retirement requires a composed task authority") {
		t.Fatalf("retireTaskAuthoritatively err = %v, want retirement requires a composed task authority", err)
	}
}

func TestRetireTaskAuthoritatively_DeliveryOutcomeNotCompleted(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "retire-not-completed"
	auth := mergeTestAuth(t, homeDir, taskID)
	seedDeliveryOutcome(t, auth, taskID, taskauthority.DeliveryOutcomeRetryable, "1111111111111111111111111111111111111111")

	meta := map[string]string{
		"pr_url":    "https://github.com/test-owner/test-repo/pull/1",
		"pr_number": "1",
	}
	_, err := retireTaskAuthoritatively(Options{ID: taskID}, meta, auth)
	if err == nil {
		t.Fatal("expected delivery outcome not completed error, got nil")
	}
	if !strings.Contains(err.Error(), "retirement requires a committed completed delivery outcome") {
		t.Fatalf("retireTaskAuthoritatively err = %v, want retirement requires a committed completed delivery outcome", err)
	}
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

func (emptyMergedSHATeardown) QueryMergeStatus(ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
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
	opts := Options{ID: "task-merge-check"}
	backend := emptyMergedSHATeardown{}

	_, err := topologyAwareMergeCheck(opts, nil, "", ident, backend, nil)
	if err == nil {
		t.Fatal("expected empty merged SHA error, got nil")
	}
	if !strings.Contains(err.Error(), "provider returned no merge-result evidence for merged PR") {
		t.Fatalf("topologyAwareMergeCheck err = %v, want provider returned no merge-result evidence", err)
	}
}
