//go:build integration

package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// bumpTaskRevisionCanonically commits one unrelated canonical mutation of
// the task (a delivery contract record), advancing its revision past the
// revision any in-flight delivery journal recorded.
func bumpTaskRevisionCanonically(t *testing.T, c *taskauthority.Canonical, taskID, mode string) {
	t.Helper()
	tid := mustFleetTaskID(t, taskID)
	agg, err := c.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalRecordDeliveryContractRequest{
		HomeID: c.HomeID(), TaskID: tid,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Mode:         mode, Rescaffold: true, Reason: "unrelated mutation",
	}
	if _, err := c.RecordDeliveryContract(mustFleetOperation(t, "op-contract-"+taskID+"-"+mode, req), req); err != nil {
		t.Fatalf("RecordDeliveryContract: %v", err)
	}
}

// revokeCurrentAuthorization revokes the task's current delivery
// authorization under a foreign operation identity (another actor).
func revokeCurrentAuthorization(t *testing.T, c *taskauthority.Canonical, taskID string) {
	t.Helper()
	tid := mustFleetTaskID(t, taskID)
	auth, err := c.DeliveryAuthorization(tid)
	if err != nil {
		t.Fatal(err)
	}
	agg, err := c.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalRevokeDeliveryRequest{
		HomeID: c.HomeID(), TaskID: tid,
		Precondition:             domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		AuthorizationOperationID: auth.OperationID, Reason: "abandoned",
	}
	if _, err := c.RevokeDeliveryAuthorization(mustFleetOperation(t, "op-foreign-revoke-"+taskID, req), req); err != nil {
		t.Fatal(err)
	}
}

func taskRevision(t *testing.T, c *taskauthority.Canonical, taskID string) taskauthority.Revision {
	t.Helper()
	agg, err := c.Get(mustFleetTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	return agg.Revision
}

// TestDeliverStaleRevisionAuthorizedJournalReleases proves a journal crashed
// at the authorized stage converges after an unrelated canonical mutation
// bumped the task revision: the authorization is still the journal's, so the
// fail-closed release runs at the current revision and the journal
// completes, with no provider mutation.
func TestDeliverStaleRevisionAuthorizedJournalReleases(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)
	installScriptedProviderFor(t, "open-then-merged")
	runDeliveryCrashHelper(t, homeDir, taskID, "authorized", "open-then-merged")
	bumpTaskRevisionCanonically(t, c, taskID, "no-mistakes")

	provider := installScriptedProviderFor(t, "open-then-merged")
	err := RecoverDeliveryJournals(homeDir)
	var failClosed *DeliveryFailClosedError
	if !errors.As(err, &failClosed) || failClosed.ReleaseErr != nil {
		t.Fatalf("RecoverDeliveryJournals err = %v, want fail-closed with a completed release", err)
	}
	if provider.merges != 0 {
		t.Fatalf("merges = %d, want 0", provider.merges)
	}
	if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
		t.Fatalf("active journals = %v, want none", active)
	}
	cur, err := c.DeliveryCurrency(mustFleetTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if !isRevokedCurrency(cur) {
		t.Fatalf("currency = %+v, want the journal's authorization revoked", cur.Reasons)
	}
	if err := RecoverDeliveryJournals(homeDir); err != nil {
		t.Fatalf("second recovery: %v", err)
	}
}

// TestDeliverStaleRevisionOutcomeJournalCommits proves a journal crashed with
// a pinned but uncommitted outcome converges after an unrelated revision
// bump: the authorization is still the journal's, so the outcome commits at
// the current revision.
func TestDeliverStaleRevisionOutcomeJournalCommits(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)
	installScriptedProviderFor(t, "open-then-merged")
	runDeliveryCrashHelper(t, homeDir, taskID, "outcome", "open-then-merged")
	bumpTaskRevisionCanonically(t, c, taskID, "no-mistakes")

	provider := installScriptedProviderFor(t, "merged")
	if err := RecoverDeliveryJournals(homeDir); err != nil {
		t.Fatalf("RecoverDeliveryJournals: %v", err)
	}
	if provider.merges != 0 {
		t.Fatalf("merges = %d, want 0", provider.merges)
	}
	out, err := c.DeliveryOutcome(mustFleetTaskID(t, taskID))
	if err != nil || out.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("outcome = %v %+v, want completed", err, out)
	}
	if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
		t.Fatalf("active journals = %v, want none", active)
	}
}

// TestDeliverStaleRevisionAuthorizationGoneCompletesWithoutMutation proves a
// stale journal whose authorization was revoked by another actor completes
// as terminal truth with no canonical mutation, at either stage.
func TestDeliverStaleRevisionAuthorizationGoneCompletesWithoutMutation(t *testing.T) {
	for _, boundary := range []string{"authorized", "outcome"} {
		t.Run(boundary, func(t *testing.T) {
			c, homeDir := newFleetCanonical(t)
			taskID := "t1"
			mustWorkingDeliveryTask(t, c, taskID)
			installScriptedProviderFor(t, "open-then-merged")
			runDeliveryCrashHelper(t, homeDir, taskID, boundary, "open-then-merged")
			journalID := listActiveDeliveryJournals(t, homeDir)[0]
			revokeCurrentAuthorization(t, c, taskID)
			bumpTaskRevisionCanonically(t, c, taskID, "no-mistakes")
			before := taskRevision(t, c, taskID)

			provider := installScriptedProviderFor(t, "merged")
			err := RecoverDeliveryJournals(homeDir)
			var failClosed *DeliveryFailClosedError
			if !errors.As(err, &failClosed) || failClosed.ReleaseErr != nil {
				t.Fatalf("RecoverDeliveryJournals err = %v, want fail-closed terminal completion", err)
			}
			if provider.merges != 0 {
				t.Fatalf("merges = %d, want 0", provider.merges)
			}
			if after := taskRevision(t, c, taskID); after != before {
				t.Fatalf("task revision %d -> %d, want no canonical mutation", before, after)
			}
			if _, err := c.DeliveryOutcome(mustFleetTaskID(t, taskID)); !errors.Is(err, taskauthority.ErrNotFound) {
				t.Fatalf("outcome err = %v, want none committed", err)
			}
			journal, err := readDeliveryJournalRecord(t, homeDir, journalID)
			if err != nil || journal.Phase != deliveryPhaseCompleted {
				t.Fatalf("journal = %v %+v, want completed", err, journal)
			}
			if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
				t.Fatalf("active journals = %v, want none", active)
			}
		})
	}
}

// TestDeliverCrashAfterOutcomeCommitThenRevisionBumpConverges proves the
// crash-replay of the outcome and revoke operations stays idempotent when the
// operations fence the current revision: after a crash following the
// canonical outcome commit and an unrelated revision bump, recovery reuses
// the committed outcome (no second outcome), and a retryable outcome's
// release runs once and is skipped once revoked.
func TestDeliverCrashAfterOutcomeCommitThenRevisionBumpConverges(t *testing.T) {
	for _, tc := range []struct {
		script string
		status taskauthority.DeliveryOutcomeStatus
	}{
		{"open-then-merged", taskauthority.DeliveryOutcomeCompleted},
		{"open", taskauthority.DeliveryOutcomeRetryable},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
			c, homeDir := newFleetCanonical(t)
			taskID := "t1"
			mustWorkingDeliveryTask(t, c, taskID)
			installScriptedProviderFor(t, tc.script)
			runDeliveryCrashHelper(t, homeDir, taskID, "committed", tc.script)
			committed, err := c.DeliveryOutcome(mustFleetTaskID(t, taskID))
			if err != nil || committed.Status != tc.status {
				t.Fatalf("committed outcome = %v %+v, want %s", err, committed, tc.status)
			}
			bumpTaskRevisionCanonically(t, c, taskID, "no-mistakes")

			provider := installScriptedProviderFor(t, "merged")
			for i := 0; i < 2; i++ {
				if err := RecoverDeliveryJournals(homeDir); err != nil {
					t.Fatalf("recovery %d: %v", i, err)
				}
			}
			if provider.merges != 0 {
				t.Fatalf("merges = %d, want 0", provider.merges)
			}
			out, err := c.DeliveryOutcome(mustFleetTaskID(t, taskID))
			if err != nil || out.OperationID != committed.OperationID || out.CommittedAt != committed.CommittedAt {
				t.Fatalf("outcome = %v %+v, want the one committed before the crash", err, out)
			}
			if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
				t.Fatalf("active journals = %v, want none", active)
			}
			cur, err := c.DeliveryCurrency(mustFleetTaskID(t, taskID))
			if err != nil {
				t.Fatal(err)
			}
			if tc.status == taskauthority.DeliveryOutcomeRetryable && !isRevokedCurrency(cur) {
				t.Fatalf("currency = %+v, want the retryable authorization released", cur.Reasons)
			}
		})
	}
}

// writeAuthorizeStageJournal writes a fresh delivery journal (stage
// authorize) for a task directly, without running Deliver (which would
// recover every other pending journal first).
func writeAuthorizeStageJournal(t *testing.T, c *taskauthority.Canonical, homeDir, taskID string) *deliveryJournal {
	t.Helper()
	h, err := home.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	lk, err := h.Lock(deliveryLockScope)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	agg, err := c.Get(mustFleetTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	journal, err := buildDeliveryJournal(h.Root(), c, agg, deliverRequest(), "squash")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDeliveryJournal(h, lk, journal); err != nil {
		t.Fatal(err)
	}
	return journal
}

// TestDeliverRecoveryContinuesPastFailedJournal proves one journal's resume
// failure no longer blocks the others, and that a release refused by a fence
// (an active transfer reservation) while the authorization is still current
// never completes the journal.
func TestDeliverRecoveryContinuesPastFailedJournal(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	mustWorkingDeliveryTask(t, c, "t1")
	mustWorkingDeliveryTask(t, c, "t2")
	installScriptedProviderFor(t, "open-then-merged")
	runDeliveryCrashHelper(t, homeDir, "t1", "authorized", "open-then-merged")
	agg, err := c.Get(mustFleetTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	reserve := taskauthority.CanonicalReserveTransferRequest{
		HomeID: c.HomeID(), TaskID: mustFleetTaskID(t, "t1"),
		Precondition:  domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		ReservationID: "res-t1", Destination: mustFleetHomeID(t, "dest-home"), FenceToken: "fence-res", Reason: "transfer",
	}
	if _, err := c.ReserveTransfer(mustFleetOperation(t, "op-res-t1", reserve), reserve); err != nil {
		t.Fatal(err)
	}
	first := listActiveDeliveryJournals(t, homeDir)[0]
	second := writeAuthorizeStageJournal(t, c, homeDir, "t2")

	provider := installScriptedProviderFor(t, "open-then-merged")
	err = RecoverDeliveryJournals(homeDir)
	var failClosed *DeliveryFailClosedError
	if !errors.As(err, &failClosed) || failClosed.ReleaseErr == nil || !errors.Is(failClosed.ReleaseErr, taskauthority.ErrConflict) {
		t.Fatalf("RecoverDeliveryJournals err = %v, want t1's release refused by the reservation", err)
	}
	if active := listActiveDeliveryJournals(t, homeDir); len(active) != 1 || active[0] != first {
		t.Fatalf("active journals = %v, want only %s", active, first)
	}
	cur, err := c.DeliveryCurrency(mustFleetTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if cur.Authorization == nil || isRevokedCurrency(cur) {
		t.Fatalf("t1 currency = %+v, want its authorization still live", cur.Reasons)
	}
	if provider.merges != 1 {
		t.Fatalf("merges = %d, want 1 (t2 delivered)", provider.merges)
	}
	out, err := c.DeliveryOutcome(mustFleetTaskID(t, "t2"))
	if err != nil || out.Status != taskauthority.DeliveryOutcomeCompleted || out.OperationID != second.OutcomeOpID {
		t.Fatalf("t2 outcome = %v %+v, want completed", err, out)
	}
}

// TestDeliverRecoveryStopsBeforeResumingOnContradictoryEntry proves recovery
// validates every active entry before resuming any: a missing journal listed
// after a valid one stops recovery and the valid journal is not resumed.
func TestDeliverRecoveryStopsBeforeResumingOnContradictoryEntry(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	mustWorkingDeliveryTask(t, c, "t1")
	mustWorkingDeliveryTask(t, c, "t2")
	writeAuthorizeStageJournal(t, c, homeDir, "t1")
	missing := writeAuthorizeStageJournal(t, c, homeDir, "t2")
	if err := os.Remove(filepath.Join(homeDir, "state", deliveryJournalKey(missing.ID))); err != nil {
		t.Fatal(err)
	}

	provider := installScriptedProviderFor(t, "open-then-merged")
	if err := RecoverDeliveryJournals(homeDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("RecoverDeliveryJournals err = %v, want the missing journal", err)
	}
	if _, err := c.DeliveryAuthorization(mustFleetTaskID(t, "t1")); !errors.Is(err, taskauthority.ErrNotFound) {
		t.Fatalf("t1 authorization err = %v, want none (nothing resumed)", err)
	}
	if provider.merges != 0 {
		t.Fatalf("merges = %d, want 0", provider.merges)
	}
}

// TestDeliverRecoveryRefusesMalformedCommittedOutcome proves a committed
// outcome record that cannot be read (anything but not-found) surfaces as a
// recovery error and keeps the journal active: recovery neither resubmits the
// outcome nor completes the journal over evidence it cannot read.
func TestDeliverRecoveryRefusesMalformedCommittedOutcome(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)
	installScriptedProviderFor(t, "open-then-merged")
	runDeliveryCrashHelper(t, homeDir, taskID, "outcome", "open-then-merged")

	active := listActiveDeliveryJournals(t, homeDir)
	if len(active) != 1 {
		t.Fatalf("active journals = %v, want one", active)
	}
	h, err := home.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := readActiveDeliveryJournal(h, active[0])
	if err != nil {
		t.Fatal(err)
	}
	path, err := h.Path(home.RootState, "task-authority/delivery/"+taskID+"/outcomes/"+journal.OutcomeOpID+".json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	provider := installScriptedProviderFor(t, "merged")
	err = RecoverDeliveryJournals(homeDir)
	if err == nil || !strings.Contains(err.Error(), "resolving committed delivery outcome") {
		t.Fatalf("RecoverDeliveryJournals err = %v, want the unreadable committed outcome surfaced", err)
	}
	if provider.merges != 0 {
		t.Fatalf("merges = %d, want 0", provider.merges)
	}
	if got := listActiveDeliveryJournals(t, homeDir); len(got) != 1 || got[0] != active[0] {
		t.Fatalf("active journals = %v, want %v still active", got, active)
	}
}

func addDeliveryHold(t *testing.T, c *taskauthority.Canonical, taskID, holdID string) {
	t.Helper()
	req := taskauthority.CanonicalAddHoldRequest{
		HomeID: c.HomeID(), HoldID: holdID, Scope: taskauthority.DispatchHoldScope{TaskIDs: []string{taskID}},
		Actions: []taskauthority.DispatchAction{taskauthority.DispatchActionDelivery}, Reason: "freeze",
	}
	if _, err := c.AddHold(mustFleetOperation(t, "op-add-"+holdID, req), req); err != nil {
		t.Fatalf("AddHold: %v", err)
	}
}

func releaseDeliveryHold(t *testing.T, c *taskauthority.Canonical, holdID string) {
	t.Helper()
	req := taskauthority.CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: holdID, Reason: "thaw"}
	if _, err := c.ReleaseHold(mustFleetOperation(t, "op-release-"+holdID, req), req); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
}

// deliverWithHoldAt runs one Deliver of a merging MR and adds a delivery
// hold on the task when the delivery reaches boundary: "mutating" is just
// before the provider merge, "outcome" just after it. The merge happens and
// the outcome commit is refused by the matching hold, leaving the journal
// active.
func deliverWithHoldAt(t *testing.T, c *taskauthority.Canonical, homeDir, taskID, boundary string) *fakeDeliveryProvider {
	t.Helper()
	provider := installScriptedProviderFor(t, "open-then-merged")
	restore := SetDeliveryCrashHookForTest(func(got string) {
		if got == boundary {
			addDeliveryHold(t, c, taskID, "hold-"+taskID)
		}
	})
	_, err := Deliver(homeDir, taskID, deliverRequest())
	restore()
	if err == nil || !strings.Contains(err.Error(), string(taskauthority.DeliveryCurrencyMatchingHold)) {
		t.Fatalf("Deliver err = %v, want the outcome commit refused by the matching hold", err)
	}
	if provider.merges != 1 {
		t.Fatalf("merges = %d, want 1", provider.merges)
	}
	if active := listActiveDeliveryJournals(t, homeDir); len(active) != 1 {
		t.Fatalf("active journals = %v, want the refused journal still active", active)
	}
	return provider
}

// assertOutcomeRecordedOnce asserts the task has exactly one committed,
// completed delivery outcome and no active journal.
func assertOutcomeRecordedOnce(t *testing.T, c *taskauthority.Canonical, homeDir, taskID string) {
	t.Helper()
	out, err := c.DeliveryOutcome(mustFleetTaskID(t, taskID))
	if err != nil || out.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("outcome = %v %+v, want completed", err, out)
	}
	h, err := home.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := h.Path(home.RootState, "task-authority/delivery/"+taskID+"/outcomes")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("outcome records = %v (err %v), want exactly one", entries, err)
	}
	if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
		t.Fatalf("active journals = %v, want none", active)
	}
}

// TestDeliverOutcomeCommitsAfterHoldReleased proves a hold added
// before the merge and released before recovery leaves only holds-digest
// drift, which the outcome commit ignores; the outcome records once and the
// journal completes.
func TestDeliverOutcomeCommitsAfterHoldReleased(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	mustWorkingDeliveryTask(t, c, "t1")
	provider := deliverWithHoldAt(t, c, homeDir, "t1", "mutating")
	releaseDeliveryHold(t, c, "hold-t1")

	if err := RecoverDeliveryJournals(homeDir); err != nil {
		t.Fatalf("RecoverDeliveryJournals: %v", err)
	}
	if provider.merges != 1 {
		t.Fatalf("merges = %d, want 1", provider.merges)
	}
	assertOutcomeRecordedOnce(t, c, homeDir, "t1")
}

// TestDeliverOutcomeRefusedWhileHoldActive covers a hold added
// before the merge and a hold added after it: while the hold is
// active the outcome commit is refused and the journal stays active; once
// it is released, recovery records the outcome.
func TestDeliverOutcomeRefusedWhileHoldActive(t *testing.T) {
	for _, boundary := range []string{"mutating", "outcome"} {
		t.Run(boundary, func(t *testing.T) {
			c, homeDir := newFleetCanonical(t)
			mustWorkingDeliveryTask(t, c, "t1")
			provider := deliverWithHoldAt(t, c, homeDir, "t1", boundary)

			err := RecoverDeliveryJournals(homeDir)
			if err == nil || !strings.Contains(err.Error(), string(taskauthority.DeliveryCurrencyMatchingHold)) {
				t.Fatalf("RecoverDeliveryJournals err = %v, want matching-hold refusal", err)
			}
			if _, err := c.DeliveryOutcome(mustFleetTaskID(t, "t1")); !errors.Is(err, taskauthority.ErrNotFound) {
				t.Fatalf("outcome err = %v, want none recorded while the hold is active", err)
			}
			if active := listActiveDeliveryJournals(t, homeDir); len(active) != 1 {
				t.Fatalf("active journals = %v, want one", active)
			}

			releaseDeliveryHold(t, c, "hold-t1")
			if err := RecoverDeliveryJournals(homeDir); err != nil {
				t.Fatalf("RecoverDeliveryJournals after release: %v", err)
			}
			if provider.merges != 1 {
				t.Fatalf("merges = %d, want 1", provider.merges)
			}
			assertOutcomeRecordedOnce(t, c, homeDir, "t1")
		})
	}
}
