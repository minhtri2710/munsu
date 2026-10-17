package taskauthority

import (
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

// The refusal branches of the canonical operations themselves — the ones the
// record validators next door (model_guards_test.go, canonical_delivery_guards_
// test.go) do not reach, because they only fire against committed state.
//
// Two rules hold for every case here.
//
// Each asserts the message of the branch under test, never `err != nil`. These
// operations refuse a dozen ways with one error type, and several guards in the
// same function emit the same sentinel: `err != nil` would be green with the
// guard deleted, because a guard further down would refuse anyway. That is the
// shape to avoid.
//
// And each builds the refused state from an accepted one, so the refusal is
// attributable to the single thing it changed. Where the setup is more than one
// call, the test asserts the accepted state first (a control), so a fixture that
// silently stopped being accepted shows up as a failed control rather than a
// passing test.

// wantErrSubstring fails unless err carries the exact refusal message of the
// branch under test.
func wantErrSubstring(t *testing.T, err error, sub, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s was accepted, want the %q refusal", what, sub)
	}
	if !strings.Contains(err.Error(), sub) {
		t.Fatalf("%s = %v, want the %q refusal", what, err, sub)
	}
}

// opIDFor turns a case name into an operation identity. Operation IDs are typed
// identities and reject whitespace, so the prose case names below cannot be
// used verbatim.
func opIDFor(prefix, name string) string {
	return prefix + "-" + strings.ReplaceAll(name, " ", "-")
}

// refusalRow is one side of a policy refusal. run builds the state on a fresh
// canonical and returns the error of the operation under test. A blocking row
// names the sentinel class and the message of the guard it pins; a row with no
// message is the nearest valid input and must be accepted. Pairing a blocking
// row with its allowing row is what makes a guard widened or deleted show up as
// a red row on one side or the other.
type refusalRow struct {
	name  string
	class error
	want  string
	run   func(t *testing.T, c *Canonical) error
}

// runRefusalRows runs every row on its own fresh canonical.
func runRefusalRows(t *testing.T, rows []refusalRow) {
	t.Helper()
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			c, _, _ := newTestCanonical(t)
			err := row.run(t, c)
			if row.want == "" {
				if err != nil {
					t.Fatalf("%s = %v, want it accepted", row.name, err)
				}
				return
			}
			if !errors.Is(err, row.class) {
				t.Fatalf("%s = %v, want class %v", row.name, err, row.class)
			}
			wantErrSubstring(t, err, row.want, row.name)
		})
	}
}

// Lifecycle steps for the refusal tables. Each runs against t1 (unless told
// otherwise) at the given revision and returns the operation's error; the
// operation identity is derived from the step and revision, which is unique
// within one row's fresh canonical.
func stepStart(t *testing.T, c *Canonical, taskID string, rev uint64) error {
	t.Helper()
	req := startWithRev(c, taskID, rev)
	_, err := c.Start(mustOperation(t, fmt.Sprintf("op-start-%s-%d", taskID, rev), req), req)
	return err
}

func stepBlock(t *testing.T, c *Canonical, taskID string, rev uint64) error {
	t.Helper()
	req := CanonicalBlockRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: preconditionOf(1, rev), Detail: "waiting", Reason: "block"}
	_, err := c.Block(mustOperation(t, fmt.Sprintf("op-block-%s-%d", taskID, rev), req), req)
	return err
}

func stepUnblock(t *testing.T, c *Canonical, taskID string, rev uint64) error {
	t.Helper()
	req := CanonicalUnblockRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: preconditionOf(1, rev), Reason: "unblock"}
	_, err := c.Unblock(mustOperation(t, fmt.Sprintf("op-unblock-%s-%d", taskID, rev), req), req)
	return err
}

func stepComplete(t *testing.T, c *Canonical, taskID string, rev uint64, to Phase) error {
	t.Helper()
	req := CanonicalCompleteRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: preconditionOf(1, rev), To: to, Reason: "complete"}
	_, err := c.Complete(mustOperation(t, fmt.Sprintf("op-complete-%s-%d", taskID, rev), req), req)
	return err
}

func stepReopen(t *testing.T, c *Canonical, taskID string, rev uint64) error {
	t.Helper()
	req := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: preconditionOf(1, rev), Reason: "reopen"}
	_, err := c.Reopen(mustOperation(t, fmt.Sprintf("op-reopen-%s-%d", taskID, rev), req), req)
	return err
}

// mustStep fails the test when a setup step of a row is refused, so a fixture
// that stopped being accepted is a failed setup and not a passing refusal.
func mustStep(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("setup step: %v", err)
	}
}

// --- dispatch.go: the durable hold record --------------------------------

// validHoldRecord is a dispatch hold validateHold accepts.
func validHoldRecord() DispatchHold {
	return DispatchHold{
		SchemaVersion: TaskAuthoritySchema,
		ID:            "hold-1",
		Scope:         DispatchHoldScope{TaskIDs: []string{"t1"}},
		Actions:       []DispatchAction{DispatchActionStart},
		Reason:        "waiting on review",
		CreatedAt:     1700000000,
	}
}

// A hold is a durable control that blocks dispatch. A record whose schema,
// identity, action set, or reason is unusable would block or fail to block
// without anyone being able to say why, so it is refused at the boundary.
func TestGuardValidateHoldRefusesUnusableRecords(t *testing.T) {
	runGuardCases(t, validHoldRecord, validateHold, []guardCase[DispatchHold]{
		{"another schema version", func(h *DispatchHold) { h.SchemaVersion = "munsu.task-authority/v2" }, "invalid dispatch hold schema"},
		{"no id", func(h *DispatchHold) { h.ID = "" }, "dispatch hold ID must be a safe non-empty value"},
		{"a path-separating id", func(h *DispatchHold) { h.ID = "holds/hold-1" }, "dispatch hold ID must be a safe non-empty value"},
		{"no actions", func(h *DispatchHold) { h.Actions = nil }, "dispatch hold requires at least one action"},
		{"a blank reason", func(h *DispatchHold) { h.Reason = "   " }, "dispatch hold requires a reason"},
	})
}

// --- canonical.go: operation identity ------------------------------------

// The Authority is bound to one opened home. Without a home there is no
// identity to bind operations to, so construction fails rather than yielding an
// Authority that would panic on first use.
func TestGuardNewCanonicalRefusesNilHome(t *testing.T) {
	// Control: the same constructor over a real home succeeds.
	if _, _, _ = newTestCanonical(t); true {
	}
	_, err := NewCanonical(nil)
	wantErrSubstring(t, err, "nil home", "NewCanonical(nil)")
}

// The Operation digest is what makes replay safe: a receipt replays only when
// the digest matches. An operation whose digest was computed over a different
// intent than the one submitted would let a changed request replay as the
// original, so prepare recomputes the digest and refuses the mismatch.
func TestGuardPrepareRefusesDigestThatDoesNotDeriveFromTheIntent(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	submitted := CanonicalStartRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 1),
		Reason:       "start",
	}
	// Control: the operation built over the submitted intent is accepted.
	if _, err := c.Start(mustOperation(t, "op-start-control", submitted), submitted); err != nil {
		t.Fatalf("Start with a matching digest: %v", err)
	}

	other := submitted
	other.Reason = "a different intent"
	op := mustOperation(t, "op-start-mismatch", other)
	_, err := c.Start(op, submitted)
	wantErrSubstring(t, err, "digest does not match its typed intent", "Start with a foreign digest")
}

// --- canonical_ops.go: lifecycle -----------------------------------------

// Owner is what makes a task dispatchable and what every later authorization
// binds. A task created without one could never be spawned or delivered, so it
// is refused at creation rather than becoming an undispatchable record.
func TestGuardCreateRefusesTaskWithoutOwner(t *testing.T) {
	c, _, _ := newTestCanonical(t)

	req := createRequest(c, "t1")
	// Control: the same request with its owner is accepted.
	if _, err := c.Create(mustOperation(t, "op-create-control", req), req); err != nil {
		t.Fatalf("Create with an owner: %v", err)
	}

	blank := createRequest(c, "t2")
	blank.Owner = "   "
	_, err := c.Create(mustOperation(t, "op-create-blank", blank), blank)
	wantErrSubstring(t, err, "create requires an owner", "Create without an owner")
}

// The task lifecycle phase, kind and currentness rules. Every refusal is a pair:
// the row that blocks it and the row for the nearest valid input.
func TestTaskLifecycleRefusals(t *testing.T) {
	supersededSource := func(t *testing.T, c *Canonical) domain.Precondition {
		mustCreate(t, c, "t1")
		mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")
		commit := commitTransferRequest(t, c, "t1", preconditionOf(1, 2), "res-t1", "dest-home")
		if _, err := c.CommitTransfer(mustOperation(t, "op-commit-t1", commit), commit); err != nil {
			t.Fatalf("CommitTransfer: %v", err)
		}
		hist, err := c.GetGeneration(mustTaskID(t, "t1"), Generation(1))
		if err != nil {
			t.Fatal(err)
		}
		return preconditionOf(uint64(hist.Generation), uint64(hist.Revision))
	}
	promote := func(t *testing.T, c *Canonical, taskID string, prec domain.Precondition, mutate func(*CanonicalPromoteRequest)) error {
		req := promoteRequest(t, c, taskID, prec)
		mutate(&req)
		_, err := c.Promote(mustOperation(t, "op-promote-"+taskID, req), req)
		return err
	}
	noChange := func(*CanonicalPromoteRequest) {}
	// promoteKeepingScope is Promote's mutation without the scope clear.
	promoteKeepingScope := func(cur Aggregate) Aggregate {
		next := cur.clone()
		next.Definition.Kind = KindShip
		next.Revision++
		return next
	}
	promoteClearingScope := func(cur Aggregate) Aggregate {
		next := promoteKeepingScope(cur)
		next.Definition.ScoutScope = ""
		return next
	}
	// writeRefusedUnchanged runs a write and, when it refuses, proves the
	// stored document is still the one from before the write.
	writeRefusedUnchanged := func(t *testing.T, c *Canonical, write func() error) error {
		before, err := c.Get(mustTaskID(t, "scout-1"))
		mustStep(t, err)
		werr := write()
		if werr == nil {
			return nil
		}
		after, err := c.Get(mustTaskID(t, "scout-1"))
		if err != nil {
			t.Fatalf("stored document after the refused write: %v", err)
		}
		if after.Revision != before.Revision || after.Definition != before.Definition {
			t.Fatalf("refused write changed the stored document: %+v -> %+v", before, after)
		}
		return werr
	}
	mutateWith := func(t *testing.T, c *Canonical, apply func(Aggregate) Aggregate) error {
		prec := mustDoneScout(t, c, "scout-1")
		return writeRefusedUnchanged(t, c, func() error {
			_, err := c.mutateTask(mustOperation(t, "op-mutate-scout-1", promoteRequest(t, c, "scout-1", prec)), mustTaskID(t, "scout-1"), prec, func(cur Aggregate) (Aggregate, error) {
				return apply(cur), nil
			})
			return err
		})
	}
	mutateDeliveryWith := func(t *testing.T, c *Canonical, apply func(Aggregate) Aggregate) error {
		prec := mustDoneScout(t, c, "scout-1")
		return writeRefusedUnchanged(t, c, func() error {
			_, _, err := c.mutateDelivery(mustOperation(t, "op-mutate-delivery-scout-1", promoteRequest(t, c, "scout-1", prec)), mustTaskID(t, "scout-1"), prec, func(cur Aggregate, index DeliveryIndex) (Aggregate, DeliveryIndex, []home.ChangeItem, error) {
				return apply(cur), index, nil, nil
			})
			return err
		})
	}

	createScout := func(t *testing.T, c *Canonical, scope string) error {
		req := createRequest(c, "scout-x")
		req.Kind, req.ScoutScope = KindScout, scope
		_, err := c.Create(mustOperation(t, "op-create-scout-x", req), req)
		return err
	}

	runRefusalRows(t, []refusalRow{
		{name: "create refuses a scout with no scope before it takes the task lock", class: ErrInvalidInput, want: "scout task requires non-empty scope", run: func(t *testing.T, c *Canonical) error {
			err := createScout(t, c, "")
			locks, _ := filepath.Glob(filepath.Join(c.h.Root(), home.LockDirName, taskScope("scout-x")+".*"))
			if len(locks) != 0 {
				t.Fatalf("refused create left lock state: %v", locks)
			}
			return err
		}},
		{name: "create allows a scout with a scope", run: func(t *testing.T, c *Canonical) error {
			err := createScout(t, c, "map the repo")
			if locks, _ := filepath.Glob(filepath.Join(c.h.Root(), home.LockDirName, taskScope("scout-x")+".*")); len(locks) == 0 {
				t.Fatal("accepted create left no lock state; the refused row's absence check proves nothing")
			}
			return err
		}},
		{name: "a task write refuses an aggregate that breaks its kind contract", class: ErrInvalidInput, want: "scout-only fields are not valid for ship tasks", run: func(t *testing.T, c *Canonical) error {
			return mutateWith(t, c, promoteKeepingScope)
		}},
		{name: "a task write allows an aggregate that keeps its kind contract", run: func(t *testing.T, c *Canonical) error {
			return mutateWith(t, c, promoteClearingScope)
		}},
		{name: "a delivery write refuses an aggregate that breaks its kind contract", class: ErrInvalidInput, want: "scout-only fields are not valid for ship tasks", run: func(t *testing.T, c *Canonical) error {
			return mutateDeliveryWith(t, c, promoteKeepingScope)
		}},
		{name: "a delivery write allows an aggregate that keeps its kind contract", run: func(t *testing.T, c *Canonical) error {
			return mutateDeliveryWith(t, c, promoteClearingScope)
		}},
		{name: "start refuses a working task", class: ErrPrecondition, want: "start requires queued task", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			mustStep(t, stepStart(t, c, "t1", 1))
			return stepStart(t, c, "t1", 2)
		}},
		{name: "start allows a queued task", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			return stepStart(t, c, "t1", 1)
		}},

		{name: "block refuses a done task", class: ErrPrecondition, want: "block requires queued or working task", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			mustStep(t, stepComplete(t, c, "t1", 1, PhaseDone))
			return stepBlock(t, c, "t1", 2)
		}},
		{name: "block allows a working task", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			mustStep(t, stepStart(t, c, "t1", 1))
			return stepBlock(t, c, "t1", 2)
		}},

		{name: "unblock refuses a queued task", class: ErrPrecondition, want: "unblock requires blocked task", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			return stepUnblock(t, c, "t1", 1)
		}},
		{name: "unblock allows a blocked task", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			mustStep(t, stepBlock(t, c, "t1", 1))
			return stepUnblock(t, c, "t1", 2)
		}},

		{name: "complete refuses a terminal task", class: ErrPrecondition, want: "complete requires a non-terminal task", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			mustStep(t, stepComplete(t, c, "t1", 1, PhaseDone))
			return stepComplete(t, c, "t1", 2, PhaseResolved)
		}},
		{name: "complete refuses a resolved task", class: ErrPrecondition, want: "complete requires a non-terminal task", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			mustStep(t, stepComplete(t, c, "t1", 1, PhaseResolved))
			return stepComplete(t, c, "t1", 2, PhaseDone)
		}},
		{name: "complete allows a queued task", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			return stepComplete(t, c, "t1", 1, PhaseDone)
		}},
		{name: "complete refuses a target that is not terminal", class: ErrInvalidInput, want: "is not a terminal phase", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			return stepComplete(t, c, "t1", 1, PhaseQueued)
		}},
		{name: "complete allows a resolved target", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			return stepComplete(t, c, "t1", 1, PhaseResolved)
		}},

		{name: "reopen refuses a task that does not exist", class: ErrNotFound, want: "not found", run: func(t *testing.T, c *Canonical) error {
			return stepReopen(t, c, "never-created", 1)
		}},
		{name: "reopen refuses a live task", class: ErrPrecondition, want: "reopen requires terminal task", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			return stepReopen(t, c, "t1", 1)
		}},
		{name: "reopen allows a terminal task", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			mustStep(t, stepComplete(t, c, "t1", 1, PhaseDone))
			return stepReopen(t, c, "t1", 2)
		}},
		{name: "reopen allows a resolved task", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			mustStep(t, stepComplete(t, c, "t1", 1, PhaseResolved))
			return stepReopen(t, c, "t1", 2)
		}},
		{name: "reopen refuses a superseded generation", class: ErrConflict, want: "is not current; it is superseded and cannot be mutated", run: func(t *testing.T, c *Canonical) error {
			prec := supersededSource(t, c)
			req := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: prec, Reason: "reopen"}
			_, err := c.Reopen(mustOperation(t, "op-reopen-superseded", req), req)
			return err
		}},
		{name: "reopen refuses a task reserved for transfer", class: ErrConflict, want: "is reserved for transfer; unrelated mutations fail closed", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			mustStep(t, stepComplete(t, c, "t1", 1, PhaseDone))
			seedReservationForTest(t, c, "t1", preconditionOf(1, 2), "dest-home")
			return stepReopen(t, c, "t1", 3)
		}},

		{name: "promote refuses a current kind that is not scout", class: ErrInvalidInput, want: "promotion requires scout -> ship kind promotion", run: func(t *testing.T, c *Canonical) error {
			return promote(t, c, "t1", mustDoneScout(t, c, "t1"), func(r *CanonicalPromoteRequest) { r.CurrentKind = "ship" })
		}},
		{name: "promote refuses a target kind that is not ship", class: ErrInvalidInput, want: "promotion requires scout -> ship kind promotion", run: func(t *testing.T, c *Canonical) error {
			return promote(t, c, "t1", mustDoneScout(t, c, "t1"), func(r *CanonicalPromoteRequest) { r.TargetKind = "scout" })
		}},
		{name: "promote allows a done scout", run: func(t *testing.T, c *Canonical) error {
			return promote(t, c, "t1", mustDoneScout(t, c, "t1"), noChange)
		}},
		{name: "promote refuses a done ship task", class: ErrPrecondition, want: "only scout tasks promote to ship", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			mustStep(t, stepComplete(t, c, "t1", 1, PhaseDone))
			return promote(t, c, "t1", preconditionOf(1, 2), noChange)
		}},
		{name: "promote refuses a live scout", class: ErrPrecondition, want: "promotion requires a done or resolved scout", run: func(t *testing.T, c *Canonical) error {
			create := scoutCreateRequest(c, "t1")
			_, err := c.Create(mustOperation(t, "op-create-t1", create), create)
			mustStep(t, err)
			return promote(t, c, "t1", preconditionOf(1, 1), noChange)
		}},
		{name: "promote allows a resolved scout", run: func(t *testing.T, c *Canonical) error {
			create := scoutCreateRequest(c, "t1")
			_, err := c.Create(mustOperation(t, "op-create-t1", create), create)
			mustStep(t, err)
			mustStep(t, stepComplete(t, c, "t1", 1, PhaseResolved))
			return promote(t, c, "t1", preconditionOf(1, 2), noChange)
		}},

		{name: "a superseded generation refuses a mutation", class: ErrConflict, want: "is not current; it is superseded and cannot be mutated", run: func(t *testing.T, c *Canonical) error {
			prec := supersededSource(t, c)
			req := CanonicalStartRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: prec, Reason: "start"}
			_, err := c.Start(mustOperation(t, "op-start-superseded", req), req)
			return err
		}},
		{name: "the current generation allows the same mutation", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			mustStep(t, stepComplete(t, c, "t1", 1, PhaseDone))
			mustStep(t, stepReopen(t, c, "t1", 2))
			req := CanonicalStartRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(2, 1), Reason: "start"}
			_, err := c.Start(mustOperation(t, "op-start-gen2", req), req)
			return err
		}},
	})
}

// GetGeneration is the audit read of one exact generation. A generation the
// task never had must fail closed rather than fall back to the current one —
// callers use it to prove what a superseded generation held.
func TestGuardGetGenerationRefusesUnknownGeneration(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// Control: generation 1 is readable, so the refusal below is the missing
	// generation and not a missing task.
	if _, err := c.GetGeneration(mustTaskID(t, "t1"), Generation(1)); err != nil {
		t.Fatalf("GetGeneration(1): %v", err)
	}
	_, err := c.GetGeneration(mustTaskID(t, "t1"), Generation(7))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetGeneration(7) = %v, want ErrNotFound", err)
	}
	wantErrSubstring(t, err, "generation 7 not found", "GetGeneration of an unheld generation")
}

func addHoldRequest(c *Canonical, holdID string) CanonicalAddHoldRequest {
	return CanonicalAddHoldRequest{
		HomeID:  c.HomeID(),
		HoldID:  holdID,
		Scope:   DispatchHoldScope{TaskIDs: []string{"t1"}},
		Actions: []DispatchAction{DispatchActionStart},
		Reason:  "waiting on review",
	}
}

// The hold ID becomes a path segment and a lock scope, and the actions and
// reason are what make the hold a decision record rather than an opaque flag.
func TestGuardAddHoldRefusesUnusableRequests(t *testing.T) {
	c, _, _ := newTestCanonical(t)

	// Control: the same request with a safe ID, one action, and a reason is
	// accepted, so every refusal below is the one field it changed.
	ok := addHoldRequest(c, "hold-ok")
	if _, err := c.AddHold(mustOperation(t, "op-hold-ok", ok), ok); err != nil {
		t.Fatalf("AddHold with a safe request: %v", err)
	}

	for _, tc := range []struct {
		name    string
		mutate  func(*CanonicalAddHoldRequest)
		wantSub string
	}{
		{"an empty hold id", func(r *CanonicalAddHoldRequest) { r.HoldID = "" }, "dispatch hold ID must be a safe non-empty value"},
		{"a path-separating hold id", func(r *CanonicalAddHoldRequest) { r.HoldID = "holds/h1" }, "dispatch hold ID must be a safe non-empty value"},
		{"a dotted hold id", func(r *CanonicalAddHoldRequest) { r.HoldID = "h1.json" }, "dispatch hold ID must be a safe non-empty value"},
		{"no actions", func(r *CanonicalAddHoldRequest) { r.Actions = nil }, "dispatch hold requires actions and reason"},
		{"a blank reason", func(r *CanonicalAddHoldRequest) { r.Reason = "  " }, "dispatch hold requires actions and reason"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := addHoldRequest(c, "hold-case")
			tc.mutate(&req)
			_, err := c.AddHold(mustOperation(t, opIDFor("op-hold", tc.name), req), req)
			wantErrSubstring(t, err, tc.wantSub, "AddHold with "+tc.name)
		})
	}
}

// A released hold is a closed decision record. Re-adding its ID would revive
// the block under an identity whose release is already committed history.
func TestGuardAddHoldRefusesToReviveAReleasedHold(t *testing.T) {
	c, _, _ := newTestCanonical(t)

	add := addHoldRequest(c, "hold-1")
	if _, err := c.AddHold(mustOperation(t, "op-hold-add", add), add); err != nil {
		t.Fatalf("AddHold: %v", err)
	}
	// Control: re-adding the identical active hold is a successful no-op, so
	// the refusal below comes from the release and not from the re-add.
	if _, err := c.AddHold(mustOperation(t, "op-hold-readd", add), add); err != nil {
		t.Fatalf("AddHold of an identical active hold: %v", err)
	}

	release := CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "hold-1", Reason: "resolved", Words: testWords()}
	if _, err := c.ReleaseHold(mustOperation(t, "op-hold-release", release), release); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	_, err := c.AddHold(mustOperation(t, "op-hold-revive", add), add)
	wantErrSubstring(t, err, "is already released", "AddHold of a released hold")
}

// ReleaseHold builds the same path and lock scope from the ID, so it applies
// the same safety rule as AddHold rather than trusting the caller's string.
func TestGuardReleaseHoldRefusesUnsafeHoldID(t *testing.T) {
	c, _, _ := newTestCanonical(t)

	add := addHoldRequest(c, "hold-1")
	if _, err := c.AddHold(mustOperation(t, "op-relhold-add", add), add); err != nil {
		t.Fatalf("AddHold: %v", err)
	}
	// Control: the safe ID releases.
	ok := CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "hold-1", Reason: "resolved", Words: testWords()}
	if _, err := c.ReleaseHold(mustOperation(t, "op-relhold-ok", ok), ok); err != nil {
		t.Fatalf("ReleaseHold with a safe ID: %v", err)
	}

	unsafe := CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "holds/hold-1", Reason: "resolved", Words: testWords()}
	_, err := c.ReleaseHold(mustOperation(t, "op-relhold-unsafe", unsafe), unsafe)
	wantErrSubstring(t, err, "dispatch hold ID must be a safe non-empty value", "ReleaseHold with a path-separating ID")
}

// --- canonical_launch.go: the launch chain -------------------------------

// launchToBindable drives one task through the launch chain up to the final
// bind — intent, worktree, manifest anchor, acquired endpoint "handle-<taskID>",
// launch evidence — and returns the committed intent and the revision the bind
// must carry.
func launchToBindable(t *testing.T, c *Canonical, taskID string) (CanonicalBeginSpawnRequest, uint64) {
	t.Helper()
	mustCreate(t, c, taskID)
	intent, rev := mustBeginSpawn(t, c, taskID, preconditionOf(1, 1))

	bw := CanonicalBindWorktreeRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, taskID),
		Precondition: preconditionOf(1, rev), Binding: launchWorktreeBinding(intent), Reason: "bind worktree",
	}
	if _, err := c.BindWorktree(mustOperation(t, "op-l2w-bindwt-"+taskID, bw), bw); err != nil {
		t.Fatalf("BindWorktree(%s): %v", taskID, err)
	}
	rev++
	mustRecordLaunchManifest(t, c, taskID, intent, bw.Binding, rev)
	rev++

	attach := attachRequest(c, taskID, preconditionOf(1, rev), intent, "handle-"+taskID)
	if _, err := c.AttachEndpoint(mustOperation(t, "op-l2w-attach-"+taskID, attach), attach); err != nil {
		t.Fatalf("AttachEndpoint(%s): %v", taskID, err)
	}
	rev++

	record := recordLaunchRequest(c, taskID, preconditionOf(1, rev), intent)
	if _, err := c.RecordLaunch(mustOperation(t, "op-l2w-record-"+taskID, record), record); err != nil {
		t.Fatalf("RecordLaunch(%s): %v", taskID, err)
	}
	return intent, rev + 1
}

// launchToWorking runs launchToBindable and the final bind, leaving the task
// working. It returns the committed intent and the working revision.
func launchToWorking(t *testing.T, c *Canonical, taskID string) (CanonicalBeginSpawnRequest, uint64) {
	t.Helper()
	intent, rev := launchToBindable(t, c, taskID)
	be := bindEndpointRequest(c, taskID, preconditionOf(1, rev))
	if _, err := c.BindEndpoint(mustOperation(t, "op-l2w-bindep-"+taskID, be), be); err != nil {
		t.Fatalf("BindEndpoint(%s): %v", taskID, err)
	}
	rev++

	agg, err := c.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseWorking || uint64(agg.Revision) != rev {
		t.Fatalf("launch chain left task %s at %s revision %d, want working revision %d", taskID, agg.Phase, agg.Revision, rev)
	}
	return intent, rev
}

// The acquired endpoint is what the later bind is matched against, so a
// request that cannot name the endpoint it acquired is refused before it can
// be recorded as evidence of one.
func TestGuardAttachEndpointRefusesIncompleteEndpointIdentity(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	intent, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))

	valid := func() CanonicalAttachEndpointRequest {
		return attachRequest(c, "t1", preconditionOf(1, rev), intent, "handle-1")
	}
	// Control: the complete request is accepted.
	ok := valid()
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-control", ok), ok); err != nil {
		t.Fatalf("AttachEndpoint with a complete identity: %v", err)
	}

	for _, tc := range []struct {
		name    string
		mutate  func(*CanonicalAttachEndpointRequest)
		wantSub string
	}{
		{"no backend", func(r *CanonicalAttachEndpointRequest) { r.Backend = "  " }, "acquired endpoint requires backend"},
		{"no handle", func(r *CanonicalAttachEndpointRequest) { r.Handle = "" }, "acquired endpoint requires handle"},
		{"no lease id", func(r *CanonicalAttachEndpointRequest) { r.LeaseID = "" }, "acquired endpoint requires lease id"},
		{"no fence token", func(r *CanonicalAttachEndpointRequest) { r.FenceToken = "" }, "acquired endpoint requires fence token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := valid()
			tc.mutate(&req)
			_, err := c.AttachEndpoint(mustOperation(t, opIDFor("op-attach", tc.name), req), req)
			wantErrSubstring(t, err, tc.wantSub, "AttachEndpoint with "+tc.name)
		})
	}
}

// The incarnation is the provenance token minted for this exact launch
// operation. An acquired endpoint recorded without one could not be told apart
// from a stale endpoint of an earlier launch of the same task.
func TestGuardAttachEndpointRequiresTheLaunchIncarnation(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	intent, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))

	req := attachRequest(c, "t1", preconditionOf(1, rev), intent, "handle-1")
	req.Incarnation = ""
	_, err := c.AttachEndpoint(mustOperation(t, "op-attach-no-inc", req), req)
	wantErrSubstring(t, err, "requires the launch incarnation token", "AttachEndpoint without an incarnation")
}

// AttachEndpoint records a PRE-bind acquisition. Once the task is working the
// endpoint is bound and the acquisition record is settled: a late attach would
// rewrite the acquisition of a live soldier.
func TestGuardAttachEndpointRefusesWorkingTask(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	intent, rev := launchToWorking(t, c, "t1")

	req := attachRequest(c, "t1", preconditionOf(1, rev), intent, "handle-t1")
	_, err := c.AttachEndpoint(mustOperation(t, "op-attach-working", req), req)
	wantErrSubstring(t, err, "attach endpoint requires queued", "AttachEndpoint on a working task")
}

// Launch evidence is what proves a submission happened under the committed
// intent. Evidence whose launch identity cannot be a path segment, or whose
// command digest is not a full sha256, cannot be that proof.
func TestGuardRecordLaunchRefusesUnusableEvidenceShape(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	intent, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))
	binding := launchWorktreeBinding(intent)
	bind := CanonicalBindWorktreeRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, rev), Binding: binding, Reason: "bind worktree"}
	if _, err := c.BindWorktree(mustOperation(t, "op-record-wt", bind), bind); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
	rev++
	mustRecordLaunchManifest(t, c, "t1", intent, binding, rev)
	rev++
	attach := attachRequest(c, "t1", preconditionOf(1, rev), intent, "handle-1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-record-attach", attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}
	rev++

	valid := func() CanonicalRecordLaunchRequest {
		return recordLaunchRequest(c, "t1", preconditionOf(1, rev), intent)
	}
	for _, tc := range []struct {
		name    string
		mutate  func(*CanonicalRecordLaunchRequest)
		wantSub string
	}{
		{"no launch id", func(r *CanonicalRecordLaunchRequest) { r.LaunchID = "" }, "requires a deterministic launch identity"},
		{"a path-separating launch id", func(r *CanonicalRecordLaunchRequest) { r.LaunchID = "launches/l1" }, "requires a deterministic launch identity"},
		{"a truncated command digest", func(r *CanonicalRecordLaunchRequest) { r.CommandDigest = "abc123" }, "command digest must be a 64-hex sha256 digest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := valid()
			tc.mutate(&req)
			_, err := c.RecordLaunch(mustOperation(t, opIDFor("op-record", tc.name), req), req)
			wantErrSubstring(t, err, tc.wantSub, "RecordLaunch with "+tc.name)
		})
	}

	// Control: the untouched request is accepted, so each refusal above is the
	// field it broke and not the surrounding state.
	ok := valid()
	if _, err := c.RecordLaunch(mustOperation(t, "op-record-control", ok), ok); err != nil {
		t.Fatalf("RecordLaunch with a valid shape: %v", err)
	}
}

// Launch evidence belongs to a committed launch intent. Without one there is
// nothing for the evidence to be evidence OF, and the fleet runner's
// re-entrancy check would have no fence to compare against.
func TestGuardRecordLaunchRequiresACommittedIntent(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := CanonicalRecordLaunchRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 1), LaunchID: "launch-t1",
		CommandDigest: digestOf("launch:t1"), Seat: testSeat(), Reason: "record",
	}
	_, err := c.RecordLaunch(mustOperation(t, "op-record-no-intent", req), req)
	wantErrSubstring(t, err, "has no launch intent", "RecordLaunch without an intent")
}

// Launch evidence is recorded BEFORE the bind that starts the work. A record
// against a working task would claim a fresh submission for a soldier that is
// already running.
func TestGuardRecordLaunchRefusesWorkingTask(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	intent, rev := launchToWorking(t, c, "t1")

	req := recordLaunchRequest(c, "t1", preconditionOf(1, rev), intent)
	req.CommandDigest = digestOf("a second submission")
	_, err := c.RecordLaunch(mustOperation(t, "op-record-working", req), req)
	wantErrSubstring(t, err, "record launch requires queued", "RecordLaunch on a working task")
}

// --- canonical_retirement.go ---------------------------------------------

// Retirement preserves the resource ownership evidence of one generation. A
// second retirement would overwrite that evidence with the retired
// generation's already-cleared bindings, losing what cleanup must release.
func TestGuardRetireRefusesAnAlreadyRetiredGeneration(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	retireWithClaim(t, c, "t1", preconditionOf(1, 1), "op-retire-1")

	// The cleanup claim the retirement commits blocks every ordinary mutation
	// while it is active, so it is reconciled first: without this the second
	// Retire below would be refused by the claim fence and never reach the
	// already-retired guard.
	if err := c.ReconcileRetirementCleanup(mustTaskID(t, "t1"), 1, CleanupCompleted, func() error { return nil }); err != nil {
		t.Fatalf("ReconcileRetirementCleanup: %v", err)
	}

	req := retireRequest(t, c, "t1", preconditionOf(1, 3))
	_, err := c.Retire(mustOperation(t, "op-retire-2", req), req)
	wantErrSubstring(t, err, "is already retired", "Retire of a retired generation")
}

// --- canonical_transfer.go -----------------------------------------------

// The reservation ID is the identity both homes share and a path segment on
// each. A value that cannot be either would make the two sides unable to name
// the same reservation.
func TestGuardTransferRefusesUnsafeReservationID(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	for _, tc := range []struct {
		name          string
		reservationID string
	}{
		{"an empty reservation id", ""},
		{"a path-separating reservation id", "res/1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := reserveTransferRequest(t, c, "t1", preconditionOf(1, 1), tc.reservationID, "dest-home")
			_, err := c.ReserveTransfer(mustOperation(t, opIDFor("op-reserve", tc.name), req), req)
			wantErrSubstring(t, err, "transfer reservation ID must be a safe non-empty value", "ReserveTransfer with "+tc.name)
		})
	}

	// Control: a safe reservation ID reserves.
	ok := reserveTransferRequest(t, c, "t1", preconditionOf(1, 1), "res-ok", "dest-home")
	if _, err := c.ReserveTransfer(mustOperation(t, "op-reserve-ok", ok), ok); err != nil {
		t.Fatalf("ReserveTransfer with a safe reservation ID: %v", err)
	}
}

// The destination re-creates the generation from the typed definition, never
// from the source document. A definition with no owner would create an
// undispatchable task at the destination out of a dispatchable one.
func TestGuardReceiveTransferRequiresAnOwner(t *testing.T) {
	c, _, _ := newTestCanonical(t)

	req := receiveTransferRequest(t, c, "t1", "res-1", "source-home", 1)
	req.Definition.Owner = "   "
	_, err := c.ReceiveTransfer(mustOperation(t, "op-receive-no-owner", req), req)
	wantErrSubstring(t, err, "receive requires an owner", "ReceiveTransfer without an owner")

	// Control: the same receive with an owner is accepted.
	ok := receiveTransferRequest(t, c, "t1", "res-1", "source-home", 1)
	if _, err := c.ReceiveTransfer(mustOperation(t, "op-receive-owner", ok), ok); err != nil {
		t.Fatalf("ReceiveTransfer with an owner: %v", err)
	}
}

// Activation makes a RECEIVED generation current. A generation the destination
// never received has no document to activate, and activating on the strength of
// the caller's precondition alone would invent one.
func TestGuardActivateTransferRequiresAReceivedGeneration(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	receive := receiveTransferRequest(t, c, "t1", "res-1", "source-home", 1)
	if _, err := c.ReceiveTransfer(mustOperation(t, "op-receive-1", receive), receive); err != nil {
		t.Fatalf("ReceiveTransfer: %v", err)
	}

	missing := CanonicalActivateTransferRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
		Precondition: preconditionOf(2, 1), ReservationID: "res-1", Reason: "activate",
	}
	_, err := c.ActivateTransfer(mustOperation(t, "op-activate-missing", missing), missing)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ActivateTransfer of an unreceived generation = %v, want ErrNotFound", err)
	}
	wantErrSubstring(t, err, "has no received generation 2", "ActivateTransfer of an unreceived generation")

	// Control: the generation that WAS received activates, so the refusal above
	// is the missing generation document and not the reservation or precondition.
	ok := CanonicalActivateTransferRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 1), ReservationID: "res-1", Reason: "activate",
	}
	if _, err := c.ActivateTransfer(mustOperation(t, "op-activate-1", ok), ok); err != nil {
		t.Fatalf("ActivateTransfer of the received generation: %v", err)
	}
}

// The source is superseded only against destination-activation evidence bound
// to the exact reservation being committed. Evidence that names another
// reservation, another task, another source home, or another source generation
// is proof of a different transfer, and would supersede this one on it.
func TestGuardCommitTransferRefusesUnboundActivationEvidence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*CanonicalCommitTransferRequest)
		wantSub string
	}{
		{
			"evidence for another reservation",
			func(r *CanonicalCommitTransferRequest) { r.Evidence.ReservationID = "res-other" },
			"evidence reservation does not match the commit reservation",
		},
		{
			"evidence for another task",
			func(r *CanonicalCommitTransferRequest) { r.Evidence.TaskID = "t-other" },
			"evidence task does not match the commit task",
		},
		{
			"evidence naming another source home",
			func(r *CanonicalCommitTransferRequest) { r.Evidence.SourceHome = "some-other-home" },
			"does not match this authority",
		},
		{
			"evidence naming another source generation",
			func(r *CanonicalCommitTransferRequest) { r.Evidence.SourceGeneration = Generation(2) },
			"does not match reserved generation",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := newTestCanonical(t)
			mustCreate(t, c, "t1")
			reservationID := mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")

			req := commitTransferRequest(t, c, "t1", preconditionOf(1, 2), reservationID, "dest-home")
			tc.mutate(&req)
			_, err := c.CommitTransfer(mustOperation(t, opIDFor("op-commit", tc.name), req), req)
			wantErrSubstring(t, err, tc.wantSub, "CommitTransfer with "+tc.name)

			// Control: the untouched evidence for the same reservation commits,
			// so each refusal above is the one field it rebound.
			ok := commitTransferRequest(t, c, "t1", preconditionOf(1, 2), reservationID, "dest-home")
			if _, err := c.CommitTransfer(mustOperation(t, "op-commit-control", ok), ok); err != nil {
				t.Fatalf("CommitTransfer with bound evidence: %v", err)
			}
		})
	}
}

// --- canonical_delivery.go: evidence resolution --------------------------

// deleteDeliveryEvidence removes one committed immutable evidence document,
// leaving the bounded index pointing at it. That is the shape a partially lost
// state directory has on disk, and every read that resolves the pointer must
// fail closed on it rather than treat absence as "no such evidence".
func deleteDeliveryEvidence(t *testing.T, c *Canonical, key string) {
	t.Helper()
	if err := os.Remove(mustPathForTest(t, c.h, key)); err != nil {
		t.Fatalf("remove evidence %s: %v", key, err)
	}
}

// A delivery mutation is fenced to the task's exact generation and revision. A
// task that does not exist has neither, so the mutation fails closed instead of
// authorizing delivery against a task no home owns.
func TestGuardDeliveryMutationRefusesMissingTask(t *testing.T) {
	c, _, _ := newTestCanonical(t)

	req := authorizeRequest(c, "never-created", preconditionOf(1, 1))
	_, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-missing", req), req)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("AuthorizeDelivery on a missing task = %v, want ErrNotFound", err)
	}
	wantErrSubstring(t, err, "not found", "AuthorizeDelivery on a missing task")
}

// Replay reconstructs the original result from the immutable evidence, never
// from a fresh computation. With the evidence gone there is nothing to
// reconstruct, and a replay that returned a zero record would report an
// authorization that no longer exists as though it did.
func TestGuardDeliveryReplayRefusesWhenItsEvidenceIsGone(t *testing.T) {
	t.Run("a missing authorization", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		rev := mustDeliveryTask(t, c, "t1")
		mustAuthorize(t, c, "t1", rev, "op-auth-1")

		req := authorizeRequest(c, "t1", preconditionOf(1, rev))
		op := mustOperation(t, "op-auth-1", req)
		// Control: the replay reconstructs the record while the evidence is there.
		if res, err := c.AuthorizeDelivery(op, req); err != nil || !res.Replayed {
			t.Fatalf("replay with evidence present = (%+v, %v), want a replayed record", res, err)
		}

		deleteDeliveryEvidence(t, c, deliveryAuthorizationKey("t1", "op-auth-1"))
		_, err := c.AuthorizeDelivery(op, req)
		wantErrSubstring(t, err, "replay of delivery authorization", "replay of a lost authorization")
	})

	t.Run("a missing revocation", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		rev := mustDeliveryTask(t, c, "t1")
		mustAuthorize(t, c, "t1", rev, "op-auth-1")
		mustRevoke(t, c, "t1", rev+1, "op-auth-1", "superseded", "op-revoke-1")

		req := CanonicalRevokeDeliveryRequest{
			HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
			Precondition: preconditionOf(1, rev+1), AuthorizationOperationID: "op-auth-1", Reason: "superseded",
		}
		op := mustOperation(t, "op-revoke-1", req)
		if res, err := c.RevokeDeliveryAuthorization(op, req); err != nil || !res.Replayed {
			t.Fatalf("replay with evidence present = (%+v, %v), want a replayed record", res, err)
		}

		deleteDeliveryEvidence(t, c, deliveryRevocationKey("t1", "op-revoke-1"))
		_, err := c.RevokeDeliveryAuthorization(op, req)
		wantErrSubstring(t, err, "replay of delivery revocation", "replay of a lost revocation")
	})

	t.Run("a missing outcome", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		rev := mustDeliveryTask(t, c, "t1")
		mustAuthorize(t, c, "t1", rev, "op-auth-1")
		mustCommitOutcome(t, c, "t1", rev+1, "op-auth-1", DeliveryOutcomeCompleted, "merged", "op-outcome-1")

		req := CanonicalDeliveryOutcomeRequest{
			HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
			Precondition: preconditionOf(1, rev+1), AuthorizationOperationID: "op-auth-1",
			Status: DeliveryOutcomeCompleted, Detail: "merged",
		}
		op := mustOperation(t, "op-outcome-1", req)
		if res, err := c.CommitDeliveryOutcome(op, req); err != nil || !res.Replayed {
			t.Fatalf("replay with evidence present = (%+v, %v), want a replayed record", res, err)
		}

		deleteDeliveryEvidence(t, c, deliveryOutcomeKey("t1", "op-outcome-1"))
		_, err := c.CommitDeliveryOutcome(op, req)
		wantErrSubstring(t, err, "replay of delivery outcome", "replay of a lost outcome")
	})
}

// The index pointer is not itself evidence: it names a document. When that
// document is gone the read fails closed rather than reporting the delivery as
// unrevoked, unauthorized, or without an outcome — each of which would be a
// permissive answer built from missing state.
func TestGuardDeliveryReadsRefuseIndexPointersWithNoDocument(t *testing.T) {
	t.Run("a revocation pointer with no document", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		rev := mustDeliveryTask(t, c, "t1")
		mustAuthorize(t, c, "t1", rev, "op-auth-1")
		mustRevoke(t, c, "t1", rev+1, "op-auth-1", "superseded", "op-revoke-1")

		// Control: with the revocation present, a fresh authorization succeeds —
		// the revoked authorization no longer blocks it.
		reissue := authorizeRequest(c, "t1", preconditionOf(1, rev+2))
		if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-control", reissue), reissue); err != nil {
			t.Fatalf("re-authorize after a recorded revocation: %v", err)
		}

		deleteDeliveryEvidence(t, c, deliveryRevocationKey("t1", "op-revoke-1"))
		again := authorizeRequest(c, "t1", preconditionOf(1, rev+3))
		_, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-lost-rev", again), again)
		wantErrSubstring(t, err, "points at missing revocation", "authorize with a lost revocation document")
	})

	t.Run("an authorization pointer with no document", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		rev := mustDeliveryTask(t, c, "t1")
		mustAuthorize(t, c, "t1", rev, "op-auth-1")

		deleteDeliveryEvidence(t, c, deliveryAuthorizationKey("t1", "op-auth-1"))
		req := CanonicalDeliveryOutcomeRequest{
			HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
			Precondition: preconditionOf(1, rev+1), AuthorizationOperationID: "op-auth-1",
			Status: DeliveryOutcomeCompleted, Detail: "merged",
		}
		_, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-lost-auth", req), req)
		wantErrSubstring(t, err, "points at missing authorization", "outcome against a lost authorization document")
	})

	t.Run("an outcome pointer with no document", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		rev := mustDeliveryTask(t, c, "t1")
		mustAuthorize(t, c, "t1", rev, "op-auth-1")
		mustCommitOutcome(t, c, "t1", rev+1, "op-auth-1", DeliveryOutcomeCompleted, "merged", "op-outcome-1")

		// Control: both reads resolve the pointer while the document is there.
		if _, err := c.DeliveryOutcome(mustTaskID(t, "t1")); err != nil {
			t.Fatalf("DeliveryOutcome with evidence present: %v", err)
		}
		if _, err := c.DeliveryCurrency(mustTaskID(t, "t1")); err != nil {
			t.Fatalf("DeliveryCurrency with evidence present: %v", err)
		}

		deleteDeliveryEvidence(t, c, deliveryOutcomeKey("t1", "op-outcome-1"))
		_, err := c.DeliveryOutcome(mustTaskID(t, "t1"))
		wantErrSubstring(t, err, "points at missing outcome", "DeliveryOutcome with a lost outcome document")
		_, err = c.DeliveryCurrency(mustTaskID(t, "t1"))
		wantErrSubstring(t, err, "points at missing outcome", "DeliveryCurrency with a lost outcome document")
	})
}

// Evidence is keyed by task AND operation identity, so a document that names a
// different task under this task's key is a substitution, not a record of this
// task's delivery. Validation alone would accept it: every field is well
// formed.
func TestGuardDeliveryReadsRefuseEvidenceBoundToAnotherTask(t *testing.T) {
	t.Run("a revocation bound to another task", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		rev := mustDeliveryTask(t, c, "t1")
		mustAuthorize(t, c, "t1", rev, "op-auth-1")
		mustRevoke(t, c, "t1", rev+1, "op-auth-1", "superseded", "op-revoke-1")

		// Control: the currency read resolves the committed revocation.
		if _, err := c.DeliveryCurrency(mustTaskID(t, "t1")); err != nil {
			t.Fatalf("DeliveryCurrency with the committed revocation: %v", err)
		}

		key := deliveryRevocationKey("t1", "op-revoke-1")
		var rec DeliveryRevocation
		readEvidenceForTest(t, c, key, &rec)
		rec.TaskID = "t2"
		writeEvidenceForTest(t, c, key, rec)

		_, err := c.DeliveryCurrency(mustTaskID(t, "t1"))
		wantErrSubstring(t, err, "is bound to a different task", "revocation substituted from another task")
	})

	t.Run("an outcome bound to another task", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		rev := mustDeliveryTask(t, c, "t1")
		mustAuthorize(t, c, "t1", rev, "op-auth-1")
		mustCommitOutcome(t, c, "t1", rev+1, "op-auth-1", DeliveryOutcomeCompleted, "merged", "op-outcome-1")

		if _, err := c.DeliveryOutcomeByOperation(mustTaskID(t, "t1"), "op-outcome-1"); err != nil {
			t.Fatalf("DeliveryOutcomeByOperation with the committed record: %v", err)
		}

		key := deliveryOutcomeKey("t1", "op-outcome-1")
		var rec DeliveryOutcome
		readEvidenceForTest(t, c, key, &rec)
		rec.TaskID = "t2"
		writeEvidenceForTest(t, c, key, rec)

		_, err := c.DeliveryOutcomeByOperation(mustTaskID(t, "t1"), "op-outcome-1")
		wantErrSubstring(t, err, "is bound to a different task", "outcome substituted from another task")
	})
}

func readEvidenceForTest(t *testing.T, c *Canonical, key string, into any) {
	t.Helper()
	data, ok, err := readDocForTest(c.h, key)
	if err != nil || !ok {
		t.Fatalf("read evidence %s: ok=%v err=%v", key, ok, err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatalf("decode evidence %s: %v", key, err)
	}
}

func writeEvidenceForTest(t *testing.T, c *Canonical, key string, rec any) {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePathForTest(t, c, key, data); err != nil {
		t.Fatalf("write evidence %s: %v", key, err)
	}
}

// The by-operation outcome read takes an operation identity straight into a
// document key. An identity nothing was ever committed under names no
// evidence at all.
func TestGuardDeliveryByOperationReadsRefuseUnusableIdentities(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	rev := mustDeliveryTask(t, c, "t1")
	mustAuthorize(t, c, "t1", rev, "op-auth-1")
	mustRevoke(t, c, "t1", rev+1, "op-auth-1", "superseded", "op-revoke-1")
	mustAuthorize(t, c, "t1", rev+2, "op-auth-2")
	mustCommitOutcome(t, c, "t1", rev+3, "op-auth-2", DeliveryOutcomeCompleted, "merged", "op-outcome-1")

	taskID := mustTaskID(t, "t1")
	// Control: the read resolves its committed record by identity.
	if _, err := c.DeliveryOutcomeByOperation(taskID, "op-outcome-1"); err != nil {
		t.Fatalf("DeliveryOutcomeByOperation: %v", err)
	}

	t.Run("an outcome identity nothing was committed under", func(t *testing.T) {
		_, err := c.DeliveryOutcomeByOperation(taskID, "op-outcome-never")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("DeliveryOutcomeByOperation(unknown) = %v, want ErrNotFound", err)
		}
		wantErrSubstring(t, err, "has no delivery outcome", "DeliveryOutcomeByOperation with an unknown identity")
	})
}

// The current-outcome read resolves the index pointer. A task that has issued
// an authorization but committed no outcome has no pointer to resolve, and
// must say so rather than return a zero outcome as a committed one.
func TestGuardDeliveryOutcomeRefusesTaskWithNoCommittedOutcome(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	rev := mustDeliveryTask(t, c, "t1")
	mustAuthorize(t, c, "t1", rev, "op-auth-1")

	_, err := c.DeliveryOutcome(mustTaskID(t, "t1"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeliveryOutcome without an outcome = %v, want ErrNotFound", err)
	}
	wantErrSubstring(t, err, "has no delivery outcome", "DeliveryOutcome before any outcome")

	// Control: once an outcome is committed the same read resolves it.
	mustCommitOutcome(t, c, "t1", rev+1, "op-auth-1", DeliveryOutcomeCompleted, "merged", "op-outcome-1")
	if _, err := c.DeliveryOutcome(mustTaskID(t, "t1")); err != nil {
		t.Fatalf("DeliveryOutcome after a committed outcome: %v", err)
	}
}

// Revocation is an act against one issued authorization. Without an active
// authorization there is nothing to revoke, and a reason is what makes the
// revocation evidence rather than an unexplained state change.
func TestGuardRevokeDeliveryRefusesUnrevocableRequests(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	rev := mustDeliveryTask(t, c, "t1")

	t.Run("a task with no issued authorization", func(t *testing.T) {
		req := CanonicalRevokeDeliveryRequest{
			HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
			Precondition: preconditionOf(1, rev), AuthorizationOperationID: "op-auth-1", Reason: "superseded",
		}
		_, err := c.RevokeDeliveryAuthorization(mustOperation(t, "op-revoke-none", req), req)
		wantErrSubstring(t, err, "has no active delivery authorization to revoke", "revoke without an authorization")
	})

	mustAuthorize(t, c, "t1", rev, "op-auth-1")

	t.Run("a revocation with no reason", func(t *testing.T) {
		req := CanonicalRevokeDeliveryRequest{
			HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
			Precondition: preconditionOf(1, rev+1), AuthorizationOperationID: "op-auth-1", Reason: "  ",
		}
		_, err := c.RevokeDeliveryAuthorization(mustOperation(t, "op-revoke-noreason", req), req)
		wantErrSubstring(t, err, "revocation requires a reason", "revoke without a reason")
	})

	// Control: with an authorization issued and a reason given, the same
	// revocation is accepted.
	mustRevoke(t, c, "t1", rev+1, "op-auth-1", "superseded", "op-revoke-ok")
}

// The outcome's head SHA is repository evidence that ends up in an immutable
// document and in diagnostics. A value carrying path separators is not a SHA.
//
// The record validator refuses it too, with the SAME message, so a test that
// only asserted that message would stay green with this request-level check
// deleted — the same shape, and what the mutation run caught here. What
// separates them is WHERE: the request check runs before the task is read, so
// it is the only one that can refuse an outcome for a task that does not exist.
// That is what this asserts.
func TestGuardCommitDeliveryOutcomeRefusesUnsafeHeadSHABeforeReadingTheTask(t *testing.T) {
	c, _, _ := newTestCanonical(t)

	unsafeForMissingTask := CanonicalDeliveryOutcomeRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "never-created"),
		Precondition: preconditionOf(1, 1), AuthorizationOperationID: "op-auth-1",
		Status: DeliveryOutcomeCompleted, Detail: "merged", HeadSHA: "refs/heads/main",
	}
	_, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-unsafe-head-missing", unsafeForMissingTask), unsafeForMissingTask)
	wantErrSubstring(t, err, "head SHA must be a safe value", "outcome with a path-separating head SHA")
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("CommitDeliveryOutcome = %v, want the request-shape refusal rather than the missing-task refusal it would give if the head SHA were checked after the read", err)
	}

	// Control: the same request against a real task with a real head SHA
	// commits, so the refusal above is the SHA shape and nothing else.
	rev := mustDeliveryTask(t, c, "t1")
	mustAuthorize(t, c, "t1", rev, "op-auth-1")
	ok := CanonicalDeliveryOutcomeRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, rev+1), AuthorizationOperationID: "op-auth-1",
		Status: DeliveryOutcomeCompleted, Detail: "merged", HeadSHA: deliveryHead,
	}
	if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-safe-head", ok), ok); err != nil {
		t.Fatalf("CommitDeliveryOutcome with a safe head SHA: %v", err)
	}
}

// mustOperationFor keeps the linter honest about the domain import when the
// file's only other use of domain is through helpers.
var _ = domain.Precondition{}
