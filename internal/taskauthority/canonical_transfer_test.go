package taskauthority

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// TestCanonicalReceiveTransferCarriesDeliveryContract proves a transferred
// task keeps the immutable resolved delivery contract at its destination.
// GetGeneration reads the received-but-not-current generation directly.
func TestCanonicalReceiveTransferCarriesDeliveryContract(t *testing.T) {
	cDest, _, _ := newTestCanonical(t)

	contract := &DeliveryContract{
		OperationID: "op-contract-src",
		Mode:        "direct-PR",
		Review:      DeliveryStep{Baseline: true, ProbeState: "baseline"},
		Forge:       DeliveryStep{Adapter: "gitlab", Path: "/bin/glab", ProbeState: "ready"},
		RecordedAt:  100,
	}
	req := receiveTransferRequest(t, cDest, "t1", "res-t1", "source-home", 3)
	req.DeliveryContract = contract
	if _, err := cDest.ReceiveTransfer(mustOperation(t, "op-receive-1", req), req); err != nil {
		t.Fatalf("ReceiveTransfer: %v", err)
	}

	agg, err := cDest.GetGeneration(mustTaskID(t, "t1"), Generation(1))
	if err != nil {
		t.Fatalf("GetGeneration: %v", err)
	}
	got := agg.DeliveryContract
	if got == nil {
		t.Fatal("received generation dropped the delivery contract")
	}
	if got.Mode != contract.Mode || got.OperationID != contract.OperationID || got.RecordedAt != contract.RecordedAt {
		t.Fatalf("contract = %+v, want %+v", got, contract)
	}
	if !deliveryStepsEqual(got.Review, contract.Review) || !deliveryStepsEqual(got.Forge, contract.Forge) {
		t.Fatalf("received steps = %+v/%+v, want %+v/%+v", got.Review, got.Forge, contract.Review, contract.Forge)
	}
}

// TestCanonicalReceiveTransferWithoutContractIsNil pins the negative: a task
// transferred with no recorded contract receives a nil contract, not a
// fabricated one.
func TestCanonicalReceiveTransferWithoutContractIsNil(t *testing.T) {
	cDest, _, _ := newTestCanonical(t)

	req := receiveTransferRequest(t, cDest, "t1", "res-t1", "source-home", 3)
	if _, err := cDest.ReceiveTransfer(mustOperation(t, "op-receive-1", req), req); err != nil {
		t.Fatalf("ReceiveTransfer: %v", err)
	}

	agg, err := cDest.GetGeneration(mustTaskID(t, "t1"), Generation(1))
	if err != nil {
		t.Fatalf("GetGeneration: %v", err)
	}
	if agg.DeliveryContract != nil {
		t.Fatalf("received contract = %+v, want nil", agg.DeliveryContract)
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

// The transfer rules: what a destination must not hold before a receive, what
// an unfinished transfer record refuses, and the reservation fence that stops
// every unrelated mutation of a reserved task.
func TestTransferRefusals(t *testing.T) {
	receive := func(t *testing.T, c *Canonical, reservationID string) error {
		req := receiveTransferRequest(t, c, "t1", reservationID, "source-home", 3)
		_, err := c.ReceiveTransfer(mustOperation(t, "op-receive-"+reservationID, req), req)
		return err
	}
	activate := func(t *testing.T, c *Canonical) {
		req := CanonicalActivateTransferRequest{
			HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
			Precondition: preconditionOf(1, 1), ReservationID: "res-t1", Reason: "activate",
		}
		_, err := c.ActivateTransfer(mustOperation(t, "op-activate-1", req), req)
		mustStep(t, err)
	}
	commit := func(t *testing.T, c *Canonical, mutate func(*CanonicalCommitTransferRequest)) error {
		mustCreate(t, c, "t1")
		mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")
		req := commitTransferRequest(t, c, "t1", preconditionOf(1, 2), "res-t1", "dest-home")
		mutate(&req)
		_, err := c.CommitTransfer(mustOperation(t, "op-commit-1", req), req)
		return err
	}

	runRefusalRows(t, []refusalRow{
		{name: "receive refuses a destination that holds earlier history of the task", class: ErrConflict, want: "destination already has task t1 history", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			mustStep(t, stepComplete(t, c, "t1", 1, PhaseDone))
			mustStep(t, stepReopen(t, c, "t1", 2))
			return receive(t, c, "res-t1")
		}},
		{name: "receive allows a destination with no history of the task", run: func(t *testing.T, c *Canonical) error {
			return receive(t, c, "res-t1")
		}},

		{name: "reserve refuses an inbound record that is still attached", class: ErrConflict, want: "already reserved for transfer", run: func(t *testing.T, c *Canonical) error {
			mustStep(t, receive(t, c, "res-t1"))
			activate(t, c)
			req := reserveTransferRequest(t, c, "t1", preconditionOf(1, 2), "res-2", "onward-home")
			_, err := c.ReserveTransfer(mustOperation(t, "op-reserve-onward", req), req)
			return err
		}},
		{name: "reserve allows a native task", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			req := reserveTransferRequest(t, c, "t1", preconditionOf(1, 1), "res-1", "dest-home")
			_, err := c.ReserveTransfer(mustOperation(t, "op-reserve-1", req), req)
			return err
		}},

		{name: "a reserved task refuses an unrelated mutation", class: ErrConflict, want: "is reserved for transfer; unrelated mutations fail closed", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")
			return stepStart(t, c, "t1", 2)
		}},
		{name: "a reserved task refuses a continuation with a stale fence token", class: ErrConflict, want: "reservation fence mismatch; stale transfer cannot mutate", run: func(t *testing.T, c *Canonical) error {
			return commit(t, c, func(r *CanonicalCommitTransferRequest) { r.FenceToken = "stale-fence" })
		}},
		{name: "a reserved task allows the continuation of its exact reservation", run: func(t *testing.T, c *Canonical) error {
			return commit(t, c, func(*CanonicalCommitTransferRequest) {})
		}},
	})
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

// interruptedCommit turns the change-set the operation run commits into a
// pending journal record: every document it wrote reverts to its earlier bytes
// (or vanishes) and the scope revision steps back, as after a crash between the
// journal write and the apply.
func interruptedCommit(t *testing.T, root, scope, txnID string, run func()) {
	t.Helper()
	state := filepath.Join(root, canonicalRoot)
	read := func() map[string][]byte {
		files := map[string][]byte{}
		err := filepath.WalkDir(state, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(path)
			rel, _ := filepath.Rel(state, path)
			files[filepath.ToSlash(rel)] = data
			return err
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		return files
	}
	revPath := filepath.Join(root, home.JournalDirName, scope+".rev")
	before := read()
	revBefore, err := os.ReadFile(revPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	run()
	var items []home.ChangeItem
	for key, data := range read() {
		old, existed := before[key]
		if existed && string(old) == string(data) {
			continue
		}
		items = append(items, home.ChangeItem{Root: canonicalRoot, Key: key, Data: data})
		path := filepath.Join(state, filepath.FromSlash(key))
		if existed {
			err = os.WriteFile(path, old, 0600)
		} else {
			err = os.Remove(path)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	var expected uint64
	if revBefore == nil {
		err = os.Remove(revPath)
	} else {
		_, err = fmt.Sscanf(strings.TrimSpace(string(revBefore)), "%d", &expected)
		if err == nil {
			err = os.WriteFile(revPath, revBefore, 0600)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	rec, err := json.Marshal(struct {
		TxnID            string            `json:"txn_id"`
		Scope            string            `json:"scope"`
		FenceToken       uint64            `json:"fence_token"`
		ExpectedRevision uint64            `json:"expected_revision"`
		NewRevision      uint64            `json:"new_revision"`
		Items            []home.ChangeItem `json:"items"`
	}{txnID, scope, 1, expected, expected + 1, items})
	if err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(root, home.JournalDirName, scope+"."+txnID+".json")
	if err := os.WriteFile(journal, append(rec, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

// pendingCommitRun is one entry point staged for a retry test: run performs the
// operation and reports whether it replayed a prior commit; conflict, when set,
// retries the same operation ID with a different intent.
type pendingCommitRun struct {
	run      func() (replayed bool, err error)
	conflict func() error
}

// A retry of the same operation over its own interrupted commit replays the
// receipt the commit carries instead of refusing it, and a retry of a committed
// operation over a journal record recovery cannot read still replays. Each row
// is one call site of the receipt, recover, receipt order; a retry with a
// different intent over the same pending commit is still refused.
func TestCanonicalRetryOverPendingCommitReplays(t *testing.T) {
	startReq := func(c *Canonical, reason string) CanonicalStartRequest {
		return CanonicalStartRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 1), Reason: reason}
	}
	holdReq := func(c *Canonical) CanonicalAddHoldRequest {
		return CanonicalAddHoldRequest{HomeID: c.HomeID(), HoldID: "t1-decision-k", Scope: DispatchHoldScope{TaskIDs: []string{"t1"}}, Actions: []DispatchAction{DispatchActionSpawn}, Reason: "freeze"}
	}
	rows := []struct {
		name  string
		scope string
		stage func(t *testing.T, c *Canonical) pendingCommitRun
	}{
		{"create", taskScope("t1"), func(t *testing.T, c *Canonical) pendingCommitRun {
			req := createRequest(c, "t1")
			op := mustOperation(t, "op-create-t1", req)
			return pendingCommitRun{run: func() (bool, error) { out, err := c.Create(op, req); return out.Replayed, err }}
		}},
		{"receive", taskScope("t1"), func(t *testing.T, c *Canonical) pendingCommitRun {
			req := receiveTransferRequest(t, c, "t1", "res-t1", "source-home", 3)
			op := mustOperation(t, "op-receive-1", req)
			return pendingCommitRun{run: func() (bool, error) { out, err := c.ReceiveTransfer(op, req); return out.Replayed, err }}
		}},
		{"start", taskScope("t1"), func(t *testing.T, c *Canonical) pendingCommitRun {
			mustCreate(t, c, "t1")
			req, other := startReq(c, "start"), startReq(c, "start again")
			op := mustOperation(t, "op-start-1", req)
			return pendingCommitRun{
				run: func() (bool, error) { out, err := c.Start(op, req); return out.Replayed, err },
				conflict: func() error {
					_, err := c.Start(mustOperation(t, "op-start-1", other), other)
					return err
				},
			}
		}},
		{"reopen", taskScope("t1"), func(t *testing.T, c *Canonical) pendingCommitRun {
			mustCreate(t, c, "t1")
			done := CanonicalCompleteRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 1), To: PhaseDone, Reason: "done"}
			if _, err := c.Complete(mustOperation(t, "op-complete-1", done), done); err != nil {
				t.Fatal(err)
			}
			req := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 2), Reason: "reopen"}
			op := mustOperation(t, "op-reopen-1", req)
			return pendingCommitRun{run: func() (bool, error) { out, err := c.Reopen(op, req); return out.Replayed, err }}
		}},
		{"add hold", holdScope("t1-decision-k"), func(t *testing.T, c *Canonical) pendingCommitRun {
			req := holdReq(c)
			op := mustOperation(t, "op-hold-1", req)
			return pendingCommitRun{run: func() (bool, error) { out, err := c.AddHold(op, req); return out.Replayed, err }}
		}},
		{"release hold", holdScope("t1-decision-k"), func(t *testing.T, c *Canonical) pendingCommitRun {
			add := holdReq(c)
			if _, err := c.AddHold(mustOperation(t, "op-hold-1", add), add); err != nil {
				t.Fatal(err)
			}
			req := CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: add.HoldID, Reason: "thaw", Words: testWords()}
			op := mustOperation(t, "op-release-1", req)
			return pendingCommitRun{run: func() (bool, error) { out, err := c.ReleaseHold(op, req); return out.Replayed, err }}
		}},
		{"authorize delivery", taskScope("t1"), func(t *testing.T, c *Canonical) pendingCommitRun {
			req := authorizeRequest(c, "t1", preconditionOf(1, mustDeliveryTask(t, c, "t1")))
			op := mustOperation(t, "op-auth-1", req)
			return pendingCommitRun{run: func() (bool, error) { out, err := c.AuthorizeDelivery(op, req); return out.Replayed, err }}
		}},
		{"record gate", taskScope("t1"), func(t *testing.T, c *Canonical) pendingCommitRun {
			auth := mustAuthorize(t, c, "t1", mustDeliveryTask(t, c, "t1"), "op-auth-gate")
			req := gateRequest(t, c, auth)
			op := mustOperation(t, "op-gate-1", req)
			// RecordGate reports no replay flag: a replay returns the first
			// call's record, a second commit stamps a new RecordedAt.
			var first *GateRecord
			return pendingCommitRun{run: func() (bool, error) {
				g, err := c.RecordGate(op, req)
				if first == nil {
					first = &g
					return false, err
				}
				return g == *first, err
			}}
		}},
		{"activate transfer", taskScope("t1"), func(t *testing.T, c *Canonical) pendingCommitRun {
			recv := receiveTransferRequest(t, c, "t1", "res-t1", "source-home", 3)
			if _, err := c.ReceiveTransfer(mustOperation(t, "op-receive-1", recv), recv); err != nil {
				t.Fatal(err)
			}
			req := CanonicalActivateTransferRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 1), ReservationID: "res-t1", Reason: "activate"}
			op := mustOperation(t, "op-activate-1", req)
			return pendingCommitRun{run: func() (bool, error) { out, err := c.ActivateTransfer(op, req); return out.Replayed, err }}
		}},
	}
	for _, tt := range rows {
		t.Run(tt.name+"/pending", func(t *testing.T) {
			c, _, root := newTestCanonical(t)
			staged := tt.stage(t, c)
			interruptedCommit(t, root, tt.scope, "pending-txn", func() {
				if replayed, err := staged.run(); err != nil || replayed {
					t.Fatalf("first run: replayed=%v err=%v", replayed, err)
				}
			})
			if staged.conflict != nil {
				if err := staged.conflict(); !errors.Is(err, ErrOperationConflict) {
					t.Fatalf("different intent over the pending commit = %v, want ErrOperationConflict", err)
				}
			}
			if replayed, err := staged.run(); err != nil || !replayed {
				t.Fatalf("retry: replayed=%v err=%v; want a replay", replayed, err)
			}
		})
		// A committed operation replays even when its scope holds a journal
		// record recovery cannot read: the receipt is read before the journal.
		t.Run(tt.name+"/unrecoverable", func(t *testing.T) {
			c, _, root := newTestCanonical(t)
			staged := tt.stage(t, c)
			if replayed, err := staged.run(); err != nil || replayed {
				t.Fatalf("first run: replayed=%v err=%v", replayed, err)
			}
			junk := filepath.Join(root, home.JournalDirName, tt.scope+".junk.json")
			if err := os.WriteFile(junk, []byte("{not json"), 0600); err != nil {
				t.Fatal(err)
			}
			if replayed, err := staged.run(); err != nil || !replayed {
				t.Fatalf("retry over an unrecoverable journal: replayed=%v err=%v; want a replay", replayed, err)
			}
		})
	}
}

// A removed record leaves its scope revision behind. Receive reuses the id at
// that revision, and refuses only when a receipt or hold still names the id.
func TestCanonicalReceiveTransferAfterRecordRemoved(t *testing.T) {
	tests := []struct {
		name        string
		never       bool
		keepReceipt bool
		hold        bool
		wantResidue string
	}{
		{name: "hold on an id never received", never: true, hold: true},
		{name: "scope revision only"},
		{name: "receipt left", keepReceipt: true, wantResidue: "op-create-t1.json"},
		{name: "hold left", hold: true, wantResidue: "t1-decision-k.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _, root := newTestCanonical(t)
			if !tt.never {
				mustCreate(t, c, "t1")
				state := filepath.Join(root, "state", "task-authority")
				if err := os.RemoveAll(filepath.Join(state, "tasks", "t1")); err != nil {
					t.Fatal(err)
				}
				if !tt.keepReceipt {
					if err := os.Remove(filepath.Join(state, "receipts", "op-create-t1.json")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if tt.hold {
				hold := CanonicalAddHoldRequest{HomeID: c.HomeID(), HoldID: "t1-decision-k", Scope: DispatchHoldScope{TaskIDs: []string{"t1"}}, Actions: []DispatchAction{DispatchActionSpawn}, Reason: "freeze"}
				if _, err := c.AddHold(mustOperation(t, "op-hold-t1", hold), hold); err != nil {
					t.Fatal(err)
				}
			}
			req := receiveTransferRequest(t, c, "t1", "res-t1", "source-home", 3)
			out, err := c.ReceiveTransfer(mustOperation(t, "op-receive-1", req), req)
			if tt.wantResidue != "" {
				if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), tt.wantResidue) || !strings.Contains(err.Error(), "munsu cannot clear it") {
					t.Fatalf("ReceiveTransfer = %v, want a conflict naming %s", err, tt.wantResidue)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReceiveTransfer: %v", err)
			}
			act := CanonicalActivateTransferRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(uint64(out.Generation), uint64(out.Revision)), ReservationID: "res-t1", Reason: "activate"}
			if _, err := c.ActivateTransfer(mustOperation(t, "op-activate-1", act), act); err != nil {
				t.Fatalf("ActivateTransfer: %v", err)
			}
		})
	}
}
