package taskauthority

import (
	"errors"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

func contractRequest(c *Canonical, taskID string, prec domain.Precondition, mode string) CanonicalRecordDeliveryContractRequest {
	id, _ := domain.NewTaskID(taskID)
	return CanonicalRecordDeliveryContractRequest{
		HomeID:       c.HomeID(),
		TaskID:       id,
		Precondition: prec,
		Mode:         mode,
		Reason:       "spawn",
	}
}

func mustRecordContract(t *testing.T, c *Canonical, opID, taskID string, prec domain.Precondition, mode string) Outcome {
	t.Helper()
	req := contractRequest(c, taskID, prec, mode)
	out, err := c.RecordDeliveryContract(mustOperation(t, opID, req), req)
	if err != nil {
		t.Fatalf("RecordDeliveryContract(%s, %s): %v", taskID, mode, err)
	}
	return out
}

func TestCanonicalRecordDeliveryContract(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	out := mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "no-mistakes")
	if out.Generation != 1 || out.Revision != 2 {
		t.Fatalf("record outcome = %+v", out)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryContract == nil {
		t.Fatal("no delivery contract recorded")
	}
	if agg.DeliveryContract.Mode != "no-mistakes" {
		t.Fatalf("contract mode = %q", agg.DeliveryContract.Mode)
	}
	if agg.DeliveryContract.OperationID != "op-contract-1" || agg.DeliveryContract.RecordedAt <= 0 {
		t.Fatalf("contract = %+v", *agg.DeliveryContract)
	}
}

// TestCanonicalRecordDeliveryContractRefusesSilentOverride builds the exact
// refused state the contract exists to prevent: a task that already contracts
// one delivery mode, asked to record a DIFFERENT one with no re-scaffold
// intent. The committed contract must survive intact.
func TestCanonicalRecordDeliveryContractRefusesSilentOverride(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "no-mistakes")

	req := contractRequest(c, "t1", preconditionOf(1, 2), "direct-PR")
	if _, err := c.RecordDeliveryContract(mustOperation(t, "op-contract-override", req), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("silent override = %v, want ErrConflict", err)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryContract == nil || agg.DeliveryContract.Mode != "no-mistakes" {
		t.Fatalf("refused override mutated the contract: %+v", agg.DeliveryContract)
	}
}

// TestCanonicalRecordDeliveryContractRescaffoldReplaces pins the one
// sanctioned way a recorded contract changes.
func TestCanonicalRecordDeliveryContractRescaffoldReplaces(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "no-mistakes")

	req := contractRequest(c, "t1", preconditionOf(1, 2), "local-only")
	req.Rescaffold = true
	if _, err := c.RecordDeliveryContract(mustOperation(t, "op-contract-rescaffold", req), req); err != nil {
		t.Fatalf("re-scaffold: %v", err)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryContract == nil || agg.DeliveryContract.Mode != "local-only" {
		t.Fatalf("re-scaffold did not replace the contract: %+v", agg.DeliveryContract)
	}
}

// TestCanonicalRecordDeliveryContractSameModeIsNoOp keeps a re-entrant spawn
// from bumping the revision on a contract it already agrees with.
func TestCanonicalRecordDeliveryContractSameModeIsNoOp(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "direct-PR")

	out := mustRecordContract(t, c, "op-contract-2", "t1", preconditionOf(1, 2), "direct-PR")
	if out.Revision != 2 {
		t.Fatalf("re-record bumped the revision: %+v", out)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryContract.OperationID != "op-contract-1" {
		t.Fatalf("re-record rewrote the committed contract: %+v", *agg.DeliveryContract)
	}
}

// TestCanonicalRecordDeliveryContractReplaysByOperationID pins idempotent
// replay: the same Operation ID and digest returns the durable prior outcome.
func TestCanonicalRecordDeliveryContractReplaysByOperationID(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	first := mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "no-mistakes")
	replay := mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "no-mistakes")
	if replay.Revision != first.Revision || replay.Generation != first.Generation {
		t.Fatalf("replay = %+v, first = %+v", replay, first)
	}
}

// TestCanonicalRecordDeliveryContractRejectsUnknownMode keeps an
// unenforceable mode out of the durable record.
func TestCanonicalRecordDeliveryContractRejectsUnknownMode(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	for _, mode := range []string{"", "direct-pr", "yolo"} {
		req := contractRequest(c, "t1", preconditionOf(1, 1), mode)
		_, err := c.RecordDeliveryContract(mustOperation(t, "op-contract-bad-"+mode, req), req)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("mode %q = %v, want ErrInvalidInput", mode, err)
		}
	}
}

// TestCanonicalDeliveryContractSurvivesReopen pins the contract as per-TASK,
// not per-generation: the next generation inherits it so the next spawn reads
// it instead of re-resolving the mode fresh.
func TestCanonicalDeliveryContractSurvivesReopen(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "local-only")

	complete := CanonicalCompleteRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 2),
		To:           PhaseDone,
		Reason:       "done",
	}
	if _, err := c.Complete(mustOperation(t, "op-complete-c", complete), complete); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	reopen := CanonicalReopenRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 3),
		Reason:       "reopen",
	}
	if _, err := c.Reopen(mustOperation(t, "op-reopen-c", reopen), reopen); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Generation != 2 {
		t.Fatalf("generation = %d", agg.Generation)
	}
	if agg.DeliveryContract == nil || agg.DeliveryContract.Mode != "local-only" {
		t.Fatalf("reopened generation lost the delivery contract: %+v", agg.DeliveryContract)
	}
	// The carried record must be a copy, never an alias of the historical
	// generation's pointer.
	hist, err := c.GetGeneration(mustTaskID(t, "t1"), 1)
	if err != nil {
		t.Fatalf("GetGeneration(1): %v", err)
	}
	if hist.DeliveryContract == agg.DeliveryContract {
		t.Fatal("reopened generation aliases the prior generation's contract pointer")
	}
}

func fallbackRequest(c *Canonical, taskID string, prec domain.Precondition, from, to string) CanonicalRecordDeliveryFallbackRequest {
	id, _ := domain.NewTaskID(taskID)
	return CanonicalRecordDeliveryFallbackRequest{
		HomeID:       c.HomeID(),
		TaskID:       id,
		Precondition: prec,
		From:         from,
		To:           to,
		Reason:       "no-mistakes blocked (gate): missing binary",
	}
}

// TestCanonicalRecordDeliveryFallback pins the transition: after an
// authorized fallback the contract states the mode in force AND how it got
// there, rather than continuing to state the pre-fallback mode.
func TestCanonicalRecordDeliveryFallback(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "no-mistakes")

	req := fallbackRequest(c, "t1", preconditionOf(1, 2), "no-mistakes", "direct-PR")
	out, err := c.RecordDeliveryFallback(mustOperation(t, "op-fallback-1", req), req)
	if err != nil {
		t.Fatalf("RecordDeliveryFallback: %v", err)
	}
	if out.Generation != 1 || out.Revision != 3 {
		t.Fatalf("fallback outcome = %+v", out)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryContract.Mode != "direct-PR" {
		t.Fatalf("contract mode after fallback = %q, want the mode in force", agg.DeliveryContract.Mode)
	}
	fb := agg.DeliveryContract.Fallback
	if fb == nil {
		t.Fatal("fallback transition not recorded")
	}
	if fb.From != "no-mistakes" || fb.To != "direct-PR" || fb.Reason != req.Reason {
		t.Fatalf("fallback = %+v", *fb)
	}
	if fb.Generation != 1 || fb.OperationID != "op-fallback-1" || fb.RecordedAt <= 0 {
		t.Fatalf("fallback provenance = %+v", *fb)
	}
	// The recording operation stays distinct from the contract's own.
	if agg.DeliveryContract.OperationID != "op-contract-1" {
		t.Fatalf("fallback rewrote the contract's recording operation: %+v", *agg.DeliveryContract)
	}
}

// TestCanonicalRecordDeliveryFallbackRefusesWithoutContract builds the
// refused state: a task that contracts nothing has no mode-in-force to
// transition away from.
func TestCanonicalRecordDeliveryFallbackRefusesWithoutContract(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := fallbackRequest(c, "t1", preconditionOf(1, 1), "no-mistakes", "direct-PR")
	_, err := c.RecordDeliveryFallback(mustOperation(t, "op-fallback-none", req), req)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("fallback on an uncontracted task = %v, want ErrInvalidInput", err)
	}
	agg, getErr := c.Get(mustTaskID(t, "t1"))
	if getErr != nil {
		t.Fatal(getErr)
	}
	if agg.DeliveryContract != nil {
		t.Fatalf("refused fallback contracted the task: %+v", *agg.DeliveryContract)
	}
}

// TestCanonicalRecordDeliveryFallbackRefusesFromMismatch builds the refused
// state the op exists to prevent: a transition whose from-mode is not the
// mode the contract actually states. Recording it would overwrite a contract
// the caller never read.
func TestCanonicalRecordDeliveryFallbackRefusesFromMismatch(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "local-only")

	req := fallbackRequest(c, "t1", preconditionOf(1, 2), "no-mistakes", "direct-PR")
	_, err := c.RecordDeliveryFallback(mustOperation(t, "op-fallback-mismatch", req), req)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("from-mismatch = %v, want ErrConflict", err)
	}
	agg, getErr := c.Get(mustTaskID(t, "t1"))
	if getErr != nil {
		t.Fatal(getErr)
	}
	if agg.DeliveryContract.Mode != "local-only" || agg.DeliveryContract.Fallback != nil {
		t.Fatalf("refused fallback mutated the contract: %+v", *agg.DeliveryContract)
	}
}

// TestCanonicalRecordDeliveryFallbackRejectsInvalidTransition keeps an
// unenforceable or empty transition out of the durable record.
func TestCanonicalRecordDeliveryFallbackRejectsInvalidTransition(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "no-mistakes")

	cases := []struct {
		name string
		mut  func(*CanonicalRecordDeliveryFallbackRequest)
	}{
		{"unknown-to-mode", func(r *CanonicalRecordDeliveryFallbackRequest) { r.To = "yolo" }},
		{"empty-to-mode", func(r *CanonicalRecordDeliveryFallbackRequest) { r.To = "" }},
		{"empty-from-mode", func(r *CanonicalRecordDeliveryFallbackRequest) { r.From = "" }},
		{"from-equals-to", func(r *CanonicalRecordDeliveryFallbackRequest) { r.To = r.From }},
		{"no-reason", func(r *CanonicalRecordDeliveryFallbackRequest) { r.Reason = "" }},
		{"unauthorized-to-mode", func(r *CanonicalRecordDeliveryFallbackRequest) { r.To = "local-only" }},
	}
	for _, tc := range cases {
		req := fallbackRequest(c, "t1", preconditionOf(1, 2), "no-mistakes", "direct-PR")
		tc.mut(&req)
		_, err := c.RecordDeliveryFallback(mustOperation(t, "op-fallback-bad-"+tc.name, req), req)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s = %v, want ErrInvalidInput", tc.name, err)
		}
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryContract.Mode != "no-mistakes" || agg.DeliveryContract.Fallback != nil {
		t.Fatalf("a refused transition reached the record: %+v", *agg.DeliveryContract)
	}
}

// TestCanonicalRecordDeliveryFallbackRefusesUnauthorizedDirection builds a
// transition every mode check accepts — two known modes, a real transition,
// a From matching the contract in force — and refuses it anyway, because
// ADR-0022 Decision #2 authorizes exactly one direction: "the authorized
// no-mistakes -> direct-PR downgrade". A downgrade the operator policy never
// sanctioned must not become durable task truth.
func TestCanonicalRecordDeliveryFallbackRefusesUnauthorizedDirection(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "direct-PR")

	req := fallbackRequest(c, "t1", preconditionOf(1, 2), "direct-PR", "local-only")
	_, err := c.RecordDeliveryFallback(mustOperation(t, "op-fallback-unauthorized", req), req)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("RecordDeliveryFallback(direct-PR -> local-only) = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "is not the authorized") {
		t.Fatalf("refusal does not name the authorized direction: %v", err)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryContract.Mode != "direct-PR" || agg.DeliveryContract.Fallback != nil {
		t.Fatalf("an unauthorized transition reached the record: %+v", *agg.DeliveryContract)
	}
}

// TestCanonicalRecordDeliveryFallbackSameTransitionIsNoOp keeps a re-entrant
// spawn from double-recording a transition the contract already carries.
func TestCanonicalRecordDeliveryFallbackSameTransitionIsNoOp(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "no-mistakes")
	req := fallbackRequest(c, "t1", preconditionOf(1, 2), "no-mistakes", "direct-PR")
	if _, err := c.RecordDeliveryFallback(mustOperation(t, "op-fallback-1", req), req); err != nil {
		t.Fatalf("RecordDeliveryFallback: %v", err)
	}

	// A DIFFERENT operation carrying the same transition against the moved
	// contract: the from-mode no longer matches the mode in force, so only
	// the same-transition no-op can accept it.
	again := fallbackRequest(c, "t1", preconditionOf(1, 3), "no-mistakes", "direct-PR")
	out, err := c.RecordDeliveryFallback(mustOperation(t, "op-fallback-2", again), again)
	if err != nil {
		t.Fatalf("re-entrant fallback: %v", err)
	}
	if out.Revision != 3 {
		t.Fatalf("re-entry bumped the revision: %+v", out)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryContract.Fallback.OperationID != "op-fallback-1" {
		t.Fatalf("re-entry rewrote the committed transition: %+v", *agg.DeliveryContract.Fallback)
	}
}

// TestCanonicalRecordDeliveryFallbackReplaysByOperationID pins idempotent
// replay: the same Operation ID and digest returns the durable prior outcome.
func TestCanonicalRecordDeliveryFallbackReplaysByOperationID(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "no-mistakes")
	req := fallbackRequest(c, "t1", preconditionOf(1, 2), "no-mistakes", "direct-PR")
	first, err := c.RecordDeliveryFallback(mustOperation(t, "op-fallback-1", req), req)
	if err != nil {
		t.Fatalf("RecordDeliveryFallback: %v", err)
	}
	replay, err := c.RecordDeliveryFallback(mustOperation(t, "op-fallback-1", req), req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Revision != first.Revision || replay.Generation != first.Generation {
		t.Fatalf("replay = %+v, first = %+v", replay, first)
	}
}

// TestCanonicalDeliveryFallbackSurvivesReopen pins the transition as per-TASK
// like the contract itself: the next generation inherits the transitioned
// mode AND its provenance, by copy and never by alias, so a later spawn reads
// the fallen-back mode instead of falling back again.
func TestCanonicalDeliveryFallbackSurvivesReopen(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "no-mistakes")
	req := fallbackRequest(c, "t1", preconditionOf(1, 2), "no-mistakes", "direct-PR")
	if _, err := c.RecordDeliveryFallback(mustOperation(t, "op-fallback-1", req), req); err != nil {
		t.Fatalf("RecordDeliveryFallback: %v", err)
	}

	complete := CanonicalCompleteRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 3),
		To:           PhaseDone,
		Reason:       "done",
	}
	if _, err := c.Complete(mustOperation(t, "op-complete-f", complete), complete); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	reopen := CanonicalReopenRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 4),
		Reason:       "reopen",
	}
	if _, err := c.Reopen(mustOperation(t, "op-reopen-f", reopen), reopen); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Generation != 2 {
		t.Fatalf("generation = %d", agg.Generation)
	}
	if agg.DeliveryContract.Mode != "direct-PR" {
		t.Fatalf("reopened generation lost the mode in force: %+v", *agg.DeliveryContract)
	}
	if agg.DeliveryContract.Fallback == nil || agg.DeliveryContract.Fallback.Generation != 1 {
		t.Fatalf("reopened generation lost the transition provenance: %+v", agg.DeliveryContract.Fallback)
	}
	hist, err := c.GetGeneration(mustTaskID(t, "t1"), 1)
	if err != nil {
		t.Fatalf("GetGeneration(1): %v", err)
	}
	if hist.DeliveryContract.Fallback == agg.DeliveryContract.Fallback {
		t.Fatal("reopened generation aliases the prior generation's fallback pointer")
	}
}

// TestCanonicalReScaffoldClearsRecordedFallback pins the re-scaffold as a
// fresh contract, not an edit of the old one: an explicitly re-selected mode
// carries no inherited transition provenance.
func TestCanonicalReScaffoldClearsRecordedFallback(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "no-mistakes")
	req := fallbackRequest(c, "t1", preconditionOf(1, 2), "no-mistakes", "direct-PR")
	if _, err := c.RecordDeliveryFallback(mustOperation(t, "op-fallback-1", req), req); err != nil {
		t.Fatalf("RecordDeliveryFallback: %v", err)
	}

	rescaffold := contractRequest(c, "t1", preconditionOf(1, 3), "local-only")
	rescaffold.Rescaffold = true
	if _, err := c.RecordDeliveryContract(mustOperation(t, "op-contract-rescaffold", rescaffold), rescaffold); err != nil {
		t.Fatalf("re-scaffold: %v", err)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryContract.Mode != "local-only" {
		t.Fatalf("re-scaffold did not replace the contract: %+v", *agg.DeliveryContract)
	}
	if agg.DeliveryContract.Fallback != nil {
		t.Fatalf("re-scaffolded contract inherited a transition: %+v", *agg.DeliveryContract.Fallback)
	}
}
