//go:build integration

package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// canonicalMergeTestAuth inits the home at homeDir, composes the canonical
// Task Authority over it, and creates the task. It is the canonical
// replacement for the deleted in-memory mergeTestAuth: the canonical surface
// is the only retirement authority.
func canonicalMergeTestAuth(t *testing.T, homeDir, taskID string) *taskauthority.Canonical {
	t.Helper()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	auth, err := taskauthority.NewCanonical(mustHome(t, homeDir))
	if err != nil {
		t.Fatal(err)
	}
	canonicalCreateTask(t, auth, taskID, "ship", "")
	return auth
}

// seedWorktreeEvidence binds one generation-scoped worktree lease through the
// canonical Authority at the task's current generation/revision.
func seedWorktreeEvidence(t *testing.T, auth *taskauthority.Canonical, taskID, path, lease, fence string) {
	t.Helper()
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalBindWorktreeRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Binding: taskauthority.WorktreeBinding{
			RepositoryIdentity: "repo-" + taskID,
			Path:               path,
			GitDir:             filepath.Join(path, ".git"),
			CommonDir:          filepath.Join(filepath.Dir(path), ".git"),
			Head:               strings.Repeat("a", 40),
			LeaseID:            lease,
			FenceToken:         fence,
			BoundAtUnix:        time.Now().Unix(),
		},
		Reason: "spawn",
	}
	op, err := domain.NewOperation(mustOpID(t, "op-bind-wt-"+taskID+"-"+lease), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.BindWorktree(op, req); err != nil {
		t.Fatalf("BindWorktree(%s): %v", taskID, err)
	}
}

// seedEndpointEvidence binds one generation-scoped endpoint lease through the
// canonical Authority (which also marks the generation working) at the task's
// current generation/revision.
func seedEndpointEvidence(t *testing.T, auth *taskauthority.Canonical, taskID, handle, lease, fence string) {
	t.Helper()
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalBindEndpointRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Binding: taskauthority.EndpointBinding{
			Backend:      "tmux",
			Handle:       handle,
			LeaseID:      lease,
			FenceToken:   fence,
			SessionOwner: "session-" + taskID,
			WorkspaceID:  "ws-" + taskID,
			TabID:        "tab-" + taskID,
			Incarnation:  "inc-" + taskID,
			BoundAtUnix:  time.Now().Unix(),
		},
		Reason: "spawn",
	}
	op, err := domain.NewOperation(mustOpID(t, "op-bind-ep-"+taskID+"-"+lease), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.BindEndpoint(op, req); err != nil {
		t.Fatalf("BindEndpoint(%s): %v", taskID, err)
	}
}

// mergeTestAuth seeds one canonical ship task with bound worktree and
// endpoint evidence (working) and returns the canonical Authority. It is the
// canonical replacement for the deleted in-memory merge test authority; the
// worktree binding path is <homeDir>/worktree so backend tests that write
// that path into meta exercise the evidence-pinned return.
func mergeTestAuth(t *testing.T, homeDir, taskID string) *taskauthority.Canonical {
	t.Helper()
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	wtDir := filepath.Join(homeDir, "worktree")
	os.MkdirAll(wtDir, 0755)
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt", "fence-wt")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep", "fence-ep")
	return auth
}

// seedMergedDelivery seeds a canonical completed provider-merge delivery
// outcome: bound worktree/endpoint (working), a provider-merge delivery
// authorization, and a committed completed outcome under the task's own
// identity. It is the canonical merged truth the retirement path requires;
// no .meta delivery_state projection is involved.
func seedMergedDelivery(t *testing.T, auth *taskauthority.Canonical, homeDir, taskID string) {
	t.Helper()
	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(wtDir, 0755)
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt-merged", "fence-wt-merged")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep-merged", "fence-ep-merged")

	ident := deliveryFixtureIdentity()
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	authReq := taskauthority.CanonicalDeliveryAuthorizationRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Kind:         taskauthority.DeliveryAuthorizationProviderMerge,
		Identity:     ident,
		Preconditions: []taskauthority.DeliveryPrecondition{
			taskauthority.DeliveryPreconditionPRMergeable,
			taskauthority.DeliveryPreconditionPRHeadCurrent,
		},
	}
	if _, err := auth.AuthorizeDelivery(mustFleetOperation(t, "op-del-auth-"+taskID, authReq), authReq); err != nil {
		t.Fatalf("AuthorizeDelivery(%s): %v", taskID, err)
	}
	agg2, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	outReq := taskauthority.CanonicalDeliveryOutcomeRequest{
		HomeID:                   auth.HomeID(),
		TaskID:                   mustTaskID(t, taskID),
		Precondition:             domain.Of(uint64(agg2.Generation), uint64(agg2.Revision)),
		AuthorizationOperationID: "op-del-auth-" + taskID,
		Status:                   taskauthority.DeliveryOutcomeCompleted,
		Detail:                   "provider confirms merged",
		HeadSHA:                  ident.HeadSHA,
		MergedSHA:                strings.Repeat("b", 40),
	}
	if _, err := auth.CommitDeliveryOutcome(mustFleetOperation(t, "op-del-out-"+taskID, outReq), outReq); err != nil {
		t.Fatalf("CommitDeliveryOutcome(%s): %v", taskID, err)
	}
}

// deliveryFixtureIdentity is the canonical delivery identity whose head
// matches the seeded worktree binding head.
func deliveryFixtureIdentity() domain.DeliveryIdentity {
	return domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      "testowner",
		Repo:       "testrepo",
		Number:     42,
		URL:        "https://github.com/testowner/testrepo/pull/42",
		BaseRef:    "main",
		HeadRef:    "feature",
		HeadSHA:    strings.Repeat("a", 40),
		CapturedAt: "2024-01-01T00:00:00Z",
	}
}

// seedCanonicalOutcome seeds a canonical delivery outcome of the given
// status under the task's own identity (bind worktree + endpoint, authorize,
// commit outcome). It returns the canonical authority.
func seedCanonicalOutcome(t *testing.T, homeDir, taskID string, status taskauthority.DeliveryOutcomeStatus) *taskauthority.Canonical {
	t.Helper()
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(wtDir, 0755)
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt-out", "fence-wt-out")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep-out", "fence-ep-out")

	ident := deliveryFixtureIdentity()
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	authReq := taskauthority.CanonicalDeliveryAuthorizationRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Kind:         taskauthority.DeliveryAuthorizationProviderMerge,
		Identity:     ident,
		Preconditions: []taskauthority.DeliveryPrecondition{
			taskauthority.DeliveryPreconditionPRMergeable,
			taskauthority.DeliveryPreconditionPRHeadCurrent,
		},
	}
	if _, err := auth.AuthorizeDelivery(mustFleetOperation(t, "op-del-auth-out-"+taskID, authReq), authReq); err != nil {
		t.Fatalf("AuthorizeDelivery(%s): %v", taskID, err)
	}
	agg2, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	outReq := taskauthority.CanonicalDeliveryOutcomeRequest{
		HomeID:                   auth.HomeID(),
		TaskID:                   mustTaskID(t, taskID),
		Precondition:             domain.Of(uint64(agg2.Generation), uint64(agg2.Revision)),
		AuthorizationOperationID: "op-del-auth-out-" + taskID,
		Status:                   status,
		Detail:                   "canonical outcome fixture",
		HeadSHA:                  ident.HeadSHA,
	}
	if _, err := auth.CommitDeliveryOutcome(mustFleetOperation(t, "op-del-out-out-"+taskID, outReq), outReq); err != nil {
		t.Fatalf("CommitDeliveryOutcome(%s): %v", taskID, err)
	}
	return auth
}

// writeRetireMeta writes a minimal task meta projection (no delivery
// identity, so the baseline retirement prerequisite applies).
func writeRetireMeta(t *testing.T, homeDir, taskID, window, worktree string) {
	t.Helper()
	meta := map[string]string{
		"kind":    "ship",
		"backend": "tmux",
		"window":  window,
	}
	if worktree != "" {
		meta["worktree"] = worktree
	}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatal(err)
	}
}

// recordingTeardown records every Dispose/ReturnWorktree it is asked to
// perform so tests can assert exactly which evidence-owned resources were
// released.
type recordingTeardown struct {
	disposed   []DisposeRequest
	returned   []string
	alive      bool
	status     *RetirementEndpointStatus // fixed observation; overrides alive when set
	onProbe    func()                    // executed at the start of Probe (TOCTOU simulation)
	onDispose  func()                    // executed at the start of Dispose (TOCTOU simulation)
	onReturn   func()                    // executed at the start of ReturnWorktree (TOCTOU simulation)
	disposeErr error
	returnErr  error
}

func (r *recordingTeardown) RefuseGate() error { return nil }
func (r *recordingTeardown) Probe(string, map[string]string) (RetirementEndpointStatus, error) {
	if r.onProbe != nil {
		r.onProbe()
	}
	if r.status != nil {
		return *r.status, nil
	}
	lifecycle := LifecycleDead
	if r.alive {
		lifecycle = LifecycleAlive
	}
	return RetirementEndpointStatus{Lifecycle: lifecycle, Responsiveness: Responsive, Freshness: FreshnessCurrent, Activity: ActivityUnknown, Source: SourceProbe}, nil
}
func (r *recordingTeardown) Dispose(_ string, _ map[string]string, req DisposeRequest) error {
	if r.onDispose != nil {
		r.onDispose()
	}
	r.disposed = append(r.disposed, req)
	return r.disposeErr
}
func (r *recordingTeardown) QueryMergeStatus(ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
	return QueryDeliveryMergeStatus(ident)
}
func (r *recordingTeardown) ReturnWorktree(_ string, path string) error {
	if r.onReturn != nil {
		r.onReturn()
	}
	r.returned = append(r.returned, path)
	return r.returnErr
}

func TestRetirementDisposesAndReturnsOnlyEvidenceOwnedResources(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "exact-release"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(wtDir, 0755)
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt", "fence-wt")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep", "fence-ep")
	writeRetireMeta(t, homeDir, taskID, "@1", wtDir)

	rec := &recordingTeardown{alive: true}
	result, err := RetireTask(Options{HomeDir: homeDir, ID: taskID, Force: true}, rec, fakeRetirementJournals{}, auth)
	if err != nil {
		t.Fatalf("retirement: %v", err)
	}
	// The dispose targeted exactly the evidence-owned endpoint identity and
	// the return exactly the evidence-owned worktree path.
	if len(rec.disposed) != 1 {
		t.Fatalf("dispose calls=%d, want 1 (evidence-owned endpoint only)", len(rec.disposed))
	}
	if rec.disposed[0].Handle != "@1" || rec.disposed[0].Backend != "tmux" {
		t.Fatalf("disposed=%+v, want evidence endpoint @1/tmux", rec.disposed[0])
	}
	if len(rec.returned) != 1 || rec.returned[0] != wtDir {
		t.Fatalf("returned=%v, want evidence worktree %s", rec.returned, wtDir)
	}
	// Projection removed after full cleanup; canonical retirement evidence is
	// preserved (projection removal must not erase it).
	if _, err := os.Stat(filepath.Join(homeDir, "state", taskID+".meta")); !os.IsNotExist(err) {
		t.Fatal("meta should be removed after successful retirement")
	}
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseRetired {
		t.Fatalf("phase=%q, want retired", agg.Phase)
	}
	if agg.Retirement == nil || agg.Retirement.Endpoint == nil || agg.Retirement.Endpoint.LeaseID != "lease-ep" ||
		agg.Retirement.Worktree == nil || agg.Retirement.Worktree.LeaseID != "lease-wt" {
		t.Fatalf("canonical retirement evidence lost: %+v", agg.Retirement)
	}
	_ = result
}

// TestRetirementAmbiguousObservationNeverDisposes asserts a raw retirement
// probe that is ambiguous (unknown/stale/unresponsive/starting — anything that
// is NOT an authorized exact absence and NOT an authorized live reading) never
// disposes the endpoint: cleanup stays pending and the lease/ownership is
// retained (BEO-16: unknown != dead, positive liveness needs acquisition
// evidence).
func TestRetirementAmbiguousObservationNeverDisposes(t *testing.T) {
	for _, state := range []EndpointObservationState{EndpointUnknown, EndpointUnresponsive, EndpointStaleIdentity, EndpointStarting} {
		t.Run(state.String(), func(t *testing.T) {
			homeDir := t.TempDir()
			taskID := "amb-retire"
			auth := canonicalMergeTestAuth(t, homeDir, taskID)
			wtDir := filepath.Join(homeDir, "worktrees", taskID)
			os.MkdirAll(wtDir, 0755)
			seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt", "fence-wt")
			seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep", "fence-ep")
			writeRetireMeta(t, homeDir, taskID, "@1", wtDir)

			rec := &recordingTeardown{status: func() *RetirementEndpointStatus {
				s := endpointStatusFromState(state)
				return &RetirementEndpointStatus{Lifecycle: s.Lifecycle, Responsiveness: s.Responsiveness, Freshness: s.Freshness, Activity: s.Activity, Source: s.Source, Detail: s.Detail}
			}()}
			_, err := RetireTask(Options{HomeDir: homeDir, ID: taskID, Force: true}, rec, fakeRetirementJournals{}, auth)
			if err == nil {
				t.Fatalf("%s observation must fail closed (cleanup pending), got nil", state)
			}
			if len(rec.disposed) != 0 {
				t.Fatalf("%s observation disposed %d endpoints; must never dispose on ambiguity", state, len(rec.disposed))
			}
		})
	}
}

// reopenTaskGen2 reopens the task to generation 2 and binds the given worktree
// and endpoint identities on it. It is used by the TOCTOU tests to simulate a
// concurrent actor re-acquiring resources at exactly the reviewer's window
// (between the probe and the destructive action).
// tryReopenExpectClaimConflict attempts a concurrent Reopen at a barrier point
// (between a locked revalidation and the destructive action that follows it)
// and asserts the durable cleanup claim REJECTS it: the task is pinned while
// cleanup is active, so no actor can land a reopen in the post-unlock window
// (BEO-16/P1a durable disposal claim barrier).
func tryReopenExpectClaimConflict(t *testing.T, auth *taskauthority.Canonical, taskID string) {
	t.Helper()
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	reopen := taskauthority.CanonicalReopenRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Reason:       "barrier reopen",
	}
	op, err := domain.NewOperation(mustOpID(t, "op-barrier-reopen-"+taskID), reopen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Reopen(op, reopen); !errors.Is(err, taskauthority.ErrConflict) {
		t.Fatalf("Reopen at barrier (claim active) = %v, want ErrConflict", err)
	}
}

// tryBindWorktreeExpectClaimConflict attempts a concurrent worktree
// acquisition at a barrier point and asserts the active cleanup claim rejects
// it (acquisition fails closed while the claim is active).
func tryBindWorktreeExpectClaimConflict(t *testing.T, auth *taskauthority.Canonical, taskID, path string) {
	t.Helper()
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	wt := taskauthority.WorktreeBinding{
		RepositoryIdentity: "repo-barrier",
		Path:               path,
		GitDir:             filepath.Join(path, ".git"),
		CommonDir:          filepath.Join(filepath.Dir(path), ".git"),
		Head:               strings.Repeat("b", 40),
		LeaseID:            "lease-wt-barrier",
		FenceToken:         "fence-wt-barrier",
		BoundAtUnix:        time.Now().UnixNano(),
	}
	req := taskauthority.CanonicalBindWorktreeRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Binding:      wt,
		Reason:       "barrier bind",
	}
	op, err := domain.NewOperation(mustOpID(t, "op-barrier-bindwt-"+taskID), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.BindWorktree(op, req); !errors.Is(err, taskauthority.ErrConflict) {
		t.Fatalf("BindWorktree at barrier (claim active) = %v, want ErrConflict", err)
	}
}

// tryBindEndpointExpectClaimConflict attempts a concurrent endpoint
// acquisition at a barrier point and asserts the active cleanup claim rejects
// it.
func tryBindEndpointExpectClaimConflict(t *testing.T, auth *taskauthority.Canonical, taskID string) {
	t.Helper()
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	ep := taskauthority.EndpointBinding{
		Backend:     "tmux",
		Handle:      "@barrier",
		LeaseID:     "lease-ep-barrier",
		FenceToken:  "fence-ep-barrier",
		Incarnation: "barrier-inc",
		BoundAtUnix: time.Now().UnixNano(),
	}
	req := taskauthority.CanonicalBindEndpointRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Binding:      ep,
		Reason:       "barrier bind",
	}
	op, err := domain.NewOperation(mustOpID(t, "op-barrier-bindep-"+taskID), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.BindEndpoint(op, req); !errors.Is(err, taskauthority.ErrConflict) {
		t.Fatalf("BindEndpoint at barrier (claim active) = %v, want ErrConflict", err)
	}
}

// abortCleanupFor releases the durable cleanup claim of the given generation
// through the canonical AbortCleanup operation (the operator escape hatch),
// asserting it reconciled to aborted.
func abortCleanupFor(t *testing.T, auth *taskauthority.Canonical, taskID string, gen taskauthority.Generation) {
	t.Helper()
	if err := AbortRetirementCleanup(auth, mustTaskID(t, taskID), gen); err != nil {
		t.Fatalf("AbortRetirementCleanup: %v", err)
	}
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != taskauthority.CleanupAborted || agg.CleanupClaim.Generation != gen {
		t.Fatalf("claim not aborted for generation %s: %+v", gen, agg.CleanupClaim)
	}
}

// assertClaimCompleted asserts the current aggregate carries the cleanup claim
// of the given generation reconciled to completed (the cleanup finished).
func assertClaimCompleted(t *testing.T, auth *taskauthority.Canonical, taskID string, gen taskauthority.Generation) {
	t.Helper()
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != taskauthority.CleanupCompleted || agg.CleanupClaim.Generation != gen || agg.CleanupClaim.ReconciledAt <= 0 {
		t.Fatalf("cleanup claim not completed for generation %s: %+v", gen, agg.CleanupClaim)
	}
}

// reopenQueued reopens the task into a fresh queued generation at the current
// precondition.
func reopenQueued(t *testing.T, auth *taskauthority.Canonical, taskID string) taskauthority.Aggregate {
	t.Helper()
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	reopen := taskauthority.CanonicalReopenRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Reason:       "reopen",
	}
	op, err := domain.NewOperation(mustOpID(t, "op-reopen-"+taskID), reopen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Reopen(op, reopen); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	cur, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if cur.Generation <= agg.Generation || cur.Phase != taskauthority.PhaseQueued {
		t.Fatalf("reopened aggregate = %+v", cur)
	}
	return cur
}

// TestRetirementClaimBarrierBetweenProbeAndDispose proves the durable cleanup
// claim closes the post-unlock window: a concurrent actor attempting a reopen
// or an acquisition exactly BETWEEN the post-probe locked revalidation (which
// released the task lock) and the Dispose action is REJECTED by the claim, so
// the cleanup proceeds to completion on the exact evidence-owned resources.
// After completion the claim is reconciled and the retired task becomes
// reopenable (BEO-16/P1a barrier + completion reconciliation).
func TestRetirementClaimBarrierBetweenProbeAndDispose(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "barrier-probe"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(wtDir, 0755)
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt-1", "fence-wt-1")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep-1", "fence-ep-1")
	writeRetireMeta(t, homeDir, taskID, "@1", wtDir)
	opts := Options{HomeDir: homeDir, ID: taskID, Force: true}

	// Commit the generation-1 retirement with interrupted cleanup (the
	// destructive Dispose never ran; the durable claim stays active).
	first := &recordingTeardown{alive: true, disposeErr: errors.New("window busy")}
	if _, err := RetireTask(opts, first, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup")
	}
	firstAgg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if firstAgg.CleanupClaim == nil || firstAgg.CleanupClaim.Status != taskauthority.CleanupActive {
		t.Fatalf("cleanup claim not active after interruption: %+v", firstAgg.CleanupClaim)
	}

	// The fake runs the barrier attempts at Probe entry — exactly the
	// reviewer's post-unlock window between the revalidation and the action.
	// Every attempt must be rejected by the durable claim (never observed as
	// a successful reopen/rebind).
	second := &recordingTeardown{alive: true, onProbe: func() {
		tryReopenExpectClaimConflict(t, auth, taskID)
		tryBindWorktreeExpectClaimConflict(t, auth, taskID, wtDir)
		tryBindEndpointExpectClaimConflict(t, auth, taskID)
	}}
	result, err := RetireTask(opts, second, fakeRetirementJournals{}, auth)
	if err != nil {
		t.Fatalf("cleanup with claim barrier: %v", err)
	}
	if len(second.disposed) != 1 || second.disposed[0].Handle != "@1" {
		t.Fatalf("disposed=%+v, want evidence endpoint @1 only", second.disposed)
	}
	if len(second.returned) != 1 || second.returned[0] != wtDir {
		t.Fatalf("returned=%v, want evidence worktree %s", second.returned, wtDir)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "state", taskID+".meta")); !os.IsNotExist(err) {
		t.Fatalf("meta should be removed after completed cleanup: %v", err)
	}
	assertClaimCompleted(t, auth, taskID, taskauthority.Generation(1))
	_ = result

	// Completion reconciliation: the reconciled claim releases the task and a
	// fresh generation can reopen.
	reopenQueued(t, auth, taskID)
}

// TestRetirementClaimBarrierBeforeWorktreeReturn proves the claim rejects a
// reopen exactly BETWEEN the worktree-return fence (lock released) and the
// ReturnWorktree action; cleanup completes and the worktree is returned once.
func TestRetirementClaimBarrierBeforeWorktreeReturn(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "barrier-worktree"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(wtDir, 0755)
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt-1", "fence-wt-1")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep-1", "fence-ep-1")
	writeRetireMeta(t, homeDir, taskID, "@1", wtDir)
	opts := Options{HomeDir: homeDir, ID: taskID, Force: true}

	// Interrupted first run: worktree return fails after the (independently
	// fenced) endpoint dispose; the claim stays active.
	first := &recordingTeardown{alive: true, returnErr: errors.New("pool busy")}
	if _, err := RetireTask(opts, first, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup")
	}

	second := &recordingTeardown{alive: true, onDispose: func() {
		// Barrier at Dispose entry: between the dispose fence and the action,
		// a concurrent reopen is rejected by the durable claim.
		tryReopenExpectClaimConflict(t, auth, taskID)
	}}
	if _, err := RetireTask(opts, second, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("cleanup with claim barrier: %v", err)
	}
	if len(second.returned) != 1 || second.returned[0] != wtDir {
		t.Fatalf("returned=%v, want evidence worktree %s", second.returned, wtDir)
	}
	assertClaimCompleted(t, auth, taskID, taskauthority.Generation(1))
}

// barrierJournals runs a callback inside FinalizeRetirementJournals — exactly
// BETWEEN the meta removal and the state-artifact/data removals, i.e. inside
// the projection multi-remove sequence — to attempt a barrier reopen while
// individual filesystem removals are in progress.
type barrierJournals struct{ onFinalize func() }

func (barrierJournals) VerifyRetirementContinuity(string, string) error { return nil }
func (barrierJournals) PrepareForcedRetirementEvidence(string, string) ([]string, error) {
	return nil, nil
}
func (b barrierJournals) FinalizeRetirementJournals(string, string) ([]string, error) {
	b.onFinalize()
	return nil, nil
}

// TestRetirementClaimBarrierDuringProjectionMultiRemove proves the durable
// claim protects the projection multi-remove sequence: a reopen attempted
// BETWEEN individual filesystem removals (after the projection fence released
// the lock and after the meta file was already removed) is rejected, the
// remaining removals complete, and the claim reconciles to completed.
func TestRetirementClaimBarrierDuringProjectionMultiRemove(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "barrier-projection"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(wtDir, 0755)
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt-1", "fence-wt-1")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep-1", "fence-ep-1")
	writeRetireMeta(t, homeDir, taskID, "@1", wtDir)
	opts := Options{HomeDir: homeDir, ID: taskID, Force: true}

	rec := &recordingTeardown{alive: true}
	journals := barrierJournals{onFinalize: func() {
		tryReopenExpectClaimConflict(t, auth, taskID)
	}}
	if _, err := RetireTask(opts, rec, journals, auth); err != nil {
		t.Fatalf("cleanup with projection barrier: %v", err)
	}
	if len(rec.disposed) != 1 || len(rec.returned) != 1 {
		t.Fatalf("disposed=%v returned=%v, want endpoint and worktree released", rec.disposed, rec.returned)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "state", taskID+".meta")); !os.IsNotExist(err) {
		t.Fatalf("meta should be removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "state", taskID+".status")); !os.IsNotExist(err) {
		t.Fatalf("status artifact should be removed: %v", err)
	}
	assertClaimCompleted(t, auth, taskID, taskauthority.Generation(1))
}

func TestRetirementMetaSubstitutionCannotRedirectCleanup(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "meta-substitution"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	realWT := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(realWT, 0755)
	seedWorktreeEvidence(t, auth, taskID, realWT, "lease-wt", "fence-wt")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep", "fence-ep")

	// The mutable .meta projection claims a DIFFERENT window and worktree:
	// cleanup must ignore it and release only the evidence-pinned identities.
	writeRetireMeta(t, homeDir, taskID, "@999", filepath.Join(homeDir, "evil-wt"))

	rec := &recordingTeardown{alive: true}
	if _, err := RetireTask(Options{HomeDir: homeDir, ID: taskID, Force: true}, rec, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("retirement: %v", err)
	}
	if len(rec.disposed) != 1 || rec.disposed[0].Handle != "@1" {
		t.Fatalf("disposed=%+v, want evidence handle @1 (not the substituted %q)", rec.disposed, "@999")
	}
	if len(rec.returned) != 1 || rec.returned[0] != realWT {
		t.Fatalf("returned=%v, want evidence worktree %s (not the substituted path)", rec.returned, realWT)
	}
}

func TestRetirementMetaOnlyResourcesAreNeverReleased(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "meta-only"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	// No canonical endpoint/worktree evidence exists: .meta alone names
	// resources, and .meta must never authorize a release.
	writeRetireMeta(t, homeDir, taskID, "@1", filepath.Join(homeDir, "worktree"))

	rec := &recordingTeardown{alive: true}
	if _, err := RetireTask(Options{HomeDir: homeDir, ID: taskID, Force: true}, rec, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("retirement: %v", err)
	}
	if len(rec.disposed) != 0 || len(rec.returned) != 0 {
		t.Fatalf("meta-only resources were released: disposed=%v returned=%v", rec.disposed, rec.returned)
	}
}

func TestRetirementCrashResumeReusesReceiptWithoutDoubleTransition(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "crash-resume"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(wtDir, 0755)
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt", "fence-wt")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep", "fence-ep")
	writeRetireMeta(t, homeDir, taskID, "@1", wtDir)
	opts := Options{HomeDir: homeDir, ID: taskID, Force: true}

	// Crash after the canonical Retire commit and before the endpoint dispose.
	first := &recordingTeardown{alive: true, disposeErr: errors.New("window busy")}
	if _, err := RetireTask(opts, first, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup after dispose interruption")
	}
	if len(first.disposed) != 1 {
		t.Fatalf("first attempt dispose calls=%d, want 1", len(first.disposed))
	}
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	revAfterCrash := agg.Revision
	if agg.Phase != taskauthority.PhaseRetired {
		t.Fatalf("phase=%q after crash, want retired", agg.Phase)
	}

	// Resume: the committed receipt replays (no new authoritative transition)
	// and the remaining cleanup completes exactly once.
	second := &recordingTeardown{alive: true}
	if _, err := RetireTask(opts, second, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("resume: %v", err)
	}
	agg, err = auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != revAfterCrash+1 {
		// The retry replays the durable retire receipt without re-committing;
		// the only revision advance is the cleanup-claim completion mutation.
		t.Fatalf("retry advanced revision %d -> %d, want exactly one claim-completion bump", revAfterCrash, agg.Revision)
	}
	if len(second.returned) != 1 || second.returned[0] != wtDir {
		t.Fatalf("resume returned=%v, want evidence worktree %s", second.returned, wtDir)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "state", taskID+".meta")); !os.IsNotExist(err) {
		t.Fatal("meta should be removed on resume")
	}
}

func TestRetirementCrashResumeAfterWorktreeReturnInterruption(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "crash-resume-wt"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(wtDir, 0755)
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt", "fence-wt")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep", "fence-ep")
	writeRetireMeta(t, homeDir, taskID, "@1", wtDir)
	opts := Options{HomeDir: homeDir, ID: taskID, Force: true}

	// Crash after the endpoint dispose and before the worktree return.
	first := &recordingTeardown{alive: true, returnErr: errors.New("pool full")}
	if _, err := RetireTask(opts, first, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup after worktree-return interruption")
	}
	if len(first.disposed) != 1 || len(first.returned) != 1 {
		t.Fatalf("first attempt disposed=%v returned=%v, want endpoint disposed then worktree return attempted", first.disposed, first.returned)
	}
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	revAfterCrash := agg.Revision

	second := &recordingTeardown{alive: true}
	if _, err := RetireTask(opts, second, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("resume: %v", err)
	}
	agg, err = auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != revAfterCrash+1 {
		// The retry replays the durable retire receipt without re-committing;
		// the only revision advance is the cleanup-claim completion mutation.
		t.Fatalf("retry advanced revision %d -> %d, want exactly one claim-completion bump", revAfterCrash, agg.Revision)
	}
	if len(second.returned) != 1 || second.returned[0] != wtDir {
		t.Fatalf("resume returned=%v, want evidence worktree %s", second.returned, wtDir)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "state", taskID+".meta")); !os.IsNotExist(err) {
		t.Fatal("meta should be removed on resume")
	}
}

func TestRetirementCleanupFailurePreservesCanonicalEvidenceAndMergedTruth(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "evidence-preserved"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	// The identity-bearing retirement prerequisite is the canonical completed
	// delivery outcome (#414 B hard cut), not the .meta delivery_state
	// projection; seedMergedDelivery also binds the worktree/endpoint.
	seedMergedDelivery(t, auth, homeDir, taskID)
	writeRetireMeta(t, homeDir, taskID, "@1", filepath.Join(homeDir, "worktrees", taskID))
	opts := Options{HomeDir: homeDir, ID: taskID, Force: true}

	// First attempt fails at the worktree return; the canonical evidence and
	// the canonical completed delivery outcome must both survive.
	first := &recordingTeardown{alive: true, returnErr: errors.New("pool full")}
	if _, err := RetireTask(opts, first, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup")
	}
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Retirement == nil || agg.Retirement.Endpoint == nil || agg.Retirement.Worktree == nil {
		t.Fatalf("canonical evidence lost on cleanup failure: %+v", agg.Retirement)
	}
	outcome, err := auth.DeliveryOutcome(mustTaskID(t, taskID))
	if err != nil || outcome.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("canonical completed delivery outcome lost on cleanup failure: %v %+v", err, outcome)
	}

	// Retry completes the cleanup; the canonical retirement evidence must
	// still be preserved after the projection is removed.
	second := &recordingTeardown{alive: true}
	if _, err := RetireTask(opts, second, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("resume: %v", err)
	}
	agg, err = auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Retirement == nil || agg.Retirement.Endpoint == nil || agg.Retirement.Worktree == nil {
		t.Fatalf("canonical retirement evidence erased by cleanup: %+v", agg.Retirement)
	}
}

func TestRetirementReopenedGenerationOwnershipFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "ownership-conflict"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(wtDir, 0755)
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt-1", "fence-wt-1")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep-1", "fence-ep-1")
	writeRetireMeta(t, homeDir, taskID, "@1", wtDir)
	opts := Options{HomeDir: homeDir, ID: taskID, Force: true}

	// Commit the generation-1 retirement with interrupted cleanup.
	first := &recordingTeardown{alive: true, disposeErr: errors.New("window busy")}
	if _, err := RetireTask(opts, first, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup")
	}
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}

	// Reopening the task requires reconciling the durable cleanup claim first
	// (an active claim rejects Reopen, BEO-16/P1a). The operator escape hatch
	// is the abort: the claim is released WITHOUT cleanup completing, then the
	// task reopens to generation 2, which re-acquires the SAME endpoint
	// handle and worktree path (pool reuse) under NEW leases/fences.
	abortCleanupFor(t, auth, taskID, taskauthority.Generation(1))
	agg, err = auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	reopen := taskauthority.CanonicalReopenRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Reason:       "reopen",
	}
	op, err := domain.NewOperation(mustOpID(t, "op-reopen-"+taskID), reopen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Reopen(op, reopen); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt-2", "fence-wt-2")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep-2", "fence-ep-2")
	writeRetireMeta(t, homeDir, taskID, "@1", wtDir)

	// The old-generation retry must not release resources now owned by
	// generation 2: the ownership overlap fails closed before any release.
	second := &recordingTeardown{alive: true}
	_, err = RetireTask(opts, second, fakeRetirementJournals{}, auth)
	if err == nil {
		t.Fatal("expected fail-closed ownership conflict on old-generation retry")
	}
	var pending *RetirementCleanupPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("error = %T %v, want typed RetirementCleanupPendingError", err, err)
	}
	if !strings.Contains(pending.CleanupErr.Error(), "owns") {
		t.Fatalf("cleanup error = %v, want ownership conflict", pending.CleanupErr)
	}
	if len(second.disposed) != 0 || len(second.returned) != 0 {
		t.Fatalf("old-generation retry released resources: disposed=%v returned=%v", second.disposed, second.returned)
	}
	// Generation-2 canonical bindings are untouched.
	agg2, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg2.Generation != 2 || agg2.Endpoint == nil || agg2.Endpoint.LeaseID != "lease-ep-2" || agg2.Worktree == nil || agg2.Worktree.LeaseID != "lease-wt-2" {
		t.Fatalf("generation-2 resources disturbed: %+v", agg2)
	}
	// Generation-2 meta survives (the resumed old-generation cleanup never
	// removes the current projection).
	if _, err := os.Stat(filepath.Join(homeDir, "state", taskID+".meta")); err != nil {
		t.Fatalf("generation-2 meta destroyed: %v", err)
	}
}

func TestRetirementOldGenerationRetryReleasesOnlyOldPinnedResources(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "old-gen-release"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	oldWT := filepath.Join(homeDir, "worktrees", taskID+"-old")
	newWT := filepath.Join(homeDir, "worktrees", taskID+"-new")
	os.MkdirAll(oldWT, 0755)
	os.MkdirAll(newWT, 0755)
	seedWorktreeEvidence(t, auth, taskID, oldWT, "lease-wt-old", "fence-wt-old")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep-old", "fence-ep-old")
	writeRetireMeta(t, homeDir, taskID, "@1", oldWT)
	opts := Options{HomeDir: homeDir, ID: taskID, Force: true}

	// Commit the generation-1 retirement with interrupted cleanup.
	first := &recordingTeardown{alive: true, disposeErr: errors.New("window busy")}
	if _, err := RetireTask(opts, first, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup")
	}

	// The durable cleanup claim rejects a direct reopen of the still-active
	// claim; the operator aborts the claim first (escape hatch), then the
	// task reopens to generation 2, which acquires DIFFERENT resources.
	abortCleanupFor(t, auth, taskID, taskauthority.Generation(1))
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	reopen := taskauthority.CanonicalReopenRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Reason:       "reopen",
	}
	op, err := domain.NewOperation(mustOpID(t, "op-reopen-"+taskID), reopen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Reopen(op, reopen); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	seedWorktreeEvidence(t, auth, taskID, newWT, "lease-wt-new", "fence-wt-new")
	seedEndpointEvidence(t, auth, taskID, "@2", "lease-ep-new", "fence-ep-new")
	writeRetireMeta(t, homeDir, taskID, "@2", newWT)

	// The old-generation retry releases ONLY the old evidence-pinned
	// resources; generation-2's resources and projection survive.
	second := &recordingTeardown{alive: true}
	if _, err := RetireTask(opts, second, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("old-generation retry: %v", err)
	}
	if len(second.disposed) != 1 || second.disposed[0].Handle != "@1" {
		t.Fatalf("disposed=%+v, want old endpoint @1 only", second.disposed)
	}
	if len(second.returned) != 1 || second.returned[0] != oldWT {
		t.Fatalf("returned=%v, want old worktree %s only", second.returned, oldWT)
	}
	agg2, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg2.Generation != 2 || agg2.Endpoint == nil || agg2.Endpoint.Handle != "@2" || agg2.Worktree == nil || agg2.Worktree.Path != newWT {
		t.Fatalf("generation-2 resources disturbed: %+v", agg2)
	}
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if meta["window"] != "@2" {
		t.Fatalf("generation-2 meta window=%q, want @2 (projection untouched)", meta["window"])
	}
}
