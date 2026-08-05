package taskauthority

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// TestCanonicalReservationFenceRejectsEveryMutationFamily reserves a source
// task, rereads the updated revision, and proves that EVERY unrelated mutation
// family fails closed while the reservation is active, even when the caller
// supplies the latest revision.
func TestCanonicalReservationFenceRejectsEveryMutationFamily(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")

	// Reread the updated revision (the reservation advanced it to 2).
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 2 || agg.Transfer == nil || agg.Transfer.ReservationID != "res-t1" {
		t.Fatalf("reserved aggregate = %+v", agg)
	}
	prec := preconditionOf(1, uint64(agg.Revision))

	taskID := mustTaskID(t, "t1")

	// Start.
	start := CanonicalStartRequest{HomeID: c.HomeID(), TaskID: taskID, Precondition: prec, Reason: "start"}
	if _, err := c.Start(mustOperation(t, "op-fence-start", start), start); !errors.Is(err, ErrConflict) {
		t.Fatalf("Start on reserved task = %v, want ErrConflict", err)
	}
	// Block.
	block := CanonicalBlockRequest{HomeID: c.HomeID(), TaskID: taskID, Precondition: prec, Detail: "d", Reason: "block"}
	if _, err := c.Block(mustOperation(t, "op-fence-block", block), block); !errors.Is(err, ErrConflict) {
		t.Fatalf("Block on reserved task = %v, want ErrConflict", err)
	}
	// Unblock.
	unblock := CanonicalUnblockRequest{HomeID: c.HomeID(), TaskID: taskID, Precondition: prec, Reason: "unblock"}
	if _, err := c.Unblock(mustOperation(t, "op-fence-unblock", unblock), unblock); !errors.Is(err, ErrConflict) {
		t.Fatalf("Unblock on reserved task = %v, want ErrConflict", err)
	}
	// Complete.
	complete := CanonicalCompleteRequest{HomeID: c.HomeID(), TaskID: taskID, Precondition: prec, To: PhaseDone, Reason: "done"}
	if _, err := c.Complete(mustOperation(t, "op-fence-complete", complete), complete); !errors.Is(err, ErrConflict) {
		t.Fatalf("Complete on reserved task = %v, want ErrConflict", err)
	}
	// Reopen.
	reopen := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: taskID, Precondition: prec, Reason: "reopen"}
	if _, err := c.Reopen(mustOperation(t, "op-fence-reopen", reopen), reopen); !errors.Is(err, ErrConflict) {
		t.Fatalf("Reopen on reserved task = %v, want ErrConflict", err)
	}
	// BindWorktree.
	bw := bindWorktreeRequest(c, "t1", prec)
	if _, err := c.BindWorktree(mustOperation(t, "op-fence-bindwt", bw), bw); !errors.Is(err, ErrConflict) {
		t.Fatalf("BindWorktree on reserved task = %v, want ErrConflict", err)
	}
	// BindEndpoint.
	be := bindEndpointRequest(c, "t1", prec)
	if _, err := c.BindEndpoint(mustOperation(t, "op-fence-bindep", be), be); !errors.Is(err, ErrConflict) {
		t.Fatalf("BindEndpoint on reserved task = %v, want ErrConflict", err)
	}
	// Retire.
	retire := retireRequest(t, c, "t1", prec)
	if _, err := c.Retire(mustOperation(t, "op-fence-retire", retire), retire); !errors.Is(err, ErrConflict) {
		t.Fatalf("Retire on reserved task = %v, want ErrConflict", err)
	}
}

// TestCanonicalReservationFenceSurvivesRestart proves the fence is enforced
// after a home reopen: a fresh Canonical over the same home still rejects
// unrelated mutations on the reserved task and still allows the transfer
// continuation for the exact reservation.
func TestCanonicalReservationFenceSurvivesRestart(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")

	h2, err := home.Open(root)
	if err != nil {
		t.Fatalf("home.Open: %v", err)
	}
	c2, err := NewCanonical(h2)
	if err != nil {
		t.Fatal(err)
	}
	agg, err := c2.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Transfer == nil || agg.Transfer.ReservationID != "res-t1" {
		t.Fatalf("reservation lost across restart: %+v", agg.Transfer)
	}

	// Unrelated mutation still fails after restart.
	start := CanonicalStartRequest{HomeID: c2.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, uint64(agg.Revision)), Reason: "start"}
	if _, err := c2.Start(mustOperation(t, "op-fence-restart-start", start), start); !errors.Is(err, ErrConflict) {
		t.Fatalf("Start on reserved task after restart = %v, want ErrConflict", err)
	}

	// The transfer continuation (commit) still works for the exact reservation.
	commit := commitTransferRequest(t, c2, "t1", preconditionOf(1, uint64(agg.Revision)), "res-t1", "dest-home")
	if _, err := c2.CommitTransfer(mustOperation(t, "op-commit-restart", commit), commit); err != nil {
		t.Fatalf("CommitTransfer after restart: %v", err)
	}
}

// TestCanonicalReservationFenceRejectsStaleFenceToken proves a stale process
// retaining an old fence token cannot commit after reservation fence state
// changes: the commit requires the exact current fence token.
func TestCanonicalReservationFenceRejectsStaleFenceToken(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}

	// Wrong fence token under the correct reservation ID must fail closed.
	commit := commitTransferRequest(t, c, "t1", preconditionOf(1, uint64(agg.Revision)), "res-t1", "dest-home")
	commit.FenceToken = "stale-fence"
	if _, err := c.CommitTransfer(mustOperation(t, "op-commit-stale-fence", commit), commit); !errors.Is(err, ErrConflict) {
		t.Fatalf("commit with stale fence token = %v, want ErrConflict", err)
	}
}

// TestCanonicalReservationFenceAllowsTransferReplay proves replaying the
// already-committed reserve operation remains allowed while the fence is
// active: idempotent transfer continuation is not blocked by the fence.
func TestCanonicalReservationFenceAllowsTransferReplay(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := reserveTransferRequest(t, c, "t1", preconditionOf(1, 1), "res-t1", "dest-home")
	op := mustOperation(t, "op-reserve-replay-fence", req)
	if _, err := c.ReserveTransfer(op, req); err != nil {
		t.Fatalf("ReserveTransfer: %v", err)
	}
	// Replay the reserve operation while the reservation is active.
	replayed, err := c.ReserveTransfer(op, req)
	if err != nil {
		t.Fatalf("replay reserve under fence: %v", err)
	}
	if !replayed.Replayed {
		t.Fatalf("replay not marked replayed: %+v", replayed)
	}
}

// TestCanonicalReserveTransferStaleFenceTokenRejected proves a ReserveTransfer
// that supplies an unsafe fence token fails closed (the fence identity is
// validated, not merely persisted).
func TestCanonicalReserveTransferStaleFenceTokenRejected(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := reserveTransferRequest(t, c, "t1", preconditionOf(1, 1), "res-bad", "dest-home")
	req.FenceToken = "bad/token"
	if _, err := c.ReserveTransfer(mustOperation(t, "op-reserve-bad-fence", req), req); err == nil {
		t.Fatalf("reserve with unsafe fence token = nil error, want failure")
	}
}

// TestCanonicalCommitTransferRejectsMismatchedEvidence proves CommitTransfer
// fails closed when the activation evidence does not bind the exact source
// reservation: a mismatched destination home in the evidence conflicts.
func TestCanonicalCommitTransferRejectsMismatchedEvidence(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}

	commit := commitTransferRequest(t, c, "t1", preconditionOf(1, uint64(agg.Revision)), "res-t1", "dest-home")
	commit.Evidence.DestinationHome = "other-home"
	if _, err := c.CommitTransfer(mustOperation(t, "op-commit-bad-evidence", commit), commit); !errors.Is(err, ErrConflict) {
		t.Fatalf("commit with mismatched evidence = %v, want ErrConflict", err)
	}
}

// TestCanonicalCommitTransferRejectsMissingEvidence proves CommitTransfer
// fails closed when the activation evidence is missing: the source is never
// superseded without destination-activation proof bound to the reservation.
func TestCanonicalCommitTransferRejectsMissingEvidence(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}

	commit := commitTransferRequest(t, c, "t1", preconditionOf(1, uint64(agg.Revision)), "res-t1", "dest-home")
	commit.Evidence = TransferActivationInfo{}
	if _, err := c.CommitTransfer(mustOperation(t, "op-commit-no-evidence", commit), commit); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("commit without evidence = %v, want validation failure", err)
	}
}

// TestCanonicalReservedTaskReadinessNotReady proves a Current=true task with an
// active transfer reservation returns not-ready with the reservation reason,
// and that the behavior survives a home reopen/reread.
func TestCanonicalReservedTaskReadinessNotReady(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// Before reservation the task is ready.
	r, err := c.Readiness(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Ready {
		t.Fatalf("pre-reservation readiness = %+v, want ready", r)
	}

	mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")

	r, err = c.Readiness(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Ready {
		t.Fatalf("reserved task readiness = %+v, want not ready", r)
	}
	if len(r.BlockingReasons) != 1 || r.BlockingReasons[0] != ReadinessReservedForTransfer {
		t.Fatalf("reserved task blocking reasons = %+v, want reserved-for-transfer", r.BlockingReasons)
	}

	// Survives reopen/reread.
	h2, err := home.Open(root)
	if err != nil {
		t.Fatalf("home.Open: %v", err)
	}
	c2, err := NewCanonical(h2)
	if err != nil {
		t.Fatal(err)
	}
	r, err = c2.Readiness(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Ready || len(r.BlockingReasons) != 1 || r.BlockingReasons[0] != ReadinessReservedForTransfer {
		t.Fatalf("reserved task readiness after reopen = %+v", r)
	}
}

// TestCanonicalSupersededSourceReadinessNotCurrent proves a superseded source
// (after CommitTransfer) is not ready and not current across reopen, and that
// Get no longer exposes it as current truth while historical evidence remains.
func TestCanonicalSupersededSourceReadinessNotCurrent(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")

	commit := commitTransferRequest(t, c, "t1", preconditionOf(1, 2), "res-t1", "dest-home")
	if _, err := c.CommitTransfer(mustOperation(t, "op-commit-rnc", commit), commit); err != nil {
		t.Fatalf("CommitTransfer: %v", err)
	}

	// Readiness is not-ready / not-current.
	r, err := c.Readiness(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Ready {
		t.Fatalf("superseded readiness = %+v, want not ready", r)
	}
	if len(r.BlockingReasons) != 1 || r.BlockingReasons[0] != ReadinessNotCurrent {
		t.Fatalf("superseded blocking reasons = %+v, want not-current", r.BlockingReasons)
	}

	// Get fails closed; historical evidence still readable by generation.
	if _, err := c.Get(mustTaskID(t, "t1")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after supersession = %v, want ErrNotFound", err)
	}
	if _, err := c.GetGeneration(mustTaskID(t, "t1"), Generation(1)); err != nil {
		t.Fatalf("GetGeneration after supersession = %v", err)
	}

	// Survives reopen.
	h2, err := home.Open(root)
	if err != nil {
		t.Fatalf("home.Open: %v", err)
	}
	c2, err := NewCanonical(h2)
	if err != nil {
		t.Fatal(err)
	}
	r, err = c2.Readiness(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Ready || len(r.BlockingReasons) != 1 || r.BlockingReasons[0] != ReadinessNotCurrent {
		t.Fatalf("superseded readiness after reopen = %+v", r)
	}
	if _, err := c2.Get(mustTaskID(t, "t1")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after reopen = %v, want ErrNotFound", err)
	}
	if _, err := c2.GetGeneration(mustTaskID(t, "t1"), Generation(1)); err != nil {
		t.Fatalf("GetGeneration after reopen = %v", err)
	}
}

// TestCanonicalSupersededSourceRejectsEveryMutationFamily commits a transfer,
// rereads the exact latest revision (via the historical generation read), and
// proves every unrelated mutation family rejects on the superseded source.
func TestCanonicalSupersededSourceRejectsEveryMutationFamily(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")

	commit := commitTransferRequest(t, c, "t1", preconditionOf(1, 2), "res-t1", "dest-home")
	if _, err := c.CommitTransfer(mustOperation(t, "op-commit-mut", commit), commit); err != nil {
		t.Fatalf("CommitTransfer: %v", err)
	}

	// The latest persisted revision of the superseded generation.
	hist, err := c.GetGeneration(mustTaskID(t, "t1"), Generation(1))
	if err != nil {
		t.Fatal(err)
	}
	prec := preconditionOf(uint64(hist.Generation), uint64(hist.Revision))
	taskID := mustTaskID(t, "t1")

	start := CanonicalStartRequest{HomeID: c.HomeID(), TaskID: taskID, Precondition: prec, Reason: "start"}
	if _, err := c.Start(mustOperation(t, "op-sup-start", start), start); !errors.Is(err, ErrConflict) {
		t.Fatalf("Start on superseded = %v, want ErrConflict", err)
	}
	block := CanonicalBlockRequest{HomeID: c.HomeID(), TaskID: taskID, Precondition: prec, Detail: "d", Reason: "block"}
	if _, err := c.Block(mustOperation(t, "op-sup-block", block), block); !errors.Is(err, ErrConflict) {
		t.Fatalf("Block on superseded = %v, want ErrConflict", err)
	}
	unblock := CanonicalUnblockRequest{HomeID: c.HomeID(), TaskID: taskID, Precondition: prec, Reason: "unblock"}
	if _, err := c.Unblock(mustOperation(t, "op-sup-unblock", unblock), unblock); !errors.Is(err, ErrConflict) {
		t.Fatalf("Unblock on superseded = %v, want ErrConflict", err)
	}
	complete := CanonicalCompleteRequest{HomeID: c.HomeID(), TaskID: taskID, Precondition: prec, To: PhaseDone, Reason: "done"}
	if _, err := c.Complete(mustOperation(t, "op-sup-complete", complete), complete); !errors.Is(err, ErrConflict) {
		t.Fatalf("Complete on superseded = %v, want ErrConflict", err)
	}
	reopen := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: taskID, Precondition: prec, Reason: "reopen"}
	if _, err := c.Reopen(mustOperation(t, "op-sup-reopen", reopen), reopen); !errors.Is(err, ErrConflict) {
		t.Fatalf("Reopen on superseded = %v, want ErrConflict", err)
	}
	bw := bindWorktreeRequest(c, "t1", prec)
	if _, err := c.BindWorktree(mustOperation(t, "op-sup-bindwt", bw), bw); !errors.Is(err, ErrConflict) {
		t.Fatalf("BindWorktree on superseded = %v, want ErrConflict", err)
	}
	be := bindEndpointRequest(c, "t1", prec)
	if _, err := c.BindEndpoint(mustOperation(t, "op-sup-bindep", be), be); !errors.Is(err, ErrConflict) {
		t.Fatalf("BindEndpoint on superseded = %v, want ErrConflict", err)
	}
	retire := retireRequest(t, c, "t1", prec)
	if _, err := c.Retire(mustOperation(t, "op-sup-retire", retire), retire); !errors.Is(err, ErrConflict) {
		t.Fatalf("Retire on superseded = %v, want ErrConflict", err)
	}

	// The same rejection holds after reopen.
	h2, err := home.Open(root)
	if err != nil {
		t.Fatalf("home.Open: %v", err)
	}
	c2, err := NewCanonical(h2)
	if err != nil {
		t.Fatal(err)
	}
	start2 := CanonicalStartRequest{HomeID: c2.HomeID(), TaskID: taskID, Precondition: prec, Reason: "start"}
	if _, err := c2.Start(mustOperation(t, "op-sup-start2", start2), start2); !errors.Is(err, ErrConflict) {
		t.Fatalf("Start on superseded after reopen = %v, want ErrConflict", err)
	}
}

// TestCanonicalHoldsCannotMakeReservedTaskReady proves a Dispatch Hold cannot
// make a reserved or superseded task ready: reservation/supersession readiness
// blockers are Task Authority owned and independent controls cannot override.
func TestCanonicalHoldsCannotMakeReservedOrSupersededTaskReady(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// Release every hold (there are none) and add no holds; the reservation
	// alone blocks readiness.
	mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")
	r, err := c.Readiness(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Ready || len(r.BlockingReasons) != 1 || r.BlockingReasons[0] != ReadinessReservedForTransfer {
		t.Fatalf("reserved readiness = %+v", r)
	}

	// Adding a hold for start keeps it blocked by reservation (hold admin
	// still works as an independent control).
	hold := CanonicalAddHoldRequest{HomeID: c.HomeID(), HoldID: "hold-x", Actions: []DispatchAction{DispatchActionStart}, Reason: "independent"}
	if _, err := c.AddHold(mustOperation(t, "op-hold-x", hold), hold); err != nil {
		t.Fatalf("AddHold: %v", err)
	}
	r, err = c.Readiness(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Ready {
		t.Fatalf("reserved readiness with hold = %+v, want not ready", r)
	}
	foundReservation := false
	for _, reason := range r.BlockingReasons {
		if reason == ReadinessReservedForTransfer {
			foundReservation = true
		}
	}
	if !foundReservation {
		t.Fatalf("reservation reason missing with hold present: %+v", r.BlockingReasons)
	}

	// Releasing the hold does not make the reserved task ready.
	release := CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "hold-x", Reason: "release"}
	if _, err := c.ReleaseHold(mustOperation(t, "op-release-x", release), release); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	r, err = c.Readiness(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Ready || len(r.BlockingReasons) != 1 || r.BlockingReasons[0] != ReadinessReservedForTransfer {
		t.Fatalf("reserved readiness after hold release = %+v", r)
	}

	// After supersession, holds cannot make it ready either.
	commit := commitTransferRequest(t, c, "t1", preconditionOf(1, 2), "res-t1", "dest-home")
	if _, err := c.CommitTransfer(mustOperation(t, "op-commit-hold", commit), commit); err != nil {
		t.Fatalf("CommitTransfer: %v", err)
	}
	r, err = c.Readiness(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Ready || len(r.BlockingReasons) != 1 || r.BlockingReasons[0] != ReadinessNotCurrent {
		t.Fatalf("superseded readiness with holds present = %+v", r)
	}
}

// TestCanonicalListExcludesSupersededSource proves List is a current-truth
// query: after CommitTransfer the superseded source is excluded.
func TestCanonicalListExcludesSupersededSource(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustCreate(t, c, "t2")
	mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")

	commit := commitTransferRequest(t, c, "t1", preconditionOf(1, 2), "res-t1", "dest-home")
	if _, err := c.CommitTransfer(mustOperation(t, "op-commit-list", commit), commit); err != nil {
		t.Fatalf("CommitTransfer: %v", err)
	}

	list, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].TaskID != "t2" {
		t.Fatalf("List after supersession = %+v, want only t2", list)
	}
}

// launchPreState drives a launch flow through BindWorktree, AttachEndpoint,
// and RecordLaunch (fenced to the committed intent) and returns the intent
// request and the current revision. It is the pre-state from which the
// binding-fence assertions start.
func launchPreState(t *testing.T, c *Canonical, taskID string) (CanonicalBeginSpawnRequest, uint64) {
	t.Helper()
	mustCreate(t, c, taskID)
	req, rev := mustBeginSpawn(t, c, taskID, preconditionOf(1, 1))

	bw := CanonicalBindWorktreeRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: preconditionOf(1, rev),
		Binding:      launchWorktreeBinding(req),
		Reason:       "bind worktree",
	}
	if _, err := c.BindWorktree(mustOperation(t, "op-wt-launch-"+taskID, bw), bw); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
	rev++

	attach := attachRequest(c, taskID, preconditionOf(1, rev), req, "handle-1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-launch-"+taskID, attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}
	rev++

	record := recordLaunchRequest(c, taskID, preconditionOf(1, rev), req)
	if _, err := c.RecordLaunch(mustOperation(t, "op-record-launch-"+taskID, record), record); err != nil {
		t.Fatalf("RecordLaunch: %v", err)
	}
	rev++
	return req, rev
}

// TestCanonicalLaunchFenceRejectsMismatchedWorktreeBinding proves BindWorktree
// fails closed when the generation carries a launch intent but the binding does
// not carry the exact worktree reservation fence the intent reserved before
// acquisition.
func TestCanonicalLaunchFenceRejectsMismatchedWorktreeBinding(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	_, _ = mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))

	bw := CanonicalBindWorktreeRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 2),
		Binding:      worktreeBinding(), // default lease/fence, not the reserved ones
		Reason:       "bind worktree",
	}
	if _, err := c.BindWorktree(mustOperation(t, "op-wt-fence-mismatch", bw), bw); !errors.Is(err, ErrConflict) {
		t.Fatalf("worktree binding outside the launch fence = %v, want ErrConflict", err)
	}
	if _, err := c.Get(mustTaskID(t, "t1")); err != nil {
		t.Fatalf("intent must survive the refusal: %v", err)
	}
}

// TestCanonicalBindEndpointWithLaunchRequiresAcquiredAndEvidence proves the
// final queued -> working transition requires the recorded acquired endpoint
// and the recorded launch evidence when the generation carries a launch
// intent: each missing record fails closed with a typed conflict.
func TestCanonicalBindEndpointWithLaunchRequiresAcquiredAndEvidence(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	req, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))

	bw := CanonicalBindWorktreeRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, rev),
		Binding:      launchWorktreeBinding(req),
		Reason:       "bind worktree",
	}
	if _, err := c.BindWorktree(mustOperation(t, "op-wt-1", bw), bw); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
	rev++

	// No acquired endpoint yet: BindEndpoint fails closed.
	be := CanonicalBindEndpointRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, rev),
		Binding:      launchEndpointBinding(req, "handle-1"),
		Reason:       "spawn",
	}
	if _, err := c.BindEndpoint(mustOperation(t, "op-be-no-acquired", be), be); !errors.Is(err, ErrConflict) {
		t.Fatalf("bind endpoint without acquired endpoint = %v, want ErrConflict", err)
	}

	// Acquired endpoint recorded, no launch evidence yet: still fails closed.
	attach := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-1", attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}
	rev++
	be.Precondition = preconditionOf(1, rev)
	if _, err := c.BindEndpoint(mustOperation(t, "op-be-no-evidence", be), be); !errors.Is(err, ErrConflict) {
		t.Fatalf("bind endpoint without launch evidence = %v, want ErrConflict", err)
	}
}

// TestCanonicalLaunchFenceRejectsMismatchedEndpointBinding proves BindEndpoint
// fails closed when the endpoint binding does not carry the exact endpoint
// reservation fence the launch intent reserved, even though the acquired
// endpoint and launch evidence are recorded.
func TestCanonicalLaunchFenceRejectsMismatchedEndpointBinding(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	_, rev := launchPreState(t, c, "t1")

	be := CanonicalBindEndpointRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, rev),
		Binding:      endpointBinding(), // default lease/fence, not the reserved ones
		Reason:       "spawn",
	}
	if _, err := c.BindEndpoint(mustOperation(t, "op-be-fence-mismatch", be), be); !errors.Is(err, ErrConflict) {
		t.Fatalf("endpoint binding outside the launch fence = %v, want ErrConflict", err)
	}
}

// TestCanonicalLaunchFenceRejectsSubstitutedEndpoint proves BindEndpoint fails
// closed when the endpoint binding carries the reserved fence but a different
// endpoint identity than the recorded acquired endpoint: a different endpoint
// can never be substituted under the same fence.
func TestCanonicalLaunchFenceRejectsSubstitutedEndpoint(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	req, rev := launchPreState(t, c, "t1")

	be := CanonicalBindEndpointRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, rev),
		Binding:      launchEndpointBinding(req, "handle-2"), // reserved fence, different handle
		Reason:       "spawn",
	}
	if _, err := c.BindEndpoint(mustOperation(t, "op-be-substituted", be), be); !errors.Is(err, ErrConflict) {
		t.Fatalf("substituted endpoint binding = %v, want ErrConflict", err)
	}
}

// TestCanonicalLaunchFinalBindingCarriesIntentOwnedFences proves the final
// working bindings carry exactly the worktree and endpoint reservation/fence
// identities the launch intent reserved before acquisition, and that the
// launch records survive beside the active bindings.
func TestCanonicalLaunchFinalBindingCarriesIntentOwnedFences(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	req, rev := launchPreState(t, c, "t1")

	be := CanonicalBindEndpointRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, rev),
		Binding:      launchEndpointBinding(req, "handle-1"),
		Reason:       "spawn",
	}
	out, err := c.BindEndpoint(mustOperation(t, "op-be-final", be), be)
	if err != nil {
		t.Fatalf("BindEndpoint: %v", err)
	}
	if out.Phase != PhaseWorking {
		t.Fatalf("final bind outcome = %+v, want working", out)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Worktree == nil || agg.Endpoint == nil {
		t.Fatalf("final bindings missing: worktree %+v endpoint %+v", agg.Worktree, agg.Endpoint)
	}
	if agg.Worktree.LeaseID != req.WorktreeReservationID || agg.Worktree.FenceToken != req.WorktreeFenceToken {
		t.Fatalf("final worktree binding = %+v, want intent-owned reservation %q/%q", agg.Worktree, req.WorktreeReservationID, req.WorktreeFenceToken)
	}
	if agg.Endpoint.LeaseID != req.EndpointReservationID || agg.Endpoint.FenceToken != req.EndpointFenceToken {
		t.Fatalf("final endpoint binding = %+v, want intent-owned reservation %q/%q", agg.Endpoint, req.EndpointReservationID, req.EndpointFenceToken)
	}
	if agg.AcquiredEndpoint == nil || agg.LaunchEvidence == nil {
		t.Fatalf("launch records lost: acquired %+v evidence %+v", agg.AcquiredEndpoint, agg.LaunchEvidence)
	}
}

// TestCanonicalLaunchOpsFencedByTransferInvariants proves the launch
// operations are ordinary task-scoped mutations: a task reserved for transfer
// (or a superseded generation) rejects BeginSpawn, AttachEndpoint, and
// RecordLaunch with the common reservation fence before any launch-specific
// check, so a launch can never begin on a fenced generation.
func TestCanonicalLaunchOpsFencedByTransferInvariants(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	prec := preconditionOf(1, uint64(agg.Revision))

	begin := launchRequest(c, "t1", prec)
	if _, err := c.BeginSpawn(mustOperation(t, "op-fence-begin", begin), begin); !errors.Is(err, ErrConflict) {
		t.Fatalf("BeginSpawn on reserved task = %v, want ErrConflict", err)
	}
	attach := attachRequest(c, "t1", prec, begin, "handle-1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-fence-attach", attach), attach); !errors.Is(err, ErrConflict) {
		t.Fatalf("AttachEndpoint on reserved task = %v, want ErrConflict", err)
	}
	record := recordLaunchRequest(c, "t1", prec, begin)
	if _, err := c.RecordLaunch(mustOperation(t, "op-fence-record", record), record); !errors.Is(err, ErrConflict) {
		t.Fatalf("RecordLaunch on reserved task = %v, want ErrConflict", err)
	}
}

// TestCanonicalDeliveryOpsFencedByTransferReservation proves the delivery
// authorization, revocation, and outcome operations are ordinary task-scoped
// mutations: a task reserved for transfer rejects every delivery mutation
// with the common reservation fence before any delivery-specific check.
func TestCanonicalDeliveryOpsFencedByTransferReservation(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")
	mustReserveTransfer(t, c, "t1", preconditionOf(1, 3), "dest-home")

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	prec := preconditionOf(uint64(agg.Generation), uint64(agg.Revision))

	ar := authorizeRequest(c, "t1", prec)
	if _, err := c.AuthorizeDelivery(mustOperation(t, "op-fence-auth", ar), ar); !errors.Is(err, ErrConflict) {
		t.Fatalf("AuthorizeDelivery on reserved task = %v, want ErrConflict", err)
	}
	rr := CanonicalRevokeDeliveryRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: prec, AuthorizationOperationID: "op-any", Reason: "x"}
	if _, err := c.RevokeDeliveryAuthorization(mustOperation(t, "op-fence-revoke", rr), rr); !errors.Is(err, ErrConflict) {
		t.Fatalf("RevokeDeliveryAuthorization on reserved task = %v, want ErrConflict", err)
	}
	or := CanonicalDeliveryOutcomeRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: prec, AuthorizationOperationID: "op-any", Status: DeliveryOutcomeCompleted, Detail: "x"}
	if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-fence-outcome", or), or); !errors.Is(err, ErrConflict) {
		t.Fatalf("CommitDeliveryOutcome on reserved task = %v, want ErrConflict", err)
	}
}

// TestCanonicalDeliveryOpsFencedBySupersession proves delivery mutations
// fail closed on a superseded source generation (after CommitTransfer) with
// the common currentness gate, even against the latest persisted revision.
func TestCanonicalDeliveryOpsFencedBySupersession(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")
	mustReserveTransfer(t, c, "t1", preconditionOf(1, 3), "dest-home")

	commit := commitTransferRequest(t, c, "t1", preconditionOf(1, 4), "res-t1", "dest-home")
	if _, err := c.CommitTransfer(mustOperation(t, "op-commit-sup-delivery", commit), commit); err != nil {
		t.Fatalf("CommitTransfer: %v", err)
	}

	hist, err := c.GetGeneration(mustTaskID(t, "t1"), Generation(1))
	if err != nil {
		t.Fatal(err)
	}
	prec := preconditionOf(uint64(hist.Generation), uint64(hist.Revision))

	ar := authorizeRequest(c, "t1", prec)
	if _, err := c.AuthorizeDelivery(mustOperation(t, "op-sup-auth", ar), ar); !errors.Is(err, ErrConflict) {
		t.Fatalf("AuthorizeDelivery on superseded source = %v, want ErrConflict", err)
	}
	or := CanonicalDeliveryOutcomeRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: prec, AuthorizationOperationID: "op-any", Status: DeliveryOutcomeCompleted, Detail: "x"}
	if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-sup-outcome", or), or); !errors.Is(err, ErrConflict) {
		t.Fatalf("CommitDeliveryOutcome on superseded source = %v, want ErrConflict", err)
	}
	rr := CanonicalRevokeDeliveryRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: prec, AuthorizationOperationID: "op-any", Reason: "x"}
	if _, err := c.RevokeDeliveryAuthorization(mustOperation(t, "op-sup-revoke", rr), rr); !errors.Is(err, ErrConflict) {
		t.Fatalf("RevokeDeliveryAuthorization on superseded source = %v, want ErrConflict", err)
	}

	// The same rejection holds after reopen.
	c2 := reopenCanonical(t, rootOf(t, c))
	ar2 := authorizeRequest(c2, "t1", prec)
	if _, err := c2.AuthorizeDelivery(mustOperation(t, "op-sup-auth2", ar2), ar2); !errors.Is(err, ErrConflict) {
		t.Fatalf("AuthorizeDelivery on superseded source after reopen = %v, want ErrConflict", err)
	}
}

// rootOf returns the home root of the canonical's home (test helper for the
// supersession reopen assertion).
func rootOf(t *testing.T, c *Canonical) string {
	t.Helper()
	return c.h.Root()
}
