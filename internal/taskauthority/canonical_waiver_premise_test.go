package taskauthority

import (
	"strings"
	"testing"
)

// Nine refusal branches in this package stay waived in
// .github/uncovered-guards.baseline because no state a caller can build reaches
// them: an earlier guard refuses first. A waiver like that is only as good as
// its premise, and a premise nobody tests is a comment. Each test here pins one:
// it builds the state that WOULD enter the waived branch and asserts the refusal
// carries the message of the earlier guard.
//
// That is deliberately not a test of the waived branch — it cannot be, the
// branch is unreachable. It is a test of the reason. When the earlier guard
// moves or softens, one of these goes red, and the waiver has to be re-argued
// instead of quietly becoming wrong. The failure mode being defended against is
// the ordinary one: somebody reads "unreachable" on a live check, believes it,
// and deletes the check.

// Premise for three waived `strings.TrimSpace(cur.Definition.Owner) == ""`
// branches — in BindEndpoint, AuthorizeDelivery and BeginSpawn apply functions.
//
// Owner is a validated field of the Aggregate, and every read runs
// validateAggregate and fails closed. So an aggregate with a blank owner never
// reaches an apply function: the read refuses first. Invalidated by making any
// task read tolerate a blank owner, or by admitting an aggregate into the store
// that was not validated on the way in.
//
// Each fixture is driven to the state its operation requires before the owner
// is blanked, so the owner check is the NEXT guard the apply function would
// read: BeginSpawn wants a queued task with no bindings, BindEndpoint wants a
// bound worktree, AuthorizeDelivery wants a working task. Without that the test
// would still go red for the right reason and still prove less than it claims —
// the call would be stopped by a guard further up, and nothing here would show
// it. The `wantIfReached` message is what that next guard emits; it is asserted
// NOT to appear, which is the whole point: the read refuses before it.
func TestPremiseNoAggregateWithABlankOwnerReachesApply(t *testing.T) {
	for _, tc := range []struct {
		name string
		// setup returns a canonical whose task t1 is one step short of the
		// apply-level owner check, and that task's current revision.
		setup         func(t *testing.T) (*Canonical, uint64)
		call          func(t *testing.T, c *Canonical, rev uint64) error
		wantIfReached string
	}{
		{
			name: "BeginSpawn",
			setup: func(t *testing.T) (*Canonical, uint64) {
				c, _, _ := newTestCanonical(t)
				mustCreate(t, c, "t1")
				return c, 1
			},
			call: func(t *testing.T, c *Canonical, rev uint64) error {
				req := launchRequest(c, "t1", preconditionOf(1, rev))
				_, err := c.BeginSpawn(mustOperation(t, "op-premise-spawn", req), req)
				return err
			},
			wantIfReached: "is not ready to spawn",
		},
		{
			name: "BindEndpoint",
			setup: func(t *testing.T) (*Canonical, uint64) {
				c, _, _ := newTestCanonical(t)
				// The worktree check runs before the owner check.
				// mustBindWorktree creates the task itself.
				_, agg := mustBindWorktree(t, c, "t1")
				return c, uint64(agg.Revision)
			},
			call: func(t *testing.T, c *Canonical, rev uint64) error {
				req := CanonicalBindEndpointRequest{
					HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
					Precondition: preconditionOf(1, rev), Binding: endpointBinding(), Reason: "bind",
				}
				_, err := c.BindEndpoint(mustOperation(t, "op-premise-bindep", req), req)
				return err
			},
			wantIfReached: "is not ready to spawn",
		},
		{
			name: "AuthorizeDelivery",
			setup: func(t *testing.T) (*Canonical, uint64) {
				c, _, _ := newTestCanonical(t)
				// The phase check runs before the owner check.
				return c, mustDeliveryTask(t, c, "t1")
			},
			call: func(t *testing.T, c *Canonical, rev uint64) error {
				req := authorizeRequest(c, "t1", preconditionOf(1, rev))
				_, err := c.AuthorizeDelivery(mustOperation(t, "op-premise-auth", req), req)
				return err
			},
			wantIfReached: "delivery authorization requires an owner",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Control: the fixture COMMITS while the owner is there, which is
			// what makes it a state the operation would otherwise accept. It runs
			// on its own canonical, because committing moves the task past the
			// state the refusal below needs.
			control, controlRev := tc.setup(t)
			if err := tc.call(t, control, controlRev); err != nil {
				t.Fatalf("%s did not commit against the fixture state, so the refusal below is not attributable to the owner: %v", tc.name, err)
			}

			c, rev := tc.setup(t)
			// The rewrite does not advance the aggregate revision, so the
			// precondition stays the one the control committed under.
			rewriteTaskDocForTest(t, c, "t1", func(agg Aggregate) Aggregate {
				agg.Definition.Owner = "   "
				return agg
			})
			err := tc.call(t, c, rev)
			if err == nil {
				t.Fatalf("%s accepted an aggregate with a blank owner", tc.name)
			}
			if !strings.Contains(err.Error(), "missing owner") {
				t.Fatalf("%s = %v, want the read-time %q refusal that makes the apply-level owner check unreachable", tc.name, err, "missing owner")
			}
			if strings.Contains(err.Error(), tc.wantIfReached) {
				t.Fatalf("%s = %v, which is the apply-level owner refusal: the read no longer fails closed and the waiver's premise is gone", tc.name, err)
			}
		})
	}
}

// Premise for the waived `cur.Endpoint != nil` branch in BindEndpoint's apply.
//
// Only BindEndpoint sets Endpoint, and it commits PhaseWorking in the same
// atomic change. So a bound endpoint implies a working task, and the phase
// guard above refuses before the endpoint check is read. Invalidated by any
// second writer of Endpoint, or by a bind that leaves the phase queued.
func TestPremiseBindEndpointRefusesOnPhaseBeforeItCanSeeABoundEndpoint(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	intent, rev := launchToWorking(t, c, "t1")

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Endpoint == nil {
		t.Fatalf("the fixture task has no bound endpoint, so it cannot pin this premise")
	}

	req := CanonicalBindEndpointRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, rev), Binding: launchEndpointBinding(intent, "handle-t1"), Reason: "bind again",
	}
	_, err = c.BindEndpoint(mustOperation(t, "op-premise-rebind", req), req)
	if err == nil {
		t.Fatalf("BindEndpoint rebound an endpoint that is already bound")
	}
	if !strings.Contains(err.Error(), "bind endpoint requires queued") {
		t.Fatalf("BindEndpoint = %v, want the phase refusal that makes the endpoint check unreachable", err)
	}
}

// Premise for the three waived claim-identity branches — the
// `claim.OperationID != req.ClaimOperationID || claim.Generation != req.ClaimGeneration`
// check inside BeginCleanup, CompleteCleanup and AbortCleanup's apply.
//
// checkCleanupFence runs in the fenced mutation BEFORE apply and rejects any
// gate whose identity differs from the stored claim, in both the active and the
// reconciled branch. So a foreign identity never reaches apply, and the
// apply-level checks are second readers of a question already answered.
//
// They are waived on unreachability alone, not on any tautology argument: the
// two messages are in fact distinct ("cleanup claim fence mismatch" against
// "stores a cleanup claim of a different identity ... refusing to overwrite |
// complete | abort"), so an assertion on the apply-level wording would be
// attributable if the state could be built. It cannot. Removing the fence lets
// exactly those three apply-level messages through, which is what says the
// fence is the only thing standing in front of them. Invalidated by a cleanup
// path that reaches apply without going through checkCleanupFence.
func TestPremiseCleanupFenceRejectsAForeignClaimBeforeApply(t *testing.T) {
	// Each case is run on its own canonical: the control commits, so sharing
	// one would leave the second call refused on the precondition instead.
	for _, tc := range []struct {
		name string
		call func(t *testing.T, c *Canonical, claimOpID string) error
	}{
		{
			"BeginCleanup",
			func(t *testing.T, c *Canonical, claimOpID string) error {
				req := CanonicalBeginCleanupRequest{
					HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
					Precondition: preconditionOf(1, 2), ClaimOperationID: claimOpID,
					ClaimGeneration: Generation(1), Reason: "begin cleanup",
				}
				_, err := c.BeginCleanup(mustOperation(t, "op-premise-begin", req), req)
				return err
			},
		},
		{
			"CompleteCleanup",
			func(t *testing.T, c *Canonical, claimOpID string) error {
				req := CanonicalCompleteCleanupRequest{
					HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
					Precondition: preconditionOf(1, 2), ClaimOperationID: claimOpID,
					ClaimGeneration: Generation(1), Reason: "cleanup done",
				}
				_, err := c.CompleteCleanup(mustOperation(t, "op-premise-complete", req), req)
				return err
			},
		},
		{
			"AbortCleanup",
			func(t *testing.T, c *Canonical, claimOpID string) error {
				req := CanonicalAbortCleanupRequest{
					HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"),
					Precondition: preconditionOf(1, 2), ClaimOperationID: claimOpID,
					ClaimGeneration: Generation(1), Reason: "operator abort",
				}
				_, err := c.AbortCleanup(mustOperation(t, "op-premise-abort", req), req)
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			retired := func() *Canonical {
				c, _, _ := newTestCanonical(t)
				mustCreate(t, c, "t1")
				retireWithClaim(t, c, "t1", preconditionOf(1, 1), "op-retire-1")
				return c
			}

			// Control: the continuation carrying the stored claim identity is
			// accepted, so the refusal below is the identity and nothing else.
			if err := tc.call(t, retired(), "op-retire-1"); err != nil {
				t.Fatalf("%s with the stored claim identity: %v", tc.name, err)
			}

			err := tc.call(t, retired(), "op-retire-foreign")
			if err == nil {
				t.Fatalf("%s accepted a foreign claim identity", tc.name)
			}
			if !strings.Contains(err.Error(), "cleanup claim fence mismatch") {
				t.Fatalf("%s = %v, want the fence refusal that makes the apply-level identity check unreachable", tc.name, err)
			}
		})
	}
}

// Premise for the waived `cur.Retirement == nil || ...` branch in BeginCleanup,
// which the code itself calls a defensive legacy path.
//
// That branch is guarded by `claim == nil`, and Retire commits the cleanup
// claim atomically with the transition to retired — unconditionally, unlike the
// retirement evidence, which is preserved only for a generation that held a
// binding. So a retired generation always carries a claim, BeginCleanup always
// takes the claim-present branch above, and the nil-claim path below it cannot
// be entered. Invalidated by any retirement that commits without setting the
// claim, or by a claim that can be cleared while the generation stays retired.
func TestPremiseRetireAlwaysCommitsACleanupClaim(t *testing.T) {
	t.Run("a generation that held no binding", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustCreate(t, c, "t1")
		retireWithClaim(t, c, "t1", preconditionOf(1, 1), "op-retire-1")
		assertClaimedRetirement(t, c, "t1", "op-retire-1")
	})

	t.Run("a generation that held a worktree and an endpoint", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		_, rev := launchToWorking(t, c, "t1")
		retireWithClaim(t, c, "t1", preconditionOf(1, rev), "op-retire-1")
		agg := assertClaimedRetirement(t, c, "t1", "op-retire-1")
		if agg.Retirement == nil {
			t.Fatalf("a retired generation that held bindings preserved no retirement evidence")
		}
	})
}

func assertClaimedRetirement(t *testing.T, c *Canonical, taskID, claimOpID string) Aggregate {
	t.Helper()
	agg, err := c.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseRetired {
		t.Fatalf("task %s is %s, want retired", taskID, agg.Phase)
	}
	if agg.CleanupClaim == nil {
		t.Fatalf("retired task %s carries no cleanup claim, so BeginCleanup's nil-claim path is reachable and must not stay waived", taskID)
	}
	if agg.CleanupClaim.OperationID != claimOpID || agg.CleanupClaim.Generation != agg.Generation {
		t.Fatalf("cleanup claim = (%q, %s), want the retiring operation %q at generation %s", agg.CleanupClaim.OperationID, agg.CleanupClaim.Generation, claimOpID, agg.Generation)
	}
	if agg.CleanupClaim.Status != CleanupActive {
		t.Fatalf("cleanup claim status = %s, want active", agg.CleanupClaim.Status)
	}
	return agg
}
