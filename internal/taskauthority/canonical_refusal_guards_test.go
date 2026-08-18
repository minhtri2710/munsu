package taskauthority

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
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
// BEO-87 shape.
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

// Start is the queued -> working transition. Starting a task that is already
// working would issue a second start against a live soldier.
func TestGuardStartRefusesNonQueuedTask(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	first := startWithRev(c, "t1", 1)
	if _, err := c.Start(mustOperation(t, "op-start-1", first), first); err != nil {
		t.Fatalf("Start on a queued task: %v", err)
	}
	second := startWithRev(c, "t1", 2)
	_, err := c.Start(mustOperation(t, "op-start-2", second), second)
	wantErrSubstring(t, err, "start requires queued task", "Start on a working task")
}

// Block moves a live task out of dispatch. A terminal task is not live:
// blocking it would resurrect a completed generation into a blocked one.
func TestGuardBlockRefusesTerminalTask(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// Control: the same call against the queued task is accepted, so the
	// refusal below is the phase and not the request.
	blockQueued := CanonicalBlockRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 1), Detail: "waiting", Reason: "block",
	}
	if _, err := c.Block(mustOperation(t, "op-block-control", blockQueued), blockQueued); err != nil {
		t.Fatalf("Block on a queued task: %v", err)
	}

	mustCreate(t, c, "t2")
	done := CanonicalCompleteRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t2"),
		Precondition: preconditionOf(1, 1), To: PhaseDone, Reason: "done",
	}
	if _, err := c.Complete(mustOperation(t, "op-complete-t2", done), done); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	blockDone := CanonicalBlockRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t2"),
		Precondition: preconditionOf(1, 2), Detail: "waiting", Reason: "block",
	}
	_, err := c.Block(mustOperation(t, "op-block-done", blockDone), blockDone)
	wantErrSubstring(t, err, "block requires queued or working task", "Block on a done task")
}

// Complete is the transition INTO a terminal phase, and only into done or
// resolved: it is neither a way back out of one nor a generic phase setter.
func TestGuardCompleteRefusesTerminalSourceAndNonTerminalTarget(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	first := CanonicalCompleteRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 1), To: PhaseDone, Reason: "done",
	}
	if _, err := c.Complete(mustOperation(t, "op-complete-1", first), first); err != nil {
		t.Fatalf("Complete on a queued task: %v", err)
	}

	t.Run("a task that is already terminal", func(t *testing.T) {
		again := CanonicalCompleteRequest{
			HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
			Precondition: preconditionOf(1, 2), To: PhaseResolved, Reason: "done again",
		}
		_, err := c.Complete(mustOperation(t, "op-complete-2", again), again)
		wantErrSubstring(t, err, "complete requires a non-terminal task", "Complete on a done task")
	})

	t.Run("a target phase that is not terminal", func(t *testing.T) {
		mustCreate(t, c, "t2")
		toQueued := CanonicalCompleteRequest{
			HomeID: c.HomeID(), TaskID: mustTaskID(t, "t2"),
			Precondition: preconditionOf(1, 1), To: PhaseQueued, Reason: "complete",
		}
		_, err := c.Complete(mustOperation(t, "op-complete-queued", toQueued), toQueued)
		wantErrSubstring(t, err, "is not a terminal phase", "Complete into queued")
	})
}

// Reopen creates the next Generation of a finished task. A task that never
// existed has no generation to succeed, and a live one is not finished.
func TestGuardReopenRefusesMissingAndNonTerminalTasks(t *testing.T) {
	c, _, _ := newTestCanonical(t)

	t.Run("a task that does not exist", func(t *testing.T) {
		req := CanonicalReopenRequest{
			HomeID: c.HomeID(), TaskID: mustTaskID(t, "never-created"),
			Precondition: preconditionOf(1, 1), Reason: "reopen",
		}
		_, err := c.Reopen(mustOperation(t, "op-reopen-missing", req), req)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("Reopen of a missing task = %v, want ErrNotFound", err)
		}
		wantErrSubstring(t, err, "not found", "Reopen of a missing task")
	})

	t.Run("a task that is still live", func(t *testing.T) {
		mustCreate(t, c, "t1")
		req := CanonicalReopenRequest{
			HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
			Precondition: preconditionOf(1, 1), Reason: "reopen",
		}
		_, err := c.Reopen(mustOperation(t, "op-reopen-queued", req), req)
		wantErrSubstring(t, err, "reopen requires terminal task", "Reopen of a queued task")
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

	release := CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "hold-1", Reason: "resolved"}
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
	ok := CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "hold-1", Reason: "resolved"}
	if _, err := c.ReleaseHold(mustOperation(t, "op-relhold-ok", ok), ok); err != nil {
		t.Fatalf("ReleaseHold with a safe ID: %v", err)
	}

	unsafe := CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "holds/hold-1", Reason: "resolved"}
	_, err := c.ReleaseHold(mustOperation(t, "op-relhold-unsafe", unsafe), unsafe)
	wantErrSubstring(t, err, "dispatch hold ID must be a safe non-empty value", "ReleaseHold with a path-separating ID")
}

// --- canonical_launch.go: the launch chain -------------------------------

// launchToWorking drives one task through the full launch chain — intent,
// worktree, acquired endpoint, launch evidence, bind — and leaves it working.
// It returns the committed intent and the working revision.
func launchToWorking(t *testing.T, c *Canonical, taskID string) (CanonicalBeginSpawnRequest, uint64) {
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

	attach := attachRequest(c, taskID, preconditionOf(1, rev), intent, "handle-"+taskID)
	if _, err := c.AttachEndpoint(mustOperation(t, "op-l2w-attach-"+taskID, attach), attach); err != nil {
		t.Fatalf("AttachEndpoint(%s): %v", taskID, err)
	}
	rev++

	record := recordLaunchRequest(c, taskID, preconditionOf(1, rev), intent)
	if _, err := c.RecordLaunch(mustOperation(t, "op-l2w-record-"+taskID, record), record); err != nil {
		t.Fatalf("RecordLaunch(%s): %v", taskID, err)
	}
	rev++

	be := CanonicalBindEndpointRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, taskID),
		Precondition: preconditionOf(1, rev), Binding: launchEndpointBinding(intent, "handle-"+taskID), Reason: "spawn",
	}
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
		CommandDigest: digestOf("launch:t1"), Reason: "record",
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

// --- canonical_promote.go ------------------------------------------------

// Promote is the named scout -> ship transition, not a generic kind setter:
// any other pair of kinds is refused before the task is even read.
func TestGuardPromoteRefusesAnyOtherKindPair(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	prec := mustDoneScout(t, c, "t1")

	for _, tc := range []struct {
		name   string
		mutate func(*CanonicalPromoteRequest)
	}{
		{"a current kind that is not scout", func(r *CanonicalPromoteRequest) { r.CurrentKind = "ship" }},
		{"a target kind that is not ship", func(r *CanonicalPromoteRequest) { r.TargetKind = "scout" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := promoteRequest(t, c, "t1", prec)
			tc.mutate(&req)
			_, err := c.Promote(mustOperation(t, opIDFor("op-promote", tc.name), req), req)
			wantErrSubstring(t, err, "promotion requires scout -> ship kind promotion", "Promote with "+tc.name)
		})
	}

	// Control: the scout -> ship pair on the same task is accepted, so the
	// refusals above are the kind pair and not the task's state.
	ok := promoteRequest(t, c, "t1", prec)
	if _, err := c.Promote(mustOperation(t, "op-promote-control", ok), ok); err != nil {
		t.Fatalf("Promote scout -> ship: %v", err)
	}
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
	complete := CanonicalCompleteCleanupRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 2), ClaimOperationID: "op-retire-1",
		ClaimGeneration: Generation(1), Reason: "cleanup done",
	}
	if _, err := c.CompleteCleanup(mustOperation(t, "op-cleanup-complete", complete), complete); err != nil {
		t.Fatalf("CompleteCleanup: %v", err)
	}

	req := retireRequest(t, c, "t1", preconditionOf(1, 3))
	_, err := c.Retire(mustOperation(t, "op-retire-2", req), req)
	wantErrSubstring(t, err, "is already retired", "Retire of a retired generation")
}

// --- canonical_cleanup.go ------------------------------------------------

// The cleanup claim is what a fleet teardown reconciles. Aborting a claim that
// was never asserted would report a reconciliation that never happened.
func TestGuardAbortCleanupRequiresAClaim(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := CanonicalAbortCleanupRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 1), ClaimOperationID: "op-retire-1",
		ClaimGeneration: Generation(1), Reason: "operator abort",
	}
	_, err := c.AbortCleanup(mustOperation(t, "op-abort-no-claim", req), req)
	wantErrSubstring(t, err, "has no cleanup claim to abort", "AbortCleanup without a claim")

	// Control: the same call against a task whose retirement DID assert a claim
	// is accepted, so the refusal above is the absent claim and not the request.
	mustCreate(t, c, "t2")
	retireWithClaim(t, c, "t2", preconditionOf(1, 1), "op-retire-t2")
	ok := CanonicalAbortCleanupRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t2"),
		Precondition: preconditionOf(1, 2), ClaimOperationID: "op-retire-t2",
		ClaimGeneration: Generation(1), Reason: "operator abort",
	}
	if _, err := c.AbortCleanup(mustOperation(t, "op-abort-t2", ok), ok); err != nil {
		t.Fatalf("AbortCleanup of an active claim: %v", err)
	}
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

		// Control: the committed revocation resolves by its operation identity.
		if _, err := c.DeliveryRevocationByOperation(mustTaskID(t, "t1"), "op-revoke-1"); err != nil {
			t.Fatalf("DeliveryRevocationByOperation with the committed record: %v", err)
		}

		key := deliveryRevocationKey("t1", "op-revoke-1")
		var rec DeliveryRevocation
		readEvidenceForTest(t, c, key, &rec)
		rec.TaskID = "t2"
		writeEvidenceForTest(t, c, key, rec)

		_, err := c.DeliveryRevocationByOperation(mustTaskID(t, "t1"), "op-revoke-1")
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

// The by-operation reads take an operation identity straight into a document
// key. An unsafe value would build a key outside the task's evidence
// directory, and an identity nothing was ever committed under names no
// evidence at all.
func TestGuardDeliveryByOperationReadsRefuseUnusableIdentities(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	rev := mustDeliveryTask(t, c, "t1")
	mustAuthorize(t, c, "t1", rev, "op-auth-1")
	mustRevoke(t, c, "t1", rev+1, "op-auth-1", "superseded", "op-revoke-1")
	mustAuthorize(t, c, "t1", rev+2, "op-auth-2")
	mustCommitOutcome(t, c, "t1", rev+3, "op-auth-2", DeliveryOutcomeCompleted, "merged", "op-outcome-1")

	taskID := mustTaskID(t, "t1")
	// Control: each read resolves its committed record by identity.
	if _, err := c.DeliveryAuthorizationByOperation(taskID, "op-auth-1"); err != nil {
		t.Fatalf("DeliveryAuthorizationByOperation: %v", err)
	}
	if _, err := c.DeliveryRevocationByOperation(taskID, "op-revoke-1"); err != nil {
		t.Fatalf("DeliveryRevocationByOperation: %v", err)
	}
	if _, err := c.DeliveryOutcomeByOperation(taskID, "op-outcome-1"); err != nil {
		t.Fatalf("DeliveryOutcomeByOperation: %v", err)
	}

	t.Run("an unsafe authorization identity", func(t *testing.T) {
		_, err := c.DeliveryAuthorizationByOperation(taskID, "../op-auth-1")
		wantErrSubstring(t, err, "authorization operation identity must be a safe non-empty value", "DeliveryAuthorizationByOperation with a path-separating identity")
	})
	t.Run("an unsafe revocation identity", func(t *testing.T) {
		_, err := c.DeliveryRevocationByOperation(taskID, "../op-revoke-1")
		wantErrSubstring(t, err, "revocation operation identity must be a safe non-empty value", "DeliveryRevocationByOperation with a path-separating identity")
	})
	t.Run("a revocation identity nothing was committed under", func(t *testing.T) {
		_, err := c.DeliveryRevocationByOperation(taskID, "op-revoke-never")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("DeliveryRevocationByOperation(unknown) = %v, want ErrNotFound", err)
		}
		wantErrSubstring(t, err, "has no delivery revocation", "DeliveryRevocationByOperation with an unknown identity")
	})
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
// deleted — the BEO-87 shape, and what the mutation run caught here. What
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
