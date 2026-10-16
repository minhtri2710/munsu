//go:build integration

package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// mergedShipFixture seeds one launched ship task whose worktree carries a
// canonical launch manifest, with a committed completed delivery outcome.
func mergedShipFixture(t *testing.T, homeDir, taskID string) *taskauthority.Canonical {
	t.Helper()
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	wt, digest := setupWorktreeWithManifest(t, filepath.Join(homeDir, "worktree"), filepath.Join(homeDir, "remote.git"), nil)
	seedRetirementManifestEvidence(t, auth, taskID, wt, digest)
	_, _, epLease, epFence := spawnReservationIdentities(taskID, 1)
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	attach := taskauthority.CanonicalAttachEndpointRequest{
		HomeID: auth.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Backend: "tmux", Handle: "@1", LeaseID: epLease, FenceToken: epFence,
		SessionOwner: "session-" + taskID, WorkspaceID: "ws-" + taskID, TabID: "tab-" + taskID, Incarnation: "inc-" + taskID,
		Reason: "attach",
	}
	if _, err := auth.AttachEndpoint(mustFleetOperation(t, "op-attach-"+taskID, attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}
	record := taskauthority.CanonicalRecordLaunchRequest{
		HomeID: auth.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)+1),
		LaunchID: "launch-" + taskID + "-1", CommandDigest: strings.Repeat("c", 64), Seat: unfencedSeat(), Reason: "record",
	}
	if _, err := auth.RecordLaunch(mustFleetOperation(t, "op-record-"+taskID, record), record); err != nil {
		t.Fatalf("RecordLaunch: %v", err)
	}
	seedEndpointEvidence(t, auth, taskID, "@1", epLease, epFence)
	seedSourceContract(t, auth, taskID, deliveryTestGitHubForge)
	seedCompletedDeliveryOutcome(t, auth, taskID)
	return auth
}

// landedStatus is the provider status of the fixture's merged PR.
func landedStatus() *domain.PRMergeStatus {
	return &domain.PRMergeStatus{State: "MERGED", Merged: true, HeadSHA: strings.Repeat("a", 40), MergedSHA: strings.Repeat("b", 40)}
}

func TestMergeAndRetireNilAuthorityFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-nil-auth"

	// A missing composed canonical Authority fails closed: no retirement
	// transition ever commits without one.
	result := MergeAndRetire(homeDir, taskID, deliverRequest(), fakeTeardown{mergeStatus: landedStatus(), alive: true}, fakeRetirementJournals{}, nil)
	if result == nil || !result.IsError() {
		t.Fatal("expected an error result when the authority is nil")
	}
	if !strings.Contains(result.MergeDetail, "composed task authority") {
		t.Fatalf("detail = %v, want composed-authority failure", result.MergeDetail)
	}
	if result.TeardownResult != nil {
		t.Fatal("teardown must not run without an authority")
	}
}

func TestMergeAndRetireRefusesUnreadableTargetGeneration(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "target-generation-read-error"
	auth := mergedShipFixture(t, homeDir, taskID)
	current := filepath.Join(homeDir, "state", "task-authority", "tasks", taskID, "current.json")
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(current, 0700); err != nil {
		t.Fatal(err)
	}

	result := MergeAndRetire(homeDir, taskID, deliverRequest(), fakeTeardown{mergeStatus: landedStatus(), alive: true}, fakeRetirementJournals{}, auth)
	if result == nil || result.MergeOutcome != taskauthority.DeliveryOutcomeRetryable {
		t.Fatalf("result = %+v, want retryable refusal", result)
	}
	if !strings.Contains(result.MergeDetail, "resolving target generation") {
		t.Fatalf("detail = %q, want target-generation read failure", result.MergeDetail)
	}
	if result.TeardownResult != nil {
		t.Fatal("retirement proceeded after target generation read failure")
	}
}

func TestMergeAndRetireRetiresThroughAuthority(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-retire-through"
	auth := mergedShipFixture(t, homeDir, taskID)

	result := MergeAndRetire(homeDir, taskID, deliverRequest(), fakeTeardown{mergeStatus: landedStatus(), alive: true}, fakeRetirementJournals{}, auth)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.MergeOutcome != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("merge outcome = %q, want completed (canonical outcome skip)", result.MergeOutcome)
	}
	if result.TeardownError != nil {
		t.Fatalf("unexpected teardown error: %v", result.TeardownError)
	}

	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseRetired || agg.Revision != 15 {
		t.Fatalf("aggregate = phase %q revision %d, want retired revision 15", agg.Phase, agg.Revision)
	}

	if result.IsError() {
		t.Fatal("expected IsError=false for fully successful retirement")
	}
}

type unsettledReportJournals struct{ fakeRetirementJournals }

func (unsettledReportJournals) VerifyRetirementContinuity(string, string, string) error {
	return errors.New("report unsettled")
}

// TestMergeAndRetireResumeKeepsReportContinuity: a pr-merge whose delivery is
// already committed still refuses teardown while a report is unsettled, and
// leaves the landed task done.
func TestMergeAndRetireResumeKeepsReportContinuity(t *testing.T) {
	for _, tc := range []struct {
		name      string
		journals  RetirementJournalPort
		wantPhase taskauthority.Phase
	}{
		{"blocks while a report is unsettled", unsettledReportJournals{}, taskauthority.PhaseDone},
		{"allows once reports are settled", fakeRetirementJournals{}, taskauthority.PhaseRetired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			taskID := "test-resume-continuity"
			auth := mergedShipFixture(t, homeDir, taskID)
			result := MergeAndRetire(homeDir, taskID, deliverRequest(), fakeTeardown{mergeStatus: landedStatus(), alive: true}, tc.journals, auth)
			if result.MergeOutcome != taskauthority.DeliveryOutcomeCompleted {
				t.Fatalf("merge outcome = %q, want completed", result.MergeOutcome)
			}
			refused := result.TeardownError != nil && strings.Contains(result.TeardownError.Error(), "report unsettled")
			if refused != (tc.wantPhase == taskauthority.PhaseDone) || (!refused && result.TeardownError != nil) {
				t.Fatalf("teardown error = %v", result.TeardownError)
			}
			agg, err := auth.Get(mustTaskID(t, taskID))
			if err != nil || agg.Phase != tc.wantPhase {
				t.Fatalf("phase = %q (%v), want %q", agg.Phase, err, tc.wantPhase)
			}
		})
	}
}

// The retry is not forced, so its safety check runs against what the failed
// attempt left: after a failed worktree return the artifacts are disposed.
func TestMergeAndRetireCleanupFailurePreservesCanonicalTruth(t *testing.T) {
	for _, tc := range []struct {
		name     string
		first    fakeTeardown
		failedAt taskauthority.Revision
	}{
		// Revision 13 = done, then retire committed the durable claim; the
		// failed attempt's BeginCleanup was a no-op (claim already active).
		{"session dispose fails", fakeTeardown{mergeStatus: landedStatus(), alive: true, disposeErr: errors.New("window busy")}, 13},
		// The disposal record is revision 14, committed before the return.
		{"worktree return fails after disposal", fakeTeardown{mergeStatus: landedStatus(), alive: true, returnWorktreeErr: errors.New("pool full")}, 14},
	} {
		t.Run(tc.name, func(t *testing.T) { mergeAndRetireResumesCleanup(t, tc.first, tc.failedAt) })
	}
}

func mergeAndRetireResumesCleanup(t *testing.T, firstTeardown fakeTeardown, failedAt taskauthority.Revision) {
	homeDir := t.TempDir()
	taskID := "test-cleanup-failure"
	auth := mergedShipFixture(t, homeDir, taskID)

	// First attempt: the canonical Retire op commits (durable receipt) but the
	// saga-side cleanup fails.
	first := MergeAndRetire(homeDir, taskID, deliverRequest(), firstTeardown, fakeRetirementJournals{}, auth)
	if first == nil || first.TeardownError == nil {
		t.Fatal("expected teardown error on cleanup failure")
	}
	var pending *RetirementCleanupPendingError
	if !errors.As(first.TeardownError, &pending) {
		t.Fatalf("teardown error = %T %v, want typed RetirementCleanupPendingError", first.TeardownError, first.TeardownError)
	}
	if pending.TaskID != taskID {
		t.Fatalf("pending task = %q, want %q", pending.TaskID, taskID)
	}
	if !first.IsError() {
		t.Fatal("expected IsError=true for retired-but-cleanup-pending")
	}

	// The committed retirement stands and the canonical completed delivery
	// outcome is preserved.
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseRetired || agg.Revision != failedAt {
		t.Fatalf("aggregate = phase %q revision %d, want retired revision %d", agg.Phase, agg.Revision, failedAt)
	}
	// The exact ownership evidence is preserved durably.
	if agg.Retirement == nil || agg.Retirement.Endpoint == nil || agg.Retirement.Worktree == nil {
		t.Fatalf("retirement evidence missing: %+v", agg.Retirement)
	}
	outcome, err := auth.DeliveryOutcome(mustTaskID(t, taskID))
	if err != nil {
		t.Fatalf("canonical delivery outcome lost: %v", err)
	}
	if outcome.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("canonical delivery outcome = %q, want completed", outcome.Status)
	}

	// Retry: delivery is never rerun (canonical completed outcome skips),
	// the retired phase is observed (no double transition, only the disposal
	// record and the claim completion advance to 15), and the cleanup resumes
	// to completion.
	second := MergeAndRetire(homeDir, taskID, deliverRequest(), fakeTeardown{mergeStatus: landedStatus(), alive: true}, fakeRetirementJournals{}, auth)
	if second == nil {
		t.Fatal("expected non-nil retry result")
	}
	if second.MergeOutcome != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("retry reran delivery: outcome = %q, want completed", second.MergeOutcome)
	}
	if second.TeardownError != nil {
		t.Fatalf("retry teardown error: %v", second.TeardownError)
	}
	agg, err = auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 15 {
		t.Fatalf("retry re-committed the retirement: revision = %d, want 15 (disposal record and claim completion only)", agg.Revision)
	}
}

func TestMergeAndRetireCrossHomeRetirement(t *testing.T) {
	// A handed-off task lives in a captain home: the fleet retirement path
	// targets the resolved task home and its composed canonical Authority, so
	// the authoritative retirement transition lands in the captain home.
	parent := t.TempDir()
	capHome := filepath.Join(parent, "captains", "cap1")
	if err := os.MkdirAll(capHome, 0700); err != nil {
		t.Fatal(err)
	}
	taskID := "test-cross-home"
	auth := mergedShipFixture(t, capHome, taskID)

	result := MergeAndRetire(capHome, taskID, deliverRequest(), fakeTeardown{mergeStatus: landedStatus(), alive: true}, fakeRetirementJournals{}, auth)
	if result == nil || result.TeardownError != nil {
		t.Fatalf("cross-home retirement failed: %+v", result)
	}
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseRetired {
		t.Fatalf("captain-home aggregate phase = %q, want retired", agg.Phase)
	}
}

func TestRetireTaskCleanupFailureReturnsResumableReceipt(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-receipt-resume"
	auth := mergedShipFixture(t, homeDir, taskID)

	// Direct teardown path: the canonical Retire op commits first, then the
	// cleanup fails — the typed partial result carries the committed state and
	// the partial steps, and the canonical completed delivery outcome stays
	// intact.
	opts := Options{HomeDir: homeDir, ID: taskID, Force: true}
	result, err := RetireTask(opts, fakeTeardown{mergeStatus: landedStatus(), alive: true, disposeErr: errors.New("window busy")}, fakeRetirementJournals{}, auth)
	if err == nil {
		t.Fatal("expected cleanup failure error")
	}
	var pending *RetirementCleanupPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("error = %T %v, want typed RetirementCleanupPendingError", err, err)
	}
	if result == nil {
		t.Fatal("expected a partial teardown result alongside the typed error")
	}
	outcome, err := auth.DeliveryOutcome(mustTaskID(t, taskID))
	if err != nil || outcome.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("canonical completed delivery outcome lost on cleanup failure: %v %+v", err, outcome)
	}

	// Resume: the retired phase is observed (same stable Operation identity
	// replays the durable receipt; only the artifact-disposal record and the
	// claim completion advance the revision) and the cleanup completes.
	_, err = RetireTask(opts, fakeTeardown{mergeStatus: landedStatus(), alive: true}, fakeRetirementJournals{}, auth)
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	agg, _ := auth.Get(mustTaskID(t, taskID))
	if agg.Phase != taskauthority.PhaseRetired || agg.Revision != 14 {
		t.Fatalf("aggregate after resume = %+v, want retired revision 14", agg)
	}
}
