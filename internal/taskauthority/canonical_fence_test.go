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
