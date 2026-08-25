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
func abortCleanupFor(t *testing.T, auth *taskauthority.Canonical, homeDir, taskID string, gen taskauthority.Generation) {
	t.Helper()
	if err := AbortRetirementCleanup(auth, homeDir, mustTaskID(t, taskID), gen); err != nil {
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

	// The fake runs the barrier attempts at Dispose entry — exactly the
	// reviewer's post-unlock window: the dispose fence revalidates under the
	// task lock and returns, the lock is released, and the very next thing is
	// the Dispose action. Every attempt must be rejected by the durable claim
	// (never observed as a successful reopen/rebind).
	second := &recordingTeardown{alive: true, onDispose: func() {
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

	second := &recordingTeardown{alive: true, onReturn: func() {
		// Barrier at ReturnWorktree entry: between the worktree-return fence
		// (lock released) and the ReturnWorktree action, a concurrent reopen
		// is rejected by the durable claim.
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

func TestRetirementAbortTerminalOldRetryNeverReleasesReopenedOwnership(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "abort-reuse"
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

	// The operator aborts the generation-1 claim (escape hatch) and reopens:
	// generation 2 re-acquires the SAME endpoint handle under a NEW lease.
	abortCleanupFor(t, auth, homeDir, taskID, taskauthority.Generation(1))
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

	// Abort is TERMINAL and the old teardown retry is a STALE continuation:
	// bound to the aborted generation-1 retirement, it fails closed with a
	// typed error and NEVER retires the reopened generation 2 (BEO-16/P1a) —
	// no claim activation, no release.
	stale := &recordingTeardown{alive: true}
	_, retErr := RetireTask(opts, stale, fakeRetirementJournals{}, auth)
	if retErr == nil {
		t.Fatal("stale teardown retry must fail closed")
	}
	var staleErr *RetirementStaleTeardownError
	if !errors.As(retErr, &staleErr) {
		t.Fatalf("stale teardown retry error = %v", retErr)
	}
	if staleErr.PriorGeneration != 1 || staleErr.CurrentGeneration != 2 || staleErr.TerminalStatus != "aborted" {
		t.Fatalf("stale error = %+v, want prior 1 / current 2 / aborted", staleErr)
	}
	if len(stale.disposed) != 0 || len(stale.returned) != 0 {
		t.Fatalf("stale retry released resources: disposed=%v returned=%v", stale.disposed, stale.returned)
	}
	aggMid, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if aggMid.Generation != 2 || aggMid.Phase != taskauthority.PhaseWorking || aggMid.CleanupClaim != nil {
		t.Fatalf("stale retry disturbed the reopened generation: %+v", aggMid)
	}

	// A FRESH teardown request bound to the reopened generation — the
	// distinct explicit target (BEO-16/P1a) — retires generation 2 and
	// releases only generation-2's own resources (the reused handle under ITS
	// lease).
	target := taskauthority.Generation(2)
	fresh := Options{HomeDir: homeDir, ID: taskID, Force: true, ExpectedGeneration: &target}
	second := &recordingTeardown{alive: true}
	if _, err := RetireTask(fresh, second, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("fresh teardown of reopened generation: %v", err)
	}
	if len(second.disposed) != 1 || second.disposed[0].Handle != "@1" {
		t.Fatalf("disposed=%+v, want exactly the reopened generation's own endpoint @1 (its cleanup, not the aborted generation-1 resume)", second.disposed)
	}
	if len(second.returned) != 1 || second.returned[0] != wtDir {
		t.Fatalf("returned=%v, want reopened generation's own worktree %s", second.returned, wtDir)
	}
	agg2, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg2.Generation != 2 || agg2.Phase != taskauthority.PhaseRetired {
		t.Fatalf("current aggregate = %+v, want generation 2 retired", agg2)
	}
	if agg2.CleanupClaim == nil || agg2.CleanupClaim.Generation != 2 {
		t.Fatalf("reopened generation claim = %+v, want the generation-2 claim (never the historical generation-1 claim)", agg2.CleanupClaim)
	}
	// The aborted generation-1 record is untouched: evidence preserved and the
	// claim stays aborted (terminal).
	hist, err := auth.GetGeneration(mustTaskID(t, taskID), taskauthority.Generation(1))
	if err != nil {
		t.Fatal(err)
	}
	if hist.Retirement == nil || hist.Retirement.Endpoint == nil || hist.Retirement.Endpoint.LeaseID != "lease-ep-1" || hist.Retirement.Worktree == nil || hist.Retirement.Worktree.LeaseID != "lease-wt-1" {
		t.Fatalf("generation-1 evidence disturbed: %+v", hist.Retirement)
	}
	if hist.CleanupClaim == nil || hist.CleanupClaim.Status != taskauthority.CleanupAborted || hist.CleanupClaim.Generation != 1 {
		t.Fatalf("generation-1 claim = %+v, want aborted (terminal)", hist.CleanupClaim)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "state", taskID+".meta")); !os.IsNotExist(err) {
		t.Fatalf("meta should be removed after the generation-2 retirement: %v", err)
	}
}

func TestRetirementAbortTerminalOldRetryReleasesOnlyReopenedResources(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "abort-diff"
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
	abortCleanupFor(t, auth, homeDir, taskID, taskauthority.Generation(1))
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

	// Abort is TERMINAL and the old teardown retry is a STALE continuation:
	// bound to the aborted generation-1 retirement, it fails closed with a
	// typed error and NEVER retires the reopened generation 2 (BEO-16/P1a).
	stale := &recordingTeardown{alive: true}
	_, retErr := RetireTask(opts, stale, fakeRetirementJournals{}, auth)
	if retErr == nil {
		t.Fatal("stale teardown retry must fail closed")
	}
	var staleErr *RetirementStaleTeardownError
	if !errors.As(retErr, &staleErr) {
		t.Fatalf("stale teardown retry error = %v", retErr)
	}
	if staleErr.PriorGeneration != 1 || staleErr.CurrentGeneration != 2 || staleErr.TerminalStatus != "aborted" {
		t.Fatalf("stale error = %+v, want prior 1 / current 2 / aborted", staleErr)
	}
	if len(stale.disposed) != 0 || len(stale.returned) != 0 {
		t.Fatalf("stale retry released resources: disposed=%v returned=%v", stale.disposed, stale.returned)
	}
	aggMid, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if aggMid.Generation != 2 || aggMid.Phase != taskauthority.PhaseWorking || aggMid.CleanupClaim != nil {
		t.Fatalf("stale retry disturbed the reopened generation: %+v", aggMid)
	}

	// A FRESH teardown request bound to the reopened generation — the
	// distinct explicit target (BEO-16/P1a) — retires generation 2 and
	// releases ONLY generation-2's own resources; the aborted generation-1
	// evidence and projection are never touched.
	target := taskauthority.Generation(2)
	fresh := Options{HomeDir: homeDir, ID: taskID, Force: true, ExpectedGeneration: &target}
	second := &recordingTeardown{alive: true}
	if _, err := RetireTask(fresh, second, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("fresh teardown of reopened generation: %v", err)
	}
	if len(second.disposed) != 1 || second.disposed[0].Handle != "@2" {
		t.Fatalf("disposed=%+v, want reopened generation's own endpoint @2 only (never the aborted generation-1 endpoint)", second.disposed)
	}
	if len(second.returned) != 1 || second.returned[0] != newWT {
		t.Fatalf("returned=%v, want reopened generation's own worktree %s (never %s)", second.returned, newWT, oldWT)
	}
	agg2, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg2.Generation != 2 || agg2.Phase != taskauthority.PhaseRetired || agg2.CleanupClaim == nil || agg2.CleanupClaim.Generation != 2 {
		t.Fatalf("current aggregate = %+v, want generation 2 retired with its own claim", agg2)
	}
	hist, err := auth.GetGeneration(mustTaskID(t, taskID), taskauthority.Generation(1))
	if err != nil {
		t.Fatal(err)
	}
	if hist.Retirement == nil || hist.Retirement.Endpoint == nil || hist.Retirement.Endpoint.LeaseID != "lease-ep-old" || hist.Retirement.Worktree == nil || hist.Retirement.Worktree.LeaseID != "lease-wt-old" {
		t.Fatalf("generation-1 evidence disturbed: %+v", hist.Retirement)
	}
	if hist.CleanupClaim == nil || hist.CleanupClaim.Status != taskauthority.CleanupAborted {
		t.Fatalf("generation-1 claim = %+v, want aborted (terminal)", hist.CleanupClaim)
	}
	// The reopened generation's own projection is removed after its fresh
	// retirement; the aborted generation-1 projection (oldWT meta) is gone
	// because the current projection describes generation 2.
	if _, err := os.Stat(filepath.Join(homeDir, "state", taskID+".meta")); !os.IsNotExist(err) {
		t.Fatalf("meta should be removed after the generation-2 retirement: %v", err)
	}
}

// TestRetirementAbortTerminalOldRetryNeverClaimsPreBindAcquisition proves the
// reviewer's Abort → Reopen → BeginSpawn/AttachEndpoint → old teardown retry
// scenario: after the operator aborts generation 1's cleanup (terminal) and
// reopens, the reopened generation records a launch intent and acquires the
// SAME endpoint handle/incarnation as generation 1 (pre-bind acquisition — no
// endpoint binding yet). An old teardown retry must NOT reactivate the
// historical claim, must NOT resume the aborted cleanup, and must NOT dispose
// the pre-bind acquired endpoint (BEO-16/P1a abort-terminal).
func TestRetirementAbortTerminalOldRetryNeverClaimsPreBindAcquisition(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "abort-prebind"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(wtDir, 0755)
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt-1", "fence-wt-1")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep-1", "fence-ep-1")
	writeRetireMeta(t, homeDir, taskID, "@1", wtDir)
	opts := Options{HomeDir: homeDir, ID: taskID, Force: true}

	// Generation-1 retirement with interrupted cleanup (claim stays active).
	first := &recordingTeardown{alive: true, disposeErr: errors.New("window busy")}
	if _, err := RetireTask(opts, first, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup")
	}

	// Operator aborts (terminal) and reopens to generation 2.
	abortCleanupFor(t, auth, homeDir, taskID, taskauthority.Generation(1))
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
	op, err := domain.NewOperation(mustOpID(t, "op-reopen-prebind"), reopen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Reopen(op, reopen); err != nil {
		t.Fatalf("Reopen after abort: %v", err)
	}

	// The reopened generation plans to acquire the SAME endpoint handle "@1"
	// and incarnation as generation 1 (pool reuse): launch intent + pre-bind
	// acquisition, no endpoint binding yet.
	begin := taskauthority.CanonicalBeginSpawnRequest{
		HomeID:                auth.HomeID(),
		TaskID:                mustTaskID(t, taskID),
		Precondition:          domain.Of(2, 1),
		SnapshotDigest:        strings.Repeat("a", 64),
		Backend:               "tmux",
		Harness:               "pi",
		Model:                 "opus",
		Effort:                "high",
		Mode:                  "direct-PR",
		Kind:                  "ship",
		Project:               "proj",
		ParentTaskID:          "parent",
		LaunchID:              "launch-" + taskID,
		WindowLabel:           "window-" + taskID,
		WorktreeReservationID: "wt-res-2",
		WorktreeFenceToken:    "wt-fence-2",
		EndpointReservationID: "ep-res-2",
		EndpointFenceToken:    "ep-fence-2",
		EndpointIncarnation:   "inc-" + taskID, // REUSED from generation 1
		Reason:                "spawn",
	}
	if _, err := auth.BeginSpawn(mustFleetOperation(t, "op-begin-2", begin), begin); err != nil {
		t.Fatalf("BeginSpawn(gen 2): %v", err)
	}
	wt2 := taskauthority.WorktreeBinding{
		RepositoryIdentity: "repo-" + taskID,
		Path:               wtDir,
		GitDir:             filepath.Join(wtDir, ".git"),
		CommonDir:          filepath.Join(filepath.Dir(wtDir), ".git"),
		Head:               strings.Repeat("b", 40),
		LeaseID:            "wt-res-2",
		FenceToken:         "wt-fence-2",
		BoundAtUnix:        time.Now().UnixNano(),
	}
	bindWT := taskauthority.CanonicalBindWorktreeRequest{HomeID: auth.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: domain.Of(2, 2), Binding: wt2, Reason: "spawn"}
	if _, err := auth.BindWorktree(mustFleetOperation(t, "op-bindwt-2", bindWT), bindWT); err != nil {
		t.Fatalf("BindWorktree(gen 2): %v", err)
	}
	attach := taskauthority.CanonicalAttachEndpointRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(2, 3),
		Backend:      "tmux",
		Handle:       "@1", // REUSED handle from generation 1
		LeaseID:      "ep-res-2",
		FenceToken:   "ep-fence-2",
		SessionOwner: "session-" + taskID,
		WorkspaceID:  "ws-" + taskID,
		TabID:        "tab-" + taskID,
		Incarnation:  "inc-" + taskID, // REUSED incarnation
		Reason:       "attach",
	}
	if _, err := auth.AttachEndpoint(mustFleetOperation(t, "op-attach-2", attach), attach); err != nil {
		t.Fatalf("AttachEndpoint(gen 2): %v", err)
	}
	agg2, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg2.CleanupClaim != nil {
		t.Fatalf("reopened generation leaked a cleanup claim before any retry: %+v", agg2.CleanupClaim)
	}
	if agg2.AcquiredEndpoint == nil || agg2.AcquiredEndpoint.Handle != "@1" || agg2.AcquiredEndpoint.Incarnation != "inc-"+taskID {
		t.Fatalf("pre-bind acquisition not recorded with the reused identity: %+v", agg2.AcquiredEndpoint)
	}

	// An old teardown retry cannot reactivate the historical claim: a direct
	// BeginCleanup with the generation-1 claim identity fails closed (newer
	// generation, terminal abort).
	histBegin := taskauthority.CanonicalBeginCleanupRequest{
		HomeID:           auth.HomeID(),
		TaskID:           mustTaskID(t, taskID),
		Precondition:     domain.Of(2, 4),
		ClaimOperationID: taskRetireOperationID(taskID, taskauthority.Generation(1)),
		ClaimGeneration:  taskauthority.Generation(1),
		Reason:           "old retry",
	}
	if _, err := auth.BeginCleanup(mustFleetOperation(t, "op-hist-begin", histBegin), histBegin); !errors.Is(err, taskauthority.ErrConflict) {
		t.Fatalf("BeginCleanup of historical claim on reopened generation = %v, want ErrConflict", err)
	}

	// The old teardown retry through RetireTask is a STALE continuation bound
	// to the aborted generation-1 retirement: it fails closed with a typed
	// error and NEVER retires the reopened generation 2 — the pre-bind
	// acquired endpoint is untouched (no claim activation, no dispose).
	stale := &recordingTeardown{alive: true}
	_, retErr := RetireTask(opts, stale, fakeRetirementJournals{}, auth)
	if retErr == nil {
		t.Fatal("stale teardown retry must fail closed")
	}
	var staleErr *RetirementStaleTeardownError
	if !errors.As(retErr, &staleErr) {
		t.Fatalf("stale teardown retry error = %v", retErr)
	}
	if staleErr.PriorGeneration != 1 || staleErr.CurrentGeneration != 2 || staleErr.TerminalStatus != "aborted" {
		t.Fatalf("stale error = %+v, want prior 1 / current 2 / aborted", staleErr)
	}
	if len(stale.disposed) != 0 || len(stale.returned) != 0 {
		t.Fatalf("stale retry released resources: disposed=%v returned=%v", stale.disposed, stale.returned)
	}
	aggMid, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if aggMid.Generation != 2 || aggMid.Phase != taskauthority.PhaseQueued || aggMid.CleanupClaim != nil || aggMid.AcquiredEndpoint == nil || aggMid.AcquiredEndpoint.Handle != "@1" {
		t.Fatalf("stale retry disturbed the reopened generation: %+v", aggMid)
	}

	// A FRESH teardown request bound to the reopened generation (the distinct
	// explicit target, BEO-16/P1a) retires generation 2. Its preserved
	// pre-bind acquired endpoint (the known externally held resource) is now
	// reconciled: probed and disposed, never completed-unresolved.
	target := taskauthority.Generation(2)
	fresh := Options{HomeDir: homeDir, ID: taskID, Force: true, ExpectedGeneration: &target}
	second := &recordingTeardown{alive: true}
	if _, err := RetireTask(fresh, second, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("fresh teardown of reopened generation: %v", err)
	}
	if len(second.disposed) != 1 || second.disposed[0].Handle != "@1" || second.disposed[0].Backend != "tmux" {
		t.Fatalf("disposed=%+v, want exactly the reopened generation's own acquired endpoint @1 (its cleanup, never the aborted generation-1 resume)", second.disposed)
	}
	if len(second.returned) != 1 || second.returned[0] != wtDir {
		t.Fatalf("returned=%v, want reopened generation's own worktree %s", second.returned, wtDir)
	}
	agg2, err = auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg2.CleanupClaim == nil || agg2.CleanupClaim.Generation != 2 || agg2.CleanupClaim.Status != taskauthority.CleanupCompleted {
		t.Fatalf("reopened generation claim = %+v, want its OWN completed generation-2 claim (never the historical generation-1 claim)", agg2.CleanupClaim)
	}
	hist, err := auth.GetGeneration(mustTaskID(t, taskID), taskauthority.Generation(1))
	if err != nil {
		t.Fatal(err)
	}
	if hist.CleanupClaim == nil || hist.CleanupClaim.Status != taskauthority.CleanupAborted || hist.CleanupClaim.Generation != 1 {
		t.Fatalf("generation-1 claim = %+v, want aborted (terminal, never resumed)", hist.CleanupClaim)
	}
	if hist.Retirement == nil || hist.Retirement.Endpoint == nil || hist.Retirement.Endpoint.LeaseID != "lease-ep-1" {
		t.Fatalf("generation-1 evidence disturbed: %+v", hist.Retirement)
	}
}

// TestRetirementAbortTerminalSameGenerationRetryNeverResumes proves that a
// teardown retry on a task whose claim was aborted (same generation still
// current) does NOT re-activate or resume the cleanup: abort is terminal, the
// retry reports the terminal state, and nothing is released.
func TestRetirementAbortTerminalSameGenerationRetryNeverResumes(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "abort-samegen"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(wtDir, 0755)
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt-1", "fence-wt-1")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep-1", "fence-ep-1")
	writeRetireMeta(t, homeDir, taskID, "@1", wtDir)
	opts := Options{HomeDir: homeDir, ID: taskID, Force: true}

	// Interrupted cleanup: claim active.
	first := &recordingTeardown{alive: true, disposeErr: errors.New("window busy")}
	if _, err := RetireTask(opts, first, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup")
	}

	// Operator aborts; the task stays on generation 1 with an aborted claim.
	abortCleanupFor(t, auth, homeDir, taskID, taskauthority.Generation(1))

	// A teardown retry reports the terminal abort and releases nothing: the
	// claim is never re-activated and the evidence-pinned endpoint/worktree
	// are never disposed/returned.
	second := &recordingTeardown{alive: true}
	result, err := RetireTask(opts, second, fakeRetirementJournals{}, auth)
	if err != nil {
		t.Fatalf("teardown retry after abort: %v", err)
	}
	if len(second.disposed) != 0 || len(second.returned) != 0 {
		t.Fatalf("aborted cleanup released resources: disposed=%v returned=%v", second.disposed, second.returned)
	}
	found := false
	for _, s := range result.Steps {
		if strings.Contains(s, "abort is terminal") {
			found = true
		}
	}
	if !found {
		t.Fatalf("retry steps = %v, want an 'abort is terminal' step", result.Steps)
	}
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != taskauthority.CleanupAborted {
		t.Fatalf("claim not retained as aborted: %+v", agg.CleanupClaim)
	}
	// The projectons survive (nothing was released/removed).
	if _, err := os.Stat(filepath.Join(homeDir, "state", taskID+".meta")); err != nil {
		t.Fatalf("meta destroyed on aborted retry: %v", err)
	}
}

// TestRetirementAcquiredEndpointResolvedOnFreshTeardown proves High-2 closure
// (BEO-16/P1a): a fresh teardown of a generation that acquired an endpoint
// pre-bind (launch intent + AttachEndpoint, never bound) preserves the exact
// acquired identity as cleanup evidence and DISPOSES the known externally held
// resource — the cleanup claim never completes while a preserved acquired
// endpoint remains unresolved.
func TestRetirementAcquiredEndpointResolvedOnFreshTeardown(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "acquired-fresh"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(wtDir, 0755)
	writeRetireMeta(t, homeDir, taskID, "@1", wtDir)

	// The task acquires an endpoint pre-bind (never binds it): launch intent
	// + AttachEndpoint with the exact acquired identity.
	begin := taskauthority.CanonicalBeginSpawnRequest{
		HomeID:                auth.HomeID(),
		TaskID:                mustTaskID(t, taskID),
		Precondition:          domain.Of(1, 1),
		SnapshotDigest:        strings.Repeat("a", 64),
		Backend:               "tmux",
		Harness:               "pi",
		Model:                 "opus",
		Effort:                "high",
		Mode:                  "direct-PR",
		Kind:                  "ship",
		Project:               "proj",
		ParentTaskID:          "parent",
		LaunchID:              "launch-" + taskID,
		WindowLabel:           "window-" + taskID,
		WorktreeReservationID: "wt-res-1",
		WorktreeFenceToken:    "wt-fence-1",
		EndpointReservationID: "ep-res-1",
		EndpointFenceToken:    "ep-fence-1",
		EndpointIncarnation:   "inc-" + taskID,
		Reason:                "spawn",
	}
	if _, err := auth.BeginSpawn(mustFleetOperation(t, "op-begin-"+taskID, begin), begin); err != nil {
		t.Fatalf("BeginSpawn: %v", err)
	}
	attach := taskauthority.CanonicalAttachEndpointRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(1, 2),
		Backend:      "tmux",
		Handle:       "@1",
		LeaseID:      "ep-res-1",
		FenceToken:   "ep-fence-1",
		SessionOwner: "session-" + taskID,
		WorkspaceID:  "ws-" + taskID,
		TabID:        "tab-" + taskID,
		Incarnation:  "inc-" + taskID,
		Reason:       "attach",
	}
	if _, err := auth.AttachEndpoint(mustFleetOperation(t, "op-attach-"+taskID, attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.AcquiredEndpoint == nil || agg.AcquiredEndpoint.Handle != "@1" {
		t.Fatalf("acquired endpoint not recorded: %+v", agg.AcquiredEndpoint)
	}

	// A fresh teardown (no prior retirement) retires the generation: the
	// preserved acquired endpoint is probed (live) and DISPOSED, and the
	// claim completes only after the known externally held resource is
	// reconciled.
	rec := &recordingTeardown{alive: true}
	if _, err := RetireTask(Options{HomeDir: homeDir, ID: taskID, Force: true}, rec, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("fresh teardown: %v", err)
	}
	if len(rec.disposed) != 1 || rec.disposed[0].Handle != "@1" || rec.disposed[0].Backend != "tmux" {
		t.Fatalf("disposed=%+v, want the acquired endpoint @1 disposed (never completed-unresolved)", rec.disposed)
	}
	if len(rec.returned) != 0 {
		t.Fatalf("returned=%v, want no worktree (none was bound)", rec.returned)
	}
	agg2, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg2.Phase != taskauthority.PhaseRetired || agg2.CleanupClaim == nil || agg2.CleanupClaim.Status != taskauthority.CleanupCompleted {
		t.Fatalf("current aggregate = %+v, want retired with a completed claim", agg2)
	}
	if agg2.Retirement == nil || agg2.Retirement.Acquired == nil ||
		agg2.Retirement.Acquired.Backend != "tmux" || agg2.Retirement.Acquired.Handle != "@1" ||
		agg2.Retirement.Acquired.LeaseID != "ep-res-1" || agg2.Retirement.Acquired.FenceToken != "ep-fence-1" ||
		agg2.Retirement.Acquired.Incarnation != "inc-"+taskID || agg2.Retirement.Generation != 1 {
		t.Fatalf("acquired evidence not preserved with the exact identity: %+v", agg2.Retirement)
	}
}

// TestRetirementStaleRetryAfterCompletedPriorNeverRetiresReopenedGeneration
// proves the invocation binding also covers a COMPLETED prior retirement: a
// delayed retry of the original teardown must never implicitly retire the
// reopened generation even though the prior cleanup finished normally — a
// fresh teardown requires the distinct explicit target (BEO-16/P1a).
func TestRetirementStaleRetryAfterCompletedPriorNeverRetiresReopenedGeneration(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "stale-completed"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(wtDir, 0755)
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt-1", "fence-wt-1")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep-1", "fence-ep-1")
	writeRetireMeta(t, homeDir, taskID, "@1", wtDir)
	opts := Options{HomeDir: homeDir, ID: taskID, Force: true}

	// Generation-1 retirement completes normally (claim completed).
	first := &recordingTeardown{alive: true}
	if _, err := RetireTask(opts, first, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("generation-1 teardown: %v", err)
	}

	// Reopen to generation 2; it acquires NEW resources.
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
	op, err := domain.NewOperation(mustOpID(t, "op-reopen-stale-completed"), reopen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Reopen(op, reopen); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt-2", "fence-wt-2")
	seedEndpointEvidence(t, auth, taskID, "@2", "lease-ep-2", "fence-ep-2")
	writeRetireMeta(t, homeDir, taskID, "@2", wtDir)

	// A delayed retry of the ORIGINAL teardown (nil target) is a stale
	// continuation of the completed generation-1 retirement: typed error,
	// generation 2 never retired.
	stale := &recordingTeardown{alive: true}
	_, retErr := RetireTask(opts, stale, fakeRetirementJournals{}, auth)
	if retErr == nil {
		t.Fatal("stale teardown retry must fail closed")
	}
	var staleErr *RetirementStaleTeardownError
	if !errors.As(retErr, &staleErr) {
		t.Fatalf("stale teardown retry error = %v", retErr)
	}
	if staleErr.PriorGeneration != 1 || staleErr.CurrentGeneration != 2 || staleErr.TerminalStatus != "completed" {
		t.Fatalf("stale error = %+v, want prior 1 / current 2 / completed", staleErr)
	}
	if len(stale.disposed) != 0 || len(stale.returned) != 0 {
		t.Fatalf("stale retry released resources: disposed=%v returned=%v", stale.disposed, stale.returned)
	}
	aggMid, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if aggMid.Generation != 2 || aggMid.Phase != taskauthority.PhaseWorking || aggMid.CleanupClaim != nil {
		t.Fatalf("stale retry disturbed the reopened generation: %+v", aggMid)
	}

	// The distinct explicit request retires generation 2.
	target := taskauthority.Generation(2)
	fresh := Options{HomeDir: homeDir, ID: taskID, Force: true, ExpectedGeneration: &target}
	second := &recordingTeardown{alive: true}
	if _, err := RetireTask(fresh, second, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("fresh teardown of reopened generation: %v", err)
	}
	if len(second.disposed) != 1 || second.disposed[0].Handle != "@2" {
		t.Fatalf("disposed=%+v, want the reopened generation's own endpoint @2", second.disposed)
	}
	agg2, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg2.Generation != 2 || agg2.Phase != taskauthority.PhaseRetired || agg2.CleanupClaim == nil || agg2.CleanupClaim.Generation != 2 || agg2.CleanupClaim.Status != taskauthority.CleanupCompleted {
		t.Fatalf("current aggregate = %+v, want generation 2 retired with its own completed claim", agg2)
	}
	hist, err := auth.GetGeneration(mustTaskID(t, taskID), taskauthority.Generation(1))
	if err != nil {
		t.Fatal(err)
	}
	if hist.CleanupClaim == nil || hist.CleanupClaim.Status != taskauthority.CleanupCompleted || hist.Retirement == nil || hist.Retirement.Endpoint == nil || hist.Retirement.Endpoint.LeaseID != "lease-ep-1" {
		t.Fatalf("generation-1 record disturbed: claim=%+v evidence=%+v", hist.CleanupClaim, hist.Retirement)
	}
}

// TestRetirementExplicitTargetConflictWhenGenerationAdvanced proves a teardown
// pinned to an expected generation (Options.ExpectedGeneration) that observes
// the current generation advanced past the target fails closed with the typed
// conflict — it never retires the newer generation.
func TestRetirementExplicitTargetConflictWhenGenerationAdvanced(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "target-advanced"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(wtDir, 0755)
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt-1", "fence-wt-1")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep-1", "fence-ep-1")
	writeRetireMeta(t, homeDir, taskID, "@1", wtDir)
	opts := Options{HomeDir: homeDir, ID: taskID, Force: true}

	// Generation-1 retirement with interrupted cleanup, then abort + reopen.
	first := &recordingTeardown{alive: true, disposeErr: errors.New("window busy")}
	if _, err := RetireTask(opts, first, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup")
	}
	abortCleanupFor(t, auth, homeDir, taskID, taskauthority.Generation(1))
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
	op, err := domain.NewOperation(mustOpID(t, "op-reopen-target-adv"), reopen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Reopen(op, reopen); err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	// A retry pinned to generation 1 observes generation 2 current: typed
	// target conflict, nothing released.
	pinned := taskauthority.Generation(1)
	pinnedOpts := Options{HomeDir: homeDir, ID: taskID, Force: true, ExpectedGeneration: &pinned}
	rec := &recordingTeardown{alive: true}
	_, retErr := RetireTask(pinnedOpts, rec, fakeRetirementJournals{}, auth)
	if retErr == nil {
		t.Fatal("pinned retry must fail closed")
	}
	var conflict *RetirementTargetConflictError
	if !errors.As(retErr, &conflict) {
		t.Fatalf("pinned retry error = %v, want *RetirementTargetConflictError", retErr)
	}
	if conflict.Target != 1 || conflict.Current != 2 {
		t.Fatalf("conflict = %+v, want target 1 / current 2", conflict)
	}
	if len(rec.disposed) != 0 || len(rec.returned) != 0 {
		t.Fatalf("pinned retry released resources: disposed=%v returned=%v", rec.disposed, rec.returned)
	}
	agg2, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg2.Generation != 2 || agg2.Phase != taskauthority.PhaseQueued || agg2.CleanupClaim != nil {
		t.Fatalf("pinned retry disturbed the reopened generation: %+v", agg2)
	}
}

// TestRetirementPinnedRetryNeverReplaysNewerRetiredGeneration proves the
// invocation binding is enforced BEFORE the retired-phase replay (BEO-16/P1a):
// a teardown pinned to generation 1 that arrives after the task reopened to
// generation 2 AND generation 2 was itself retired fails closed with the
// typed target conflict — it is never reported as a successful replay of
// generation 2's retirement and never releases anything. Only a replay pinned
// to the exact current generation may replay.
// TestRetirementPinnedRetryNeverReplaysNewerRetiredGeneration proves the
// invocation binding is enforced BEFORE the retired-phase replay (BEO-16/P1a):
// a teardown pinned to generation 1 that arrives after the task reopened to
// generation 2 AND generation 2 is itself already retired (claim still active,
// cleanup interrupted) fails closed with the typed target conflict — it is
// never reported as a successful replay of generation 2's retirement and
// never resumes its cleanup. Only a replay pinned to the exact current
// generation may replay.
func TestRetirementPinnedRetryNeverReplaysNewerRetiredGeneration(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "pinned-replay"
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(wtDir, 0755)
	opts := Options{HomeDir: homeDir, ID: taskID, Force: true}

	// Generation-1 retirement completes normally (claim completed).
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt-1", "fence-wt-1")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep-1", "fence-ep-1")
	writeRetireMeta(t, homeDir, taskID, "@1", wtDir)
	first := &recordingTeardown{alive: true}
	if _, err := RetireTask(opts, first, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("generation-1 teardown: %v", err)
	}

	// Reopen to generation 2; its teardown retires it but the cleanup is
	// interrupted (claim stays ACTIVE on the retired generation 2).
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
	op, err := domain.NewOperation(mustOpID(t, "op-reopen-pinned-replay"), reopen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Reopen(op, reopen); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt-2", "fence-wt-2")
	seedEndpointEvidence(t, auth, taskID, "@2", "lease-ep-2", "fence-ep-2")
	writeRetireMeta(t, homeDir, taskID, "@2", wtDir)
	target2 := taskauthority.Generation(2)
	secondOpts := Options{HomeDir: homeDir, ID: taskID, Force: true, ExpectedGeneration: &target2}
	second := &recordingTeardown{alive: true, disposeErr: errors.New("window busy")}
	if _, err := RetireTask(secondOpts, second, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup for generation 2")
	}
	aggRet, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if aggRet.Generation != 2 || aggRet.Phase != taskauthority.PhaseRetired || aggRet.CleanupClaim == nil || aggRet.CleanupClaim.Generation != 2 || aggRet.CleanupClaim.Status != taskauthority.CleanupActive {
		t.Fatalf("generation 2 not retired with active claim: %+v", aggRet)
	}

	// A delayed retry pinned to generation 1 observes generation 2 current
	// (retired, claim active): typed target conflict — never a Replayed
	// success for generation 2, never a resume of its cleanup.
	pinned := taskauthority.Generation(1)
	pinnedOpts := Options{HomeDir: homeDir, ID: taskID, Force: true, ExpectedGeneration: &pinned}
	rec := &recordingTeardown{alive: true}
	_, retErr := RetireTask(pinnedOpts, rec, fakeRetirementJournals{}, auth)
	if retErr == nil {
		t.Fatal("pinned retry must fail closed, not replay the newer retirement")
	}
	var conflict *RetirementTargetConflictError
	if !errors.As(retErr, &conflict) {
		t.Fatalf("pinned retry error = %v, want *RetirementTargetConflictError", retErr)
	}
	if conflict.Target != 1 || conflict.Current != 2 {
		t.Fatalf("conflict = %+v, want target 1 / current 2", conflict)
	}
	if len(rec.disposed) != 0 || len(rec.returned) != 0 {
		t.Fatalf("pinned retry released resources: disposed=%v returned=%v", rec.disposed, rec.returned)
	}
	agg2, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg2.Generation != 2 || agg2.Phase != taskauthority.PhaseRetired || agg2.CleanupClaim == nil || agg2.CleanupClaim.Generation != 2 || agg2.CleanupClaim.Status != taskauthority.CleanupActive {
		t.Fatalf("generation-2 retirement disturbed: %+v", agg2)
	}
	if agg2.Retirement == nil || agg2.Retirement.Endpoint == nil || agg2.Retirement.Endpoint.LeaseID != "lease-ep-2" {
		t.Fatalf("generation-2 evidence disturbed: %+v", agg2.Retirement)
	}

	// The exact-current retry (pinned to generation 2) still resumes the
	// interrupted generation-2 cleanup and completes it.
	resume := &recordingTeardown{alive: true}
	if _, err := RetireTask(secondOpts, resume, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("generation-2 cleanup resume: %v", err)
	}
	if len(resume.disposed) != 1 || resume.disposed[0].Handle != "@2" {
		t.Fatalf("resume disposed=%+v, want generation-2 endpoint @2", resume.disposed)
	}
	agg3, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg3.CleanupClaim == nil || agg3.CleanupClaim.Status != taskauthority.CleanupCompleted {
		t.Fatalf("generation-2 claim not completed: %+v", agg3.CleanupClaim)
	}
	hist, err := auth.GetGeneration(mustTaskID(t, taskID), taskauthority.Generation(1))
	if err != nil {
		t.Fatal(err)
	}
	if hist.CleanupClaim == nil || hist.CleanupClaim.Status != taskauthority.CleanupCompleted {
		t.Fatalf("generation-1 record disturbed: %+v", hist.CleanupClaim)
	}
}
