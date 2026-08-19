package taskauthority

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// digestOf returns a deterministic 64-hex sha256 digest for a test string.
func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// reserveRequest builds a ReserveTransfer request for a source home.
func reserveTransferRequest(t *testing.T, c *Canonical, taskID string, prec domain.Precondition, reservationID, dest string) CanonicalReserveTransferRequest {
	return CanonicalReserveTransferRequest{
		HomeID:        c.HomeID(),
		TaskID:        mustTaskID(t, taskID),
		Precondition:  prec,
		ReservationID: reservationID,
		Destination:   mustHomeID(t, dest),
		FenceToken:    "fence-" + reservationID,
		Reason:        "reserve",
	}
}

func mustHomeID(t *testing.T, value string) domain.HomeID {
	t.Helper()
	id, err := domain.NewHomeID(value)
	if err != nil {
		t.Fatalf("NewHomeID(%s): %v", value, err)
	}
	return id
}

func mustReserveTransfer(t *testing.T, c *Canonical, taskID string, prec domain.Precondition, dest string) string {
	t.Helper()
	reservationID := "res-" + taskID
	req := reserveTransferRequest(t, c, taskID, prec, reservationID, dest)
	if _, err := c.ReserveTransfer(mustOperation(t, "op-reserve-"+taskID, req), req); err != nil {
		t.Fatalf("ReserveTransfer(%s): %v", taskID, err)
	}
	return reservationID
}

func receiveTransferRequest(t *testing.T, c *Canonical, taskID, reservationID, sourceHome string, sourceGen uint64) CanonicalReceiveTransferRequest {
	return CanonicalReceiveTransferRequest{
		HomeID:           c.HomeID(),
		TaskID:           mustTaskID(t, taskID),
		ReservationID:    reservationID,
		SourceHome:       mustHomeID(t, sourceHome),
		SourceGeneration: Generation(sourceGen),
		Definition:       TaskDefinition{Owner: "owner", Description: "work", Kind: "ship"},
		Reason:           "receive",
	}
}

// fenceTokenFor returns the fence token used for a reservation of the given ID.
func fenceTokenFor(reservationID string) string { return "fence-" + reservationID }

// activationEvidence builds the destination-activation evidence bound to a
// source reservation, for a CommitTransfer. destGen is the destination
// generation the activate operation produced.
func activationEvidence(c *Canonical, taskID, reservationID, destHome string, sourceGen, destGen uint64) TransferActivationInfo {
	return TransferActivationInfo{
		ReservationID:         reservationID,
		TaskID:                taskID,
		SourceHome:            c.HomeID().Value(),
		SourceGeneration:      Generation(sourceGen),
		DestinationHome:       destHome,
		DestinationGeneration: Generation(destGen),
		ActivationOperationID: "op-activate-" + taskID,
		ActivationDigest:      digestOf("activate:" + taskID + ":" + reservationID),
	}
}

func commitTransferRequest(t *testing.T, c *Canonical, taskID string, prec domain.Precondition, reservationID, destHome string) CanonicalCommitTransferRequest {
	return CanonicalCommitTransferRequest{
		HomeID:        c.HomeID(),
		TaskID:        mustTaskID(t, taskID),
		Precondition:  prec,
		ReservationID: reservationID,
		FenceToken:    fenceTokenFor(reservationID),
		Evidence:      activationEvidence(c, taskID, reservationID, destHome, 1, 1),
		Reason:        "commit",
	}
}

func TestCanonicalReserveTransferFencesSource(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	reservationID := mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Transfer == nil || agg.Transfer.ReservationID != reservationID {
		t.Fatalf("reservation not recorded: %+v", agg.Transfer)
	}
	if agg.Transfer.DestinationHome != "dest-home" || agg.Transfer.FenceToken == "" {
		t.Fatalf("reservation state = %+v", agg.Transfer)
	}
	if agg.Revision != 2 {
		t.Fatalf("revision = %d, want 2", agg.Revision)
	}
}

func TestCanonicalReserveTransferStalePrecondition(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := reserveTransferRequest(t, c, "t1", preconditionOf(1, 9), "res-1", "dest-home")
	if _, err := c.ReserveTransfer(mustOperation(t, "op-reserve-stale", req), req); !errors.Is(err, domain.ErrStalePrecondition) {
		t.Fatalf("stale reserve = %v, want domain.ErrStalePrecondition", err)
	}
}

func TestCanonicalReserveTransferSameHomeFails(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := CanonicalReserveTransferRequest{
		HomeID:        c.HomeID(),
		TaskID:        mustTaskID(t, "t1"),
		Precondition:  preconditionOf(1, 1),
		ReservationID: "res-1",
		Destination:   c.HomeID(),
		FenceToken:    "fence",
		Reason:        "reserve",
	}
	if _, err := c.ReserveTransfer(mustOperation(t, "op-reserve-same", req), req); err == nil {
		t.Fatalf("same-home reserve = nil error, want failure")
	}
}

func TestCanonicalReserveTransferAlreadyReservedConflicts(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")

	// A second reserve on the same generation (now revision 2) conflicts.
	req := reserveTransferRequest(t, c, "t1", preconditionOf(1, 2), "res-2", "other-home")
	if _, err := c.ReserveTransfer(mustOperation(t, "op-reserve-again", req), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("re-reserve = %v, want ErrConflict", err)
	}
}

func TestCanonicalReserveTransferIdempotentReplay(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := reserveTransferRequest(t, c, "t1", preconditionOf(1, 1), "res-1", "dest-home")
	op := mustOperation(t, "op-reserve-replay", req)
	first, err := c.ReserveTransfer(op, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.ReserveTransfer(op, req)
	if err != nil {
		t.Fatalf("replay reserve: %v", err)
	}
	if !second.Replayed || first.Replayed {
		t.Fatalf("replay flags first=%v second=%v, want false/true", first.Replayed, second.Replayed)
	}
}

func TestCanonicalReceiveTransfer(t *testing.T) {
	// Destination home is a fresh canonical.
	cDest, _, _ := newTestCanonical(t)

	req := receiveTransferRequest(t, cDest, "t1", "res-t1", "source-home", 3)
	out, err := cDest.ReceiveTransfer(mustOperation(t, "op-receive-1", req), req)
	if err != nil {
		t.Fatalf("ReceiveTransfer: %v", err)
	}
	if out.Generation != 1 || out.Revision != FirstRevision || out.Phase != PhaseQueued {
		t.Fatalf("receive outcome = %+v", out)
	}

	// The received generation is not yet current.
	agg, err := cDest.Get(mustTaskID(t, "t1"))
	if err == nil {
		t.Fatalf("Get on received-not-activated task = %+v, want not found", agg)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get on received-not-activated = %v, want ErrNotFound", err)
	}
}

func TestCanonicalReceiveTransferDestinationAlreadyOwnsFails(t *testing.T) {
	cDest, _, _ := newTestCanonical(t)
	mustCreate(t, cDest, "t1")

	req := receiveTransferRequest(t, cDest, "t1", "res-t1", "source-home", 3)
	if _, err := cDest.ReceiveTransfer(mustOperation(t, "op-receive-dup", req), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("receive into owned destination = %v, want ErrConflict", err)
	}
}

func TestCanonicalReceiveTransferPendingReceiveNotOverwritten(t *testing.T) {
	cDest, _, _ := newTestCanonical(t)

	first := receiveTransferRequest(t, cDest, "t1", "res-t1", "source-home", 3)
	if _, err := cDest.ReceiveTransfer(mustOperation(t, "op-receive-1", first), first); err != nil {
		t.Fatalf("ReceiveTransfer: %v", err)
	}

	// A second receive under a different reservation must fail closed: the
	// destination already has a pending received generation and it is never
	// overwritten.
	second := receiveTransferRequest(t, cDest, "t1", "res-other", "source-home", 5)
	if _, err := cDest.ReceiveTransfer(mustOperation(t, "op-receive-2", second), second); !errors.Is(err, ErrConflict) {
		t.Fatalf("second receive = %v, want ErrConflict", err)
	}
}

func TestCanonicalReceiveTransferSameSourceFails(t *testing.T) {
	cDest, _, _ := newTestCanonical(t)

	req := CanonicalReceiveTransferRequest{
		HomeID:           cDest.HomeID(),
		TaskID:           mustTaskID(t, "t1"),
		ReservationID:    "res-t1",
		SourceHome:       cDest.HomeID(),
		SourceGeneration: 3,
		Definition:       TaskDefinition{Owner: "owner", Description: "work", Kind: "ship"},
		Reason:           "receive",
	}
	if _, err := cDest.ReceiveTransfer(mustOperation(t, "op-receive-same", req), req); err == nil {
		t.Fatalf("same-source receive = nil error, want failure")
	}
}

// TestCanonicalReceiveTransferConflictsWithNonCurrentDestinationHistory proves
// a receive fails closed when the destination holds ANY non-current generation
// history for the task (here: an older generation preserved by a Reopen), not
// just when the task is current. The receive never overwrites or conflicts
// with existing destination generation state.
func TestCanonicalReceiveTransferConflictsWithNonCurrentDestinationHistory(t *testing.T) {
	cDest, _, _ := newTestCanonical(t)

	// Build normal destination lifecycle history with a preserved older
	// generation: create, complete, reopen (gen-1.json + current.json exist).
	mustCreate(t, cDest, "t1")
	complete := CanonicalCompleteRequest{HomeID: cDest.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 1), To: PhaseDone, Reason: "done"}
	if _, err := cDest.Complete(mustOperation(t, "op-c-hist", complete), complete); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	reopen := CanonicalReopenRequest{HomeID: cDest.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 2), Reason: "reopen"}
	if _, err := cDest.Reopen(mustOperation(t, "op-r-hist", reopen), reopen); err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	req := receiveTransferRequest(t, cDest, "t1", "res-t1", "source-home", 3)
	if _, err := cDest.ReceiveTransfer(mustOperation(t, "op-receive-history", req), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("receive into destination with non-current history = %v, want ErrConflict", err)
	}
}

// TestCanonicalReceiveTransferAfterActivationFailsClosed proves a receive
// fails closed once the destination has activated a received generation: the
// destination now owns the task and destination truth is never overwritten.
func TestCanonicalReceiveTransferAfterActivationFailsClosed(t *testing.T) {
	cDest, _, _ := newTestCanonical(t)

	req := receiveTransferRequest(t, cDest, "t1", "res-t1", "source-home", 3)
	if _, err := cDest.ReceiveTransfer(mustOperation(t, "op-receive-1", req), req); err != nil {
		t.Fatalf("ReceiveTransfer: %v", err)
	}
	activate := CanonicalActivateTransferRequest{
		HomeID:        cDest.HomeID(),
		TaskID:        mustTaskID(t, "t1"),
		Precondition:  preconditionOf(1, 1),
		ReservationID: "res-t1",
		Reason:        "activate",
	}
	if _, err := cDest.ActivateTransfer(mustOperation(t, "op-activate-1", activate), activate); err != nil {
		t.Fatalf("ActivateTransfer: %v", err)
	}

	again := receiveTransferRequest(t, cDest, "t1", "res-other", "source-home", 7)
	if _, err := cDest.ReceiveTransfer(mustOperation(t, "op-receive-after", again), again); !errors.Is(err, ErrConflict) {
		t.Fatalf("receive after activation = %v, want ErrConflict", err)
	}
}

func TestCanonicalActivateTransfer(t *testing.T) {
	cDest, _, _ := newTestCanonical(t)

	req := receiveTransferRequest(t, cDest, "t1", "res-t1", "source-home", 3)
	if _, err := cDest.ReceiveTransfer(mustOperation(t, "op-receive-1", req), req); err != nil {
		t.Fatalf("ReceiveTransfer: %v", err)
	}

	activate := CanonicalActivateTransferRequest{
		HomeID:        cDest.HomeID(),
		TaskID:        mustTaskID(t, "t1"),
		Precondition:  preconditionOf(1, 1),
		ReservationID: "res-t1",
		Reason:        "activate",
	}
	out, err := cDest.ActivateTransfer(mustOperation(t, "op-activate-1", activate), activate)
	if err != nil {
		t.Fatalf("ActivateTransfer: %v", err)
	}
	if out.Phase != PhaseQueued || out.Revision != 2 {
		t.Fatalf("activate outcome = %+v", out)
	}

	// Now the task is current/owned at the destination.
	agg, err := cDest.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatalf("Get after activate: %v", err)
	}
	if !agg.Current || agg.Generation != 1 || agg.Revision != 2 {
		t.Fatalf("activated aggregate = %+v", agg)
	}
	if agg.Transfer == nil || agg.Transfer.ReservationID != "res-t1" || agg.Transfer.SourceHome != "source-home" {
		t.Fatalf("activated transfer provenance = %+v", agg.Transfer)
	}
}

func TestCanonicalActivateTransferUniqueConflicts(t *testing.T) {
	cDest, _, _ := newTestCanonical(t)

	req := receiveTransferRequest(t, cDest, "t1", "res-t1", "source-home", 3)
	if _, err := cDest.ReceiveTransfer(mustOperation(t, "op-receive-1", req), req); err != nil {
		t.Fatalf("ReceiveTransfer: %v", err)
	}

	activate := CanonicalActivateTransferRequest{
		HomeID:        cDest.HomeID(),
		TaskID:        mustTaskID(t, "t1"),
		Precondition:  preconditionOf(1, 1),
		ReservationID: "res-t1",
		Reason:        "activate",
	}
	if _, err := cDest.ActivateTransfer(mustOperation(t, "op-activate-1", activate), activate); err != nil {
		t.Fatalf("ActivateTransfer: %v", err)
	}

	// A second activation attempt (fresh operation) conflicts: activation is unique.
	again := CanonicalActivateTransferRequest{
		HomeID:        cDest.HomeID(),
		TaskID:        mustTaskID(t, "t1"),
		Precondition:  preconditionOf(1, 2),
		ReservationID: "res-t1",
		Reason:        "activate-again",
	}
	if _, err := cDest.ActivateTransfer(mustOperation(t, "op-activate-2", again), again); !errors.Is(err, ErrConflict) {
		t.Fatalf("second activate = %v, want ErrConflict", err)
	}
}

func TestCanonicalActivateTransferWrongReservation(t *testing.T) {
	cDest, _, _ := newTestCanonical(t)

	req := receiveTransferRequest(t, cDest, "t1", "res-t1", "source-home", 3)
	if _, err := cDest.ReceiveTransfer(mustOperation(t, "op-receive-1", req), req); err != nil {
		t.Fatalf("ReceiveTransfer: %v", err)
	}

	activate := CanonicalActivateTransferRequest{
		HomeID:        cDest.HomeID(),
		TaskID:        mustTaskID(t, "t1"),
		Precondition:  preconditionOf(1, 1),
		ReservationID: "res-other",
		Reason:        "activate",
	}
	if _, err := cDest.ActivateTransfer(mustOperation(t, "op-activate-wrong", activate), activate); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong reservation activate = %v, want ErrConflict", err)
	}
}

func TestCanonicalActivateTransferStalePrecondition(t *testing.T) {
	cDest, _, _ := newTestCanonical(t)

	req := receiveTransferRequest(t, cDest, "t1", "res-t1", "source-home", 3)
	if _, err := cDest.ReceiveTransfer(mustOperation(t, "op-receive-1", req), req); err != nil {
		t.Fatalf("ReceiveTransfer: %v", err)
	}

	activate := CanonicalActivateTransferRequest{
		HomeID:        cDest.HomeID(),
		TaskID:        mustTaskID(t, "t1"),
		Precondition:  preconditionOf(1, 9),
		ReservationID: "res-t1",
		Reason:        "activate",
	}
	if _, err := cDest.ActivateTransfer(mustOperation(t, "op-activate-stale", activate), activate); !errors.Is(err, domain.ErrStalePrecondition) {
		t.Fatalf("stale activate = %v, want domain.ErrStalePrecondition", err)
	}
}

func TestCanonicalCommitTransferSupersedesSource(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")

	commit := commitTransferRequest(t, c, "t1", preconditionOf(1, 2), "res-t1", "dest-home")
	if _, err := c.CommitTransfer(mustOperation(t, "op-commit-1", commit), commit); err != nil {
		t.Fatalf("CommitTransfer: %v", err)
	}

	// The superseded source is no longer current truth: normal Get fails closed
	// with ErrNotFound.
	if _, err := c.Get(mustTaskID(t, "t1")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after transfer commit = %v, want ErrNotFound", err)
	}
	// The historical evidence remains available by generation for audit.
	agg, err := c.GetGeneration(mustTaskID(t, "t1"), Generation(1))
	if err != nil {
		t.Fatalf("GetGeneration: %v", err)
	}
	if agg.Current {
		t.Fatalf("historical source still flagged current: %+v", agg)
	}
	if agg.Transfer == nil || !agg.Transfer.Transferred {
		t.Fatalf("transfer not marked committed: %+v", agg.Transfer)
	}
	if agg.Transfer.Activation == nil || agg.Transfer.Activation.DestinationHome != "dest-home" {
		t.Fatalf("activation evidence not recorded: %+v", agg.Transfer)
	}
}

func TestCanonicalCommitTransferWithoutReservationFails(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	commit := CanonicalCommitTransferRequest{
		HomeID:        c.HomeID(),
		TaskID:        mustTaskID(t, "t1"),
		Precondition:  preconditionOf(1, 1),
		ReservationID: "res-unknown",
		FenceToken:    fenceTokenFor("res-unknown"),
		Evidence:      activationEvidence(c, "t1", "res-unknown", "dest-home", 1, 1),
		Reason:        "commit",
	}
	if _, err := c.CommitTransfer(mustOperation(t, "op-commit-norev", commit), commit); !errors.Is(err, ErrConflict) {
		t.Fatalf("commit without reservation = %v, want ErrConflict", err)
	}
}

func TestCanonicalTransferIdempotentReplayAcrossOps(t *testing.T) {
	cDest, _, _ := newTestCanonical(t)

	req := receiveTransferRequest(t, cDest, "t1", "res-t1", "source-home", 3)
	op := mustOperation(t, "op-receive-replay", req)
	first, err := cDest.ReceiveTransfer(op, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cDest.ReceiveTransfer(op, req)
	if err != nil {
		t.Fatalf("replay receive: %v", err)
	}
	if !second.Replayed || first.Replayed {
		t.Fatalf("replay flags first=%v second=%v, want false/true", first.Replayed, second.Replayed)
	}
	if second.Generation != first.Generation || second.Revision != first.Revision {
		t.Fatalf("replay outcome differs: %+v vs %+v", second, first)
	}
}

func TestCanonicalTransferSurvivesHomeReopen(t *testing.T) {
	// Destination: receive + activate, then reopen the home and re-read.
	cDest, _, root := newTestCanonical(t)

	req := receiveTransferRequest(t, cDest, "t1", "res-t1", "source-home", 3)
	if _, err := cDest.ReceiveTransfer(mustOperation(t, "op-receive-1", req), req); err != nil {
		t.Fatalf("ReceiveTransfer: %v", err)
	}
	activate := CanonicalActivateTransferRequest{
		HomeID:        cDest.HomeID(),
		TaskID:        mustTaskID(t, "t1"),
		Precondition:  preconditionOf(1, 1),
		ReservationID: "res-t1",
		Reason:        "activate",
	}
	if _, err := cDest.ActivateTransfer(mustOperation(t, "op-activate-1", activate), activate); err != nil {
		t.Fatalf("ActivateTransfer: %v", err)
	}

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
		t.Fatalf("Get after reopen: %v", err)
	}
	if !agg.Current || agg.Generation != 1 {
		t.Fatalf("reread aggregate = %+v", agg)
	}
	if agg.Transfer == nil || agg.Transfer.ReservationID != "res-t1" {
		t.Fatalf("transfer provenance lost across reopen: %+v", agg.Transfer)
	}
}

// A task activated at the destination still carries the inbound transfer
// record: it names the source it came from, and its DestinationHome is empty
// because this home IS the destination. That record is not a live outbound
// reservation, so the reservation fence lets ordinary mutations through — and
// ReserveTransfer's own guard is what refuses an onward transfer while the
// inbound record is still attached. The fence and this guard cover disjoint
// states, which is why removing either one would go unnoticed by the other's
// tests.
func TestCanonicalReserveTransferRefusesOnwardTransferOfAnInboundRecord(t *testing.T) {
	cDest, _, _ := newTestCanonical(t)
	req := receiveTransferRequest(t, cDest, "t1", "res-t1", "source-home", 3)
	if _, err := cDest.ReceiveTransfer(mustOperation(t, "op-receive-1", req), req); err != nil {
		t.Fatalf("ReceiveTransfer: %v", err)
	}
	activate := CanonicalActivateTransferRequest{
		HomeID: cDest.HomeID(), TaskID: mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 1), ReservationID: "res-t1", Reason: "activate",
	}
	if _, err := cDest.ActivateTransfer(mustOperation(t, "op-activate-1", activate), activate); err != nil {
		t.Fatalf("ActivateTransfer: %v", err)
	}

	onward := reserveTransferRequest(t, cDest, "t1", preconditionOf(1, 2), "res-2", "onward-home")
	_, err := cDest.ReserveTransfer(mustOperation(t, "op-reserve-onward", onward), onward)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("onward reserve = %v, want ErrConflict", err)
	}
	if !strings.Contains(err.Error(), "already reserved for transfer") {
		t.Fatalf("error = %v, want the already-reserved refusal", err)
	}
}

// Premise test for the CommitTransfer `cur.Transfer.Transferred` waiver in
// .github/uncovered-guards.baseline.
//
// It builds the state that WOULD enter that guard -- a second commit of an
// already-committed transfer -- and asserts the refusal carries the EARLIER
// guard's message instead. Transferred is written at exactly one site, and the
// same atomic change-set clears Current, so mutateTaskTransfer's supersession
// check always refuses first. If that ordering ever changes, this test goes red
// and the waiver has to be re-argued rather than quietly becoming wrong.
func TestPremiseCommitTransferRefusesOnSupersessionBeforeItCanSeeACommittedTransfer(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")

	commit := commitTransferRequest(t, c, "t1", preconditionOf(1, 2), "res-t1", "dest-home")
	out, err := c.CommitTransfer(mustOperation(t, "op-commit-1", commit), commit)
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}

	// The generation the first commit both marked Transferred and superseded.
	again := commitTransferRequest(t, c, "t1", preconditionOf(1, uint64(out.Revision)), "res-t1", "dest-home")
	_, err = c.CommitTransfer(mustOperation(t, "op-commit-2", again), again)
	if err == nil {
		t.Fatal("a second commit of a committed transfer was accepted")
	}
	if !strings.Contains(err.Error(), "is not current; it is superseded and cannot be mutated") {
		t.Fatalf("error = %v, want the supersession refusal that shadows the already-committed guard", err)
	}
}
