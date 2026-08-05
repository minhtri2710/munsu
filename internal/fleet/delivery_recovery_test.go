//go:build integration

package fleet

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestDeliverCrashBeforeMutationRetriesSafely proves a crash after the
// journal intent and authorization but before the irreversible mutation is
// recovered by re-running the SAME journal: the currency check runs again
// and the mutation executes exactly once on retry.
func TestDeliverCrashBeforeMutationRetriesSafely(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)
	installScriptedProviderFor(t, "open-then-merged")

	runDeliveryCrashHelper(t, homeDir, taskID, "authorized", "open-then-merged")

	// The journal is active, the authorization is committed, and no outcome
	// exists (the crash happened before the mutation).
	active := listActiveDeliveryJournals(t, homeDir)
	if len(active) != 1 {
		t.Fatalf("active journals = %v, want exactly 1", active)
	}
	if _, err := c.DeliveryAuthorization(mustFleetTaskID(t, taskID)); err != nil {
		t.Fatalf("authorization not issued: %v", err)
	}
	if _, err := c.DeliveryOutcome(mustFleetTaskID(t, taskID)); err == nil {
		t.Fatal("outcome committed before the mutation")
	}

	// Recovery retries safely: the mutation executes exactly once.
	provider := installScriptedProviderFor(t, "open-then-merged")
	if err := RecoverDeliveryJournals(homeDir); err != nil {
		t.Fatalf("RecoverDeliveryJournals: %v", err)
	}
	if provider.merges != 1 {
		t.Fatalf("merges on retry = %d, want exactly 1", provider.merges)
	}
	out, err := c.DeliveryOutcome(mustFleetTaskID(t, taskID))
	if err != nil || out.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("outcome = %v %+v, want completed", err, out)
	}
	if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
		t.Fatalf("active journals after recovery = %v, want none", active)
	}
}

// TestDeliverCrashAfterMutationNeverRepeatsMutation proves a crash after the
// irreversible mutation (outcome committed, journal incomplete) is recovered
// by replaying the committed truth; the mutation is NEVER repeated.
func TestDeliverCrashAfterMutationNeverRepeatsMutation(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)
	installScriptedProviderFor(t, "open-then-merged")

	runDeliveryCrashHelper(t, homeDir, taskID, "committed", "open-then-merged")

	// The outcome committed in the subprocess (mutation executed once there).
	out, err := c.DeliveryOutcome(mustFleetTaskID(t, taskID))
	if err != nil || out.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("outcome = %v %+v, want completed", err, out)
	}
	active := listActiveDeliveryJournals(t, homeDir)
	if len(active) != 1 {
		t.Fatalf("active journals = %v, want exactly 1 (journal incomplete)", active)
	}

	// Recovery replays the committed outcome and NEVER re-mutates.
	provider := installScriptedProviderFor(t, "merged")
	if err := RecoverDeliveryJournals(homeDir); err != nil {
		t.Fatalf("RecoverDeliveryJournals: %v", err)
	}
	if provider.merges != 0 {
		t.Fatalf("recovery repeated the mutation: merges = %d, want 0", provider.merges)
	}
	out2, err := c.DeliveryOutcome(mustFleetTaskID(t, taskID))
	if err != nil || out2.Status != taskauthority.DeliveryOutcomeCompleted || out2.OperationID != out.OperationID {
		t.Fatalf("outcome after recovery = %v %+v, want the committed %s", err, out2, out.OperationID)
	}
	if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
		t.Fatalf("active journals after recovery = %v, want none", active)
	}
}

// TestDeliverCrashAtMutatingReconcilesObservationAndNeverMutates proves a
// crash after the durable mutating boundary (the mutation may or may not have
// executed) is recovered by provider observation and classification; the
// mutation is never repeated and the truthful outcome is committed.
func TestDeliverCrashAtMutatingReconcilesObservationAndNeverMutates(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)
	installScriptedProviderFor(t, "open-then-merged")

	// Crash at the durable mutating boundary: the subprocess persisted
	// stage=mutating but the Merge call never executed.
	runDeliveryCrashHelper(t, homeDir, taskID, "mutating", "open-then-merged")

	active := listActiveDeliveryJournals(t, homeDir)
	if len(active) != 1 {
		t.Fatalf("active journals = %v, want exactly 1", active)
	}

	// Recovery observes remote truth (merged) and NEVER mutates.
	provider := installScriptedProviderFor(t, "merged")
	if err := RecoverDeliveryJournals(homeDir); err != nil {
		t.Fatalf("RecoverDeliveryJournals: %v", err)
	}
	if provider.merges != 0 {
		t.Fatalf("recovery mutated: merges = %d, want 0", provider.merges)
	}
	out, err := c.DeliveryOutcome(mustFleetTaskID(t, taskID))
	if err != nil || out.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("outcome = %v %+v, want completed via observation", err, out)
	}
	if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
		t.Fatalf("active journals after recovery = %v, want none", active)
	}
}

// TestDeliverOutcomeCommitConflictYieldsCommittedNeverCompleted proves a
// canonical outcome commit conflict after mutation resolves to the already
// committed record (partial/remote-unknown stand) and completed is never
// fabricated.
func TestDeliverOutcomeCommitConflictYieldsCommittedNeverCompleted(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)
	installScriptedProviderFor(t, "open-then-merged")

	// Crash at the outcome boundary: the journal pinned a completed outcome
	// but the canonical commit never ran.
	runDeliveryCrashHelper(t, homeDir, taskID, "outcome", "open-then-merged")

	active := listActiveDeliveryJournals(t, homeDir)
	if len(active) != 1 {
		t.Fatalf("active journals = %v, want exactly 1", active)
	}
	journal, err := readDeliveryJournalRecord(t, homeDir, active[0])
	if err != nil {
		t.Fatal(err)
	}
	if journal.Stage != deliveryStageOutcome || journal.OutcomeStatus != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("journal = stage %q outcome %q, want outcome/completed", journal.Stage, journal.OutcomeStatus)
	}

	// A prior attempt committed a terminal remote-unknown outcome under the
	// journal's exact outcome operation identity with a DIFFERENT digest
	// (simulating the crash-after-commit window where a later observation
	// derived different truth).
	tid := mustFleetTaskID(t, taskID)
	committedReq := taskauthority.CanonicalDeliveryOutcomeRequest{
		HomeID:                   c.HomeID(),
		TaskID:                   tid,
		Precondition:             domain.Of(journal.Generation, journal.Revision+1),
		AuthorizationOperationID: journal.AuthorizeOpID,
		Status:                   taskauthority.DeliveryOutcomeRemoteUnknown,
		Detail:                   "provider unreachable after mutation",
		HeadSHA:                  journal.OutcomeHeadSHA,
	}
	if _, err := c.CommitDeliveryOutcome(mustFleetOperation(t, journal.OutcomeOpID, committedReq), committedReq); err != nil {
		t.Fatalf("committing prior remote-unknown outcome: %v", err)
	}

	// Recovery commits the pinned outcome and hits the operation-identity
	// conflict; the committed remote-unknown record stands and completed is
	// never fabricated.
	provider := installScriptedProviderFor(t, "merged")
	if err := RecoverDeliveryJournals(homeDir); err != nil {
		t.Fatalf("RecoverDeliveryJournals: %v", err)
	}
	if provider.merges != 0 {
		t.Fatalf("recovery mutated: merges = %d, want 0", provider.merges)
	}
	out, err := c.DeliveryOutcome(tid)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != taskauthority.DeliveryOutcomeRemoteUnknown {
		t.Fatalf("outcome = %q, want the committed remote-unknown (completed never fabricated)", out.Status)
	}
	if out.OperationID != journal.OutcomeOpID {
		t.Fatalf("outcome operation = %q, want %q", out.OperationID, journal.OutcomeOpID)
	}
	if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
		t.Fatalf("active journals after recovery = %v, want none", active)
	}
}

// TestDeliverRecoveryIdempotentReplay proves repeated recovery of the same
// journal is idempotent: the committed outcome never changes and the active
// index stays empty after completion.
func TestDeliverRecoveryIdempotentReplay(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)
	installScriptedProviderFor(t, "open-then-merged")

	runDeliveryCrashHelper(t, homeDir, taskID, "committed", "open-then-merged")

	provider := installScriptedProviderFor(t, "merged")
	for i := 0; i < 3; i++ {
		if err := RecoverDeliveryJournals(homeDir); err != nil {
			t.Fatalf("RecoverDeliveryJournals cycle %d: %v", i, err)
		}
	}
	if provider.merges != 0 {
		t.Fatalf("repeated recovery mutated: merges = %d, want 0", provider.merges)
	}
	out, err := c.DeliveryOutcome(mustFleetTaskID(t, taskID))
	if err != nil || out.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("outcome = %v %+v, want completed (unchanged)", err, out)
	}
	if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
		t.Fatalf("active journals = %v, want none", active)
	}
}

// TestDeliverActiveIndexStaysBounded proves many delivery cycles leave the
// active index bounded (never scanning, always empty after completion) while
// every completed journal record remains durably retained.
func TestDeliverActiveIndexStaysBounded(t *testing.T) {
	_, homeDir := newFleetCanonical(t)
	c := mustCanonicalForHome(t, homeDir)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)

	const cycles = 4
	for i := 0; i < cycles; i++ {
		installScriptedProviderFor(t, "open") // open after merge -> retryable -> release
		if _, err := Deliver(homeDir, taskID, deliverRequest()); err != nil {
			t.Fatalf("cycle %d: Deliver: %v", i, err)
		}
		if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
			t.Fatalf("cycle %d: active journals = %v, want none after completion", i, active)
		}
	}

	// The active index is bounded (empty) and the completed records remain
	// durable but undiscovered by the index.
	if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
		t.Fatalf("active journals = %v, want none", active)
	}
	files := listDeliveryJournalFiles(t, homeDir)
	if len(files) != cycles {
		t.Fatalf("retained completed records = %d, want %d (all durable)", len(files), cycles)
	}
	// The canonical delivery index itself stays bounded: one authorization,
	// one revocation, one outcome pointer regardless of cycles.
	idx, err := readDeliveryIndexFile(t, homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if idx.AuthorizationOpID == "" || idx.RevocationOpID == "" || idx.OutcomeOpID == "" {
		t.Fatalf("bounded delivery index incomplete: %+v", idx)
	}
}

// TestDeliverRecoveryFailClosedBeforeMutation proves a recovery whose
// provider observation fails closed (currency invalid) keeps the journal
// active and never mutates.
func TestDeliverRecoveryFailClosedBeforeMutation(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)
	installScriptedProviderFor(t, "open-then-merged")

	runDeliveryCrashHelper(t, homeDir, taskID, "authorized", "open-then-merged")

	// Break the currency at recovery time: add a matching delivery hold (the
	// release succeeds because the task revision is unchanged).
	hold := taskauthority.CanonicalAddHoldRequest{
		HomeID: c.HomeID(), HoldID: "hold-recovery", Scope: taskauthority.DispatchHoldScope{TaskIDs: []string{taskID}},
		Actions: []taskauthority.DispatchAction{taskauthority.DispatchActionDelivery}, Reason: "freeze",
	}
	if _, err := c.AddHold(mustFleetOperation(t, "op-hold-recovery", hold), hold); err != nil {
		t.Fatal(err)
	}

	provider := installScriptedProviderFor(t, "open-then-merged")
	err := RecoverDeliveryJournals(homeDir)
	var failClosed *DeliveryFailClosedError
	if !errors.As(err, &failClosed) {
		t.Fatalf("RecoverDeliveryJournals err = %T %v, want *DeliveryFailClosedError", err, err)
	}
	if provider.merges != 0 {
		t.Fatalf("recovery mutated after fail-closed currency: merges = %d, want 0", provider.merges)
	}
	if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
		t.Fatalf("active journals = %v, want none (release completed the journal)", active)
	}
}

// mustCanonicalForHome opens the canonical authority over the given home.
func mustCanonicalForHome(t *testing.T, homeDir string) *taskauthority.Canonical {
	t.Helper()
	c, err := canonicalAuthority(homeDir)
	if err != nil {
		t.Fatalf("canonicalAuthority: %v", err)
	}
	return c
}

// readDeliveryIndexFile reads the bounded per-task canonical delivery index.
func readDeliveryIndexFile(t *testing.T, homeDir, taskID string) (struct {
	AuthorizationOpID string `json:"authorization_op_id"`
	RevocationOpID    string `json:"revocation_op_id"`
	OutcomeOpID       string `json:"outcome_op_id"`
}, error) {
	t.Helper()
	var out struct {
		AuthorizationOpID string `json:"authorization_op_id"`
		RevocationOpID    string `json:"revocation_op_id"`
		OutcomeOpID       string `json:"outcome_op_id"`
	}
	data, err := os.ReadFile(filepath.Join(homeDir, "state", "task-authority", "delivery", taskID, "current.json"))
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}
