package taskauthority

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// deliveryHead is a full 40-hex Git object ID carried by the delivery
// identity and the bound worktree head (the canonical Git fixtures are
// 40-hex object IDs, not 64-hex digests).
const deliveryHead = "abc123def456abc123def456abc123def456abc1"

// deliveryIdentity builds a fully valid typed delivery identity whose head
// matches the delivery worktree binding head.
func deliveryIdentity() domain.DeliveryIdentity {
	return domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      "minhtri2710",
		Repo:       "munsu",
		Number:     42,
		URL:        "https://github.com/minhtri2710/munsu/pull/42",
		BaseRef:    "main",
		HeadRef:    "feature/delivery",
		HeadSHA:    deliveryHead,
		CapturedAt: "2026-08-05T00:00:00Z",
	}
}

// deliveryWorktreeBinding is a worktree binding whose head is the delivery
// identity head, and deliveryEndpointBinding is the matching endpoint lease.
func deliveryWorktreeBinding() WorktreeBinding {
	b := worktreeBinding()
	b.Head = deliveryHead
	return b
}

func deliveryEndpointBinding() EndpointBinding {
	return endpointBinding()
}

// mustDeliveryTask creates a task and binds the worktree and endpoint so it
// is working (revision 3) with the exact delivery bindings.
func mustDeliveryTask(t *testing.T, c *Canonical, taskID string) uint64 {
	t.Helper()
	mustCreate(t, c, taskID)
	bw := CanonicalBindWorktreeRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: preconditionOf(1, 1),
		Binding:      deliveryWorktreeBinding(),
		Reason:       "bind worktree",
	}
	if _, err := c.BindWorktree(mustOperation(t, "op-delivery-bindwt-"+taskID, bw), bw); err != nil {
		t.Fatalf("BindWorktree(%s): %v", taskID, err)
	}
	be := CanonicalBindEndpointRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: preconditionOf(1, 2),
		Binding:      deliveryEndpointBinding(),
		Reason:       "spawn",
	}
	if _, err := c.BindEndpoint(mustOperation(t, "op-delivery-bindep-"+taskID, be), be); err != nil {
		t.Fatalf("BindEndpoint(%s): %v", taskID, err)
	}
	return 3
}

// authorizeRequest builds a provider-merge authorization request for the task.
func authorizeRequest(c *Canonical, taskID string, prec domain.Precondition) CanonicalDeliveryAuthorizationRequest {
	id, _ := domain.NewTaskID(taskID)
	return CanonicalDeliveryAuthorizationRequest{
		HomeID:       c.HomeID(),
		TaskID:       id,
		Precondition: prec,
		Kind:         DeliveryAuthorizationProviderMerge,
		Identity:     deliveryIdentity(),
		Preconditions: []DeliveryPrecondition{
			DeliveryPreconditionPRMergeable,
			DeliveryPreconditionPRHeadCurrent,
		},
	}
}

func mustAuthorize(t *testing.T, c *Canonical, taskID string, rev uint64, opID string) DeliveryAuthorization {
	t.Helper()
	req := authorizeRequest(c, taskID, preconditionOf(1, rev))
	res, err := c.AuthorizeDelivery(mustOperation(t, opID, req), req)
	if err != nil {
		t.Fatalf("AuthorizeDelivery(%s): %v", taskID, err)
	}
	if res.Replayed {
		t.Fatalf("fresh AuthorizeDelivery(%s) marked replayed", taskID)
	}
	return res.Authorization
}

// mustRevoke revokes the current authorization of the task.
func mustRevoke(t *testing.T, c *Canonical, taskID string, rev uint64, authOpID, reason string) DeliveryAuthorization {
	t.Helper()
	req := CanonicalRevokeDeliveryRequest{
		HomeID:                   c.HomeID(),
		TaskID:                   mustTaskID(t, taskID),
		Precondition:             preconditionOf(1, rev),
		AuthorizationOperationID: authOpID,
		Reason:                   reason,
	}
	res, err := c.RevokeDeliveryAuthorization(mustOperation(t, "op-revoke-"+taskID, req), req)
	if err != nil {
		t.Fatalf("RevokeDeliveryAuthorization(%s): %v", taskID, err)
	}
	if res.Replayed {
		t.Fatalf("fresh revoke(%s) marked replayed", taskID)
	}
	return res.Authorization
}

func mustCommitOutcome(t *testing.T, c *Canonical, taskID string, rev uint64, authOpID string, status DeliveryOutcomeStatus, detail string) DeliveryOutcome {
	t.Helper()
	req := CanonicalDeliveryOutcomeRequest{
		HomeID:                   c.HomeID(),
		TaskID:                   mustTaskID(t, taskID),
		Precondition:             preconditionOf(1, rev),
		AuthorizationOperationID: authOpID,
		Status:                   status,
		Detail:                   detail,
	}
	res, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-"+taskID+"-"+string(status), req), req)
	if err != nil {
		t.Fatalf("CommitDeliveryOutcome(%s, %s): %v", taskID, status, err)
	}
	if res.Replayed {
		t.Fatalf("fresh outcome(%s, %s) marked replayed", taskID, status)
	}
	return res.Outcome
}

// rewriteTaskDocForTest replaces the current task document on disk with a
// modified aggregate and the incremented envelope revision. Only read-only
// currency checks may follow a rewrite: the home journal scope revision is
// deliberately not advanced, so a later mutation fails closed on the
// optimistic check (which the tests assert indirectly by not mutating).
func rewriteTaskDocForTest(t *testing.T, c *Canonical, taskID string, mutate func(Aggregate) Aggregate) {
	t.Helper()
	doc, exists, err := c.readTaskDoc(taskID)
	if err != nil || !exists {
		t.Fatalf("read task doc for rewrite: exists=%v err=%v", exists, err)
	}
	next := doc
	next.Aggregate = mutate(doc.Aggregate)
	next.HomeRevision++
	data, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePathForTest(t, c, taskCurrentKey(taskID), data); err != nil {
		t.Fatal(err)
	}
}

func writePathForTest(t *testing.T, c *Canonical, key string, data []byte) error {
	t.Helper()
	p := mustPathForTest(t, c.h, key)
	return os.WriteFile(p, data, 0600)
}

// TestCanonicalDeliveryAuthorizationIssuancePinsPostIssuanceState proves a
// successful issuance records the committed post-issuance Task revision, the
// exact working phase, ownership, typed identity/head, kind, expected state,
// binding digest, holds digest, and preconditions.
func TestCanonicalDeliveryAuthorizationIssuancePinsPostIssuanceState(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")

	auth := mustAuthorize(t, c, "t1", 3, "op-auth-t1")
	if auth.Revision != 4 {
		t.Fatalf("post-issuance revision = %d, want 4", auth.Revision)
	}
	if auth.Generation != 1 || auth.Phase != PhaseWorking || auth.Owner != "owner" {
		t.Fatalf("authorization generation/phase/owner = %+v", auth)
	}
	if auth.Kind != DeliveryAuthorizationProviderMerge || auth.OperationID != "op-auth-t1" {
		t.Fatalf("authorization kind/operation = %+v", auth)
	}
	if auth.IssuedAt <= 0 {
		t.Fatalf("authorization missing issued timestamp")
	}
	want := deliveryIdentity()
	if auth.Identity != want {
		t.Fatalf("authorization identity = %+v, want exact typed identity", auth.Identity)
	}
	if auth.ExpectedState != nil {
		t.Fatalf("provider-merge authorization carries expected state: %+v", auth.ExpectedState)
	}
	if len(auth.Preconditions) != 2 {
		t.Fatalf("preconditions = %+v, want 2", auth.Preconditions)
	}

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 4 || agg.Phase != PhaseWorking {
		t.Fatalf("aggregate after issuance = rev %d phase %q, want rev 4 working", agg.Revision, agg.Phase)
	}
	if auth.BindingDigest != deliveryBindingDigest(*agg.Endpoint, *agg.Worktree) {
		t.Fatalf("binding digest = %q, want recomputed %q", auth.BindingDigest, deliveryBindingDigest(*agg.Endpoint, *agg.Worktree))
	}
	holds, err := c.ListHolds()
	if err != nil {
		t.Fatal(err)
	}
	if auth.HoldsDigest != deliveryHoldsDigest(holds, agg) {
		t.Fatalf("holds digest = %q, want recomputed %q", auth.HoldsDigest, deliveryHoldsDigest(holds, agg))
	}

	// The narrow current read returns the same record.
	read, err := c.DeliveryAuthorization(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if read.OperationID != auth.OperationID || read.Revision != auth.Revision || read.Identity != auth.Identity {
		t.Fatalf("DeliveryAuthorization read = %+v, want %+v", read, auth)
	}
}

// TestCanonicalDeliveryAuthorizationRepositoryMutationPinsExpectedState
// proves the repository-mutation kind pins the operation-specific expected
// repository state (ref + old SHA lease semantics).
func TestCanonicalDeliveryAuthorizationRepositoryMutationPinsExpectedState(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")

	req := authorizeRequest(c, "t1", preconditionOf(1, 3))
	req.Kind = DeliveryAuthorizationRepositoryMutation
	req.Preconditions = []DeliveryPrecondition{DeliveryPreconditionWorktreeClean}
	req.ExpectedState = &DeliveryExpectedState{Ref: "refs/heads/feature/delivery", OldSHA: "1111222233334444555566667777888899990000"}
	res, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-repo-t1", req), req)
	if err != nil {
		t.Fatalf("AuthorizeDelivery(repository-mutation): %v", err)
	}
	if res.Authorization.ExpectedState == nil {
		t.Fatalf("repository-mutation authorization missing expected state")
	}
	if res.Authorization.ExpectedState.Ref != "refs/heads/feature/delivery" || res.Authorization.ExpectedState.OldSHA != "1111222233334444555566667777888899990000" {
		t.Fatalf("expected state = %+v", res.Authorization.ExpectedState)
	}
}

// TestCanonicalDeliveryAuthorizationFailClosed proves issuance fails closed
// for every non-issuable state: non-working phases, missing owner, missing
// required bindings, active transfer reservation, matching delivery hold,
// invalid identity, invalid kind/preconditions/expected state, an already
// active authorization, and a terminal committed outcome.
func TestCanonicalDeliveryAuthorizationFailClosed(t *testing.T) {
	// queued/blocked/done/retired phases.
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, c *Canonical)
	}{
		{"queued", func(t *testing.T, c *Canonical) { mustCreate(t, c, "t1") }},
		{"blocked", func(t *testing.T, c *Canonical) {
			mustCreate(t, c, "t1")
			block := CanonicalBlockRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 1), Detail: "d", Reason: "block"}
			if _, err := c.Block(mustOperation(t, "op-block-q", block), block); err != nil {
				t.Fatal(err)
			}
		}},
		{"done", func(t *testing.T, c *Canonical) {
			mustCreate(t, c, "t1")
			complete := CanonicalCompleteRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 1), To: PhaseDone, Reason: "done"}
			if _, err := c.Complete(mustOperation(t, "op-complete-q", complete), complete); err != nil {
				t.Fatal(err)
			}
		}},
		{"retired", func(t *testing.T, c *Canonical) {
			mustCreate(t, c, "t1")
			complete := CanonicalCompleteRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 1), To: PhaseDone, Reason: "done"}
			if _, err := c.Complete(mustOperation(t, "op-complete-r", complete), complete); err != nil {
				t.Fatal(err)
			}
			req := retireRequest(t, c, "t1", preconditionOf(1, 2))
			if _, err := c.Retire(mustOperation(t, "op-retire-q", req), req); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := newTestCanonical(t)
			tc.setup(t, c)
			agg, err := c.Get(mustTaskID(t, "t1"))
			if err != nil {
				t.Fatal(err)
			}
			req := authorizeRequest(c, "t1", preconditionOf(uint64(agg.Generation), uint64(agg.Revision)))
			if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-fail-"+tc.name, req), req); !errors.Is(err, ErrPrecondition) {
				t.Fatalf("authorize on %s task = %v, want ErrPrecondition", tc.name, err)
			}
		})
	}

	// Missing owner fails closed: a task whose definition carries no owner is
	// malformed current state and can never receive a delivery authorization.
	t.Run("missing-owner", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		rewriteTaskDocForTest(t, c, "t1", func(agg Aggregate) Aggregate {
			agg.Definition.Owner = " "
			return agg
		})
		req := authorizeRequest(c, "t1", preconditionOf(1, 3))
		if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-no-owner", req), req); err == nil {
			t.Fatalf("authorize without owner = nil error, want fail closed")
		}
	})

	// Missing required bindings: a working task started without bindings.
	t.Run("missing-bindings", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustCreate(t, c, "t1")
		start := startWithRev(c, "t1", 1)
		if _, err := c.Start(mustOperation(t, "op-start-nobind", start), start); err != nil {
			t.Fatal(err)
		}
		req := authorizeRequest(c, "t1", preconditionOf(1, 2))
		if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-nobind", req), req); !errors.Is(err, ErrPrecondition) {
			t.Fatalf("authorize without bindings = %v, want ErrPrecondition", err)
		}
	})

	// Active transfer reservation fences issuance.
	t.Run("reserved", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		mustReserveTransfer(t, c, "t1", preconditionOf(1, 3), "dest-home")
		req := authorizeRequest(c, "t1", preconditionOf(1, 4))
		if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-reserved", req), req); !errors.Is(err, ErrConflict) {
			t.Fatalf("authorize on reserved task = %v, want ErrConflict", err)
		}
	})

	// A matching active delivery hold blocks authorization.
	t.Run("matching-hold", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		hold := CanonicalAddHoldRequest{HomeID: c.HomeID(), HoldID: "hold-delivery", Scope: DispatchHoldScope{TaskIDs: []string{"t1"}}, Actions: []DispatchAction{DispatchActionDelivery}, Reason: "freeze delivery"}
		if _, err := c.AddHold(mustOperation(t, "op-add-hold-delivery", hold), hold); err != nil {
			t.Fatal(err)
		}
		req := authorizeRequest(c, "t1", preconditionOf(1, 3))
		if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-held", req), req); !errors.Is(err, ErrDispatchHeld) {
			t.Fatalf("authorize with matching delivery hold = %v, want ErrDispatchHeld", err)
		}
	})

	// An already-active authorization blocks a second issuance.
	t.Run("active-authorization", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		mustAuthorize(t, c, "t1", 3, "op-auth-t1")
		req := authorizeRequest(c, "t1", preconditionOf(1, 4))
		if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-second", req), req); !errors.Is(err, ErrConflict) {
			t.Fatalf("second authorization = %v, want ErrConflict", err)
		}
	})

	// A terminal committed outcome blocks a new authorization.
	t.Run("terminal-outcome", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		auth := mustAuthorize(t, c, "t1", 3, "op-auth-t1")
		mustCommitOutcome(t, c, "t1", 4, auth.OperationID, DeliveryOutcomeCompleted, "merged")
		req := authorizeRequest(c, "t1", preconditionOf(1, 5))
		if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-terminal", req), req); !errors.Is(err, ErrConflict) {
			t.Fatalf("authorize after terminal outcome = %v, want ErrConflict", err)
		}
	})

	// Malformed issuance intents fail validation without mutating anything.
	t.Run("invalid-intents", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")

		unknown := authorizeRequest(c, "t1", preconditionOf(1, 3))
		unknown.Kind = "capability-tier"
		if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-unknown-kind", unknown), unknown); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("unknown kind = %v, want ErrInvalidInput", err)
		}

		badIdentity := authorizeRequest(c, "t1", preconditionOf(1, 3))
		badIdentity.Identity.HeadSHA = ""
		if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-bad-identity", badIdentity), badIdentity); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid identity = %v, want ErrInvalidInput", err)
		}

		headMismatch := authorizeRequest(c, "t1", preconditionOf(1, 3))
		headMismatch.Identity.HeadSHA = "9999888877776666555544443333222211110000"
		if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-head-mismatch", headMismatch), headMismatch); !errors.Is(err, ErrPrecondition) {
			t.Fatalf("head mismatch = %v, want ErrPrecondition", err)
		}

		noPreconditions := authorizeRequest(c, "t1", preconditionOf(1, 3))
		noPreconditions.Preconditions = nil
		if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-no-prec", noPreconditions), noPreconditions); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("missing preconditions = %v, want ErrInvalidInput", err)
		}

		dupPreconditions := authorizeRequest(c, "t1", preconditionOf(1, 3))
		dupPreconditions.Preconditions = []DeliveryPrecondition{DeliveryPreconditionPRMergeable, DeliveryPreconditionPRMergeable}
		if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-dup-prec", dupPreconditions), dupPreconditions); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("duplicate preconditions = %v, want ErrInvalidInput", err)
		}

		mergeWithState := authorizeRequest(c, "t1", preconditionOf(1, 3))
		mergeWithState.ExpectedState = &DeliveryExpectedState{Ref: "refs/heads/main", OldSHA: "1111222233334444555566667777888899990000"}
		if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-merge-state", mergeWithState), mergeWithState); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("provider-merge with expected state = %v, want ErrInvalidInput", err)
		}

		mutationWithoutState := authorizeRequest(c, "t1", preconditionOf(1, 3))
		mutationWithoutState.Kind = DeliveryAuthorizationRepositoryMutation
		if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-mutation-nostate", mutationWithoutState), mutationWithoutState); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("repository-mutation without expected state = %v, want ErrInvalidInput", err)
		}

		badState := authorizeRequest(c, "t1", preconditionOf(1, 3))
		badState.Kind = DeliveryAuthorizationRepositoryMutation
		badState.ExpectedState = &DeliveryExpectedState{Ref: "refs/heads/main", OldSHA: "bad sha/with/separator"}
		if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-bad-state", badState), badState); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("repository-mutation with unsafe old SHA = %v, want ErrInvalidInput", err)
		}

		// Nothing was committed by any invalid intent.
		agg, err := c.Get(mustTaskID(t, "t1"))
		if err != nil {
			t.Fatal(err)
		}
		if agg.Revision != 3 {
			t.Fatalf("invalid intents advanced revision to %d, want 3", agg.Revision)
		}
		if _, err := c.DeliveryAuthorization(mustTaskID(t, "t1")); !errors.Is(err, ErrNotFound) {
			t.Fatalf("invalid intents created an authorization: %v", err)
		}
	})
}

// TestCanonicalDeliveryAuthorizationReplayAndConflict proves same
// Operation ID + digest replays the durable record idempotently and a reused
// Operation ID with a different intent conflicts.
func TestCanonicalDeliveryAuthorizationReplayAndConflict(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")

	req := authorizeRequest(c, "t1", preconditionOf(1, 3))
	op := mustOperation(t, "op-auth-replay", req)
	first, err := c.AuthorizeDelivery(op, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.AuthorizeDelivery(op, req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !second.Replayed || first.Replayed {
		t.Fatalf("replay flags first=%v second=%v, want false/true", first.Replayed, second.Replayed)
	}
	if second.Authorization.OperationID != first.Authorization.OperationID || second.Authorization.Revision != first.Authorization.Revision || second.Authorization.Identity != first.Authorization.Identity {
		t.Fatalf("replay record differs: %+v vs %+v", second.Authorization, first.Authorization)
	}

	// Same Operation ID with a different intent (repository-mutation kind).
	other := authorizeRequest(c, "t1", preconditionOf(1, 3))
	other.Kind = DeliveryAuthorizationRepositoryMutation
	other.ExpectedState = &DeliveryExpectedState{Ref: "refs/heads/main", OldSHA: "1111222233334444555566667777888899990000"}
	other.Preconditions = []DeliveryPrecondition{DeliveryPreconditionWorktreeClean}
	reused, err := domain.NewOperation(op.ID, other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AuthorizeDelivery(reused, other); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("reused op with different intent = %v, want ErrOperationConflict", err)
	}
}

// TestCanonicalDeliveryCurrencyInvalidation proves the currency read reports
// typed invalid reasons when the task revision, phase, generation, binding
// lease/fence/path/head, transfer reservation, authorization revocation,
// identity/head, or delivery holds change after issuance, and that an
// unrelated non-matching hold does not invalidate currency.
func TestCanonicalDeliveryCurrencyInvalidation(t *testing.T) {
	// Setup helper: a fresh home with an issued authorization.
	setup := func(t *testing.T) (*Canonical, string) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		mustAuthorize(t, c, "t1", 3, "op-auth-t1")
		cur, err := c.DeliveryCurrency(mustTaskID(t, "t1"))
		if err != nil {
			t.Fatal(err)
		}
		if !cur.Valid || len(cur.Reasons) != 0 {
			t.Fatalf("fresh currency = %+v, want valid", cur)
		}
		return c, "t1"
	}

	// Revision: a subsequent unrelated Task mutation advances the revision.
	t.Run("revision", func(t *testing.T) {
		c, taskID := setup(t)
		rewriteTaskDocForTest(t, c, taskID, func(agg Aggregate) Aggregate {
			agg.Revision++
			return agg
		})
		cur, err := c.DeliveryCurrency(mustTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		if cur.Valid || !hasCurrencyReason(cur, DeliveryCurrencyRevision) {
			t.Fatalf("revision currency = %+v, want revision-mismatch", cur)
		}
	})

	// Phase: the task leaves working.
	t.Run("phase", func(t *testing.T) {
		c, taskID := setup(t)
		rewriteTaskDocForTest(t, c, taskID, func(agg Aggregate) Aggregate {
			agg.Phase = PhaseBlocked
			return agg
		})
		cur, err := c.DeliveryCurrency(mustTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		if cur.Valid || !hasCurrencyReason(cur, DeliveryCurrencyPhase) {
			t.Fatalf("phase currency = %+v, want phase-mismatch", cur)
		}
	})

	// Generation: a reopen creates a new generation the authorization does
	// not bind.
	t.Run("generation", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		mustAuthorize(t, c, "t1", 3, "op-auth-t1")
		complete := CanonicalCompleteRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4), To: PhaseDone, Reason: "done"}
		if _, err := c.Complete(mustOperation(t, "op-complete-gen", complete), complete); err != nil {
			t.Fatal(err)
		}
		reopen := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 5), Reason: "reopen"}
		if _, err := c.Reopen(mustOperation(t, "op-reopen-gen", reopen), reopen); err != nil {
			t.Fatal(err)
		}
		cur, err := c.DeliveryCurrency(mustTaskID(t, "t1"))
		if err != nil {
			t.Fatal(err)
		}
		if cur.Valid || !hasCurrencyReason(cur, DeliveryCurrencyGeneration) {
			t.Fatalf("generation currency = %+v, want generation-mismatch", cur)
		}
	})

	// Binding lease change invalidates via the binding digest.
	t.Run("binding-lease", func(t *testing.T) {
		c, taskID := setup(t)
		rewriteTaskDocForTest(t, c, taskID, func(agg Aggregate) Aggregate {
			agg.Endpoint.LeaseID = "lease-changed"
			return agg
		})
		cur, err := c.DeliveryCurrency(mustTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		if cur.Valid || !hasCurrencyReason(cur, DeliveryCurrencyBindingDigest) {
			t.Fatalf("binding lease currency = %+v, want binding-digest", cur)
		}
	})

	// Binding path change invalidates via the binding digest.
	t.Run("binding-path", func(t *testing.T) {
		c, taskID := setup(t)
		rewriteTaskDocForTest(t, c, taskID, func(agg Aggregate) Aggregate {
			agg.Worktree.Path = "/changed/path"
			return agg
		})
		cur, err := c.DeliveryCurrency(mustTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		if cur.Valid || !hasCurrencyReason(cur, DeliveryCurrencyBindingDigest) {
			t.Fatalf("binding path currency = %+v, want binding-digest", cur)
		}
	})

	// Identity/head mismatch: the bound worktree head moved after issuance.
	t.Run("identity-head", func(t *testing.T) {
		c, taskID := setup(t)
		rewriteTaskDocForTest(t, c, taskID, func(agg Aggregate) Aggregate {
			agg.Worktree.Head = "9999888877776666555544443333222211110000"
			return agg
		})
		cur, err := c.DeliveryCurrency(mustTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		if cur.Valid || !hasCurrencyReason(cur, DeliveryCurrencyIdentityHead) {
			t.Fatalf("identity/head currency = %+v, want identity-head", cur)
		}
	})

	// Active transfer reservation invalidates currency.
	t.Run("reservation", func(t *testing.T) {
		c, taskID := setup(t)
		mustReserveTransfer(t, c, taskID, preconditionOf(1, 4), "dest-home")
		cur, err := c.DeliveryCurrency(mustTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		if cur.Valid || !hasCurrencyReason(cur, DeliveryCurrencyReservation) {
			t.Fatalf("reservation currency = %+v, want transfer-reserved", cur)
		}
	})

	// Revocation invalidates currency with the revoked reason.
	t.Run("revoked", func(t *testing.T) {
		c, taskID := setup(t)
		mustRevoke(t, c, taskID, 4, "op-auth-t1", "abandoned")
		cur, err := c.DeliveryCurrency(mustTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		if cur.Valid || !hasCurrencyReason(cur, DeliveryCurrencyRevoked) {
			t.Fatalf("revoked currency = %+v, want revoked", cur)
		}
	})

	// A matching delivery hold add invalidates via the digest.
	t.Run("hold-add", func(t *testing.T) {
		c, taskID := setup(t)
		hold := CanonicalAddHoldRequest{HomeID: c.HomeID(), HoldID: "hold-delivery", Scope: DispatchHoldScope{TaskIDs: []string{taskID}}, Actions: []DispatchAction{DispatchActionDelivery}, Reason: "freeze"}
		if _, err := c.AddHold(mustOperation(t, "op-add-hold-currency", hold), hold); err != nil {
			t.Fatal(err)
		}
		cur, err := c.DeliveryCurrency(mustTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		if cur.Valid || !hasCurrencyReason(cur, DeliveryCurrencyMatchingHold) || !hasCurrencyReason(cur, DeliveryCurrencyHoldsDigest) {
			t.Fatalf("hold add currency = %+v, want matching-hold + holds-digest", cur)
		}
	})

	// A matching delivery hold release also invalidates via the digest even
	// though the Task revision is unchanged: the released hold's release
	// state is part of the relevant holds digest.
	t.Run("hold-release", func(t *testing.T) {
		c, taskID := setup(t)
		hold := CanonicalAddHoldRequest{HomeID: c.HomeID(), HoldID: "hold-delivery", Scope: DispatchHoldScope{TaskIDs: []string{taskID}}, Actions: []DispatchAction{DispatchActionDelivery}, Reason: "freeze"}
		if _, err := c.AddHold(mustOperation(t, "op-add-hold-release", hold), hold); err != nil {
			t.Fatal(err)
		}
		release := CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "hold-delivery", Reason: "resume"}
		if _, err := c.ReleaseHold(mustOperation(t, "op-release-hold-release", release), release); err != nil {
			t.Fatal(err)
		}
		cur, err := c.DeliveryCurrency(mustTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		if cur.Valid || !hasCurrencyReason(cur, DeliveryCurrencyHoldsDigest) {
			t.Fatalf("hold release currency = %+v, want holds-digest", cur)
		}
		if hasCurrencyReason(cur, DeliveryCurrencyMatchingHold) {
			t.Fatalf("hold release currency still reports matching-hold: %+v", cur)
		}
	})

	// An unrelated non-matching hold (different action) does not invalidate
	// currency.
	t.Run("unrelated-hold", func(t *testing.T) {
		c, taskID := setup(t)
		hold := CanonicalAddHoldRequest{HomeID: c.HomeID(), HoldID: "hold-start", Scope: DispatchHoldScope{TaskIDs: []string{taskID}}, Actions: []DispatchAction{DispatchActionStart}, Reason: "freeze start"}
		if _, err := c.AddHold(mustOperation(t, "op-add-hold-unrelated", hold), hold); err != nil {
			t.Fatal(err)
		}
		cur, err := c.DeliveryCurrency(mustTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		if !cur.Valid || len(cur.Reasons) != 0 {
			t.Fatalf("unrelated hold currency = %+v, want valid", cur)
		}
	})

	// A delivery hold scoped to another task does not invalidate currency.
	t.Run("other-task-hold", func(t *testing.T) {
		c, taskID := setup(t)
		mustCreate(t, c, "other")
		hold := CanonicalAddHoldRequest{HomeID: c.HomeID(), HoldID: "hold-other", Scope: DispatchHoldScope{TaskIDs: []string{"other"}}, Actions: []DispatchAction{DispatchActionDelivery}, Reason: "freeze other"}
		if _, err := c.AddHold(mustOperation(t, "op-add-hold-other", hold), hold); err != nil {
			t.Fatal(err)
		}
		cur, err := c.DeliveryCurrency(mustTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		if !cur.Valid || len(cur.Reasons) != 0 {
			t.Fatalf("other-task hold currency = %+v, want valid", cur)
		}
	})
}

func hasCurrencyReason(cur DeliveryCurrency, reason DeliveryCurrencyReason) bool {
	for _, r := range cur.Reasons {
		if r == reason {
			return true
		}
	}
	return false
}

// TestCanonicalDeliveryCurrencyReadOnly proves the currency read never
// mutates state and never creates receipts, even when it reports invalid
// reasons.
func TestCanonicalDeliveryCurrencyReadOnly(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")
	mustAuthorize(t, c, "t1", 3, "op-auth-t1")

	if _, err := c.DeliveryCurrency(mustTaskID(t, "t1")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeliveryCurrency(mustTaskID(t, "missing")); err != nil {
		t.Fatalf("currency on missing task = %v, want not-found result", err)
	}

	// No receipts were created by any currency read.
	ids, err := c.listHoldIDs()
	if err != nil {
		t.Fatal(err)
	}
	_ = ids
	path, err := c.h.Path(home.RootState, receiptsDir)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "currency") {
			t.Fatalf("currency read created receipt %s", e.Name())
		}
	}
}

// TestCanonicalDeliveryRevokeReauthorizePreservesAudit proves revocation
// preserves the prior authorization evidence and a later distinct
// authorization is a new issuance with a distinct identity; both records stay
// readable and the record never holds a current/superseded ambiguity.
func TestCanonicalDeliveryRevokeReauthorizePreservesAudit(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")

	auth1 := mustAuthorize(t, c, "t1", 3, "op-auth-t1")
	revoked := mustRevoke(t, c, "t1", 4, auth1.OperationID, "delivery abandoned")
	if revoked.Revoked == nil {
		t.Fatalf("revocation evidence missing: %+v", revoked)
	}
	if revoked.Revoked.OperationID != "op-revoke-t1" || revoked.Revoked.Reason != "delivery abandoned" {
		t.Fatalf("revocation evidence = %+v", revoked.Revoked)
	}

	// A distinct identity: different PR number and head ref, same head SHA
	// (the bound worktree head must match the authorized identity head).
	req2 := authorizeRequest(c, "t1", preconditionOf(1, 5))
	req2.Identity.Number = 43
	req2.Identity.URL = "https://github.com/minhtri2710/munsu/pull/43"
	req2.Identity.HeadRef = "feature/next"
	res2, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-t1-2", req2), req2)
	if err != nil {
		t.Fatalf("re-authorize: %v", err)
	}
	auth2 := res2.Authorization
	if auth2.OperationID == auth1.OperationID || auth2.Identity.Number != 43 {
		t.Fatalf("second authorization = %+v, want distinct identity", auth2)
	}
	if auth2.Revoked != nil {
		t.Fatalf("second authorization marked revoked: %+v", auth2)
	}

	// The prior revoked record is still readable by its operation identity.
	prior, err := c.DeliveryAuthorizationByOperation(mustTaskID(t, "t1"), auth1.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if prior.Revoked == nil || prior.Revoked.OperationID != "op-revoke-t1" || prior.Identity.Number != 42 {
		t.Fatalf("prior revoked record = %+v", prior)
	}
	// The current read returns the second authorization.
	cur, err := c.DeliveryAuthorization(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if cur.OperationID != auth2.OperationID {
		t.Fatalf("current authorization = %+v, want %s", cur, auth2.OperationID)
	}

	// Revoking with the wrong authorization identity fails closed.
	wrong := CanonicalRevokeDeliveryRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 6), AuthorizationOperationID: "op-unknown-auth", Reason: "x"}
	if _, err := c.RevokeDeliveryAuthorization(mustOperation(t, "op-revoke-wrong", wrong), wrong); !errors.Is(err, ErrConflict) {
		t.Fatalf("revoke with wrong identity = %v, want ErrConflict", err)
	}

	// Revoking the active authorization succeeds, and a repeated revoke of
	// the already-revoked authorization conflicts (no active auth remains).
	revokeAuth2 := CanonicalRevokeDeliveryRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 6), AuthorizationOperationID: auth2.OperationID, Reason: "superseded"}
	if res, err := c.RevokeDeliveryAuthorization(mustOperation(t, "op-revoke-auth2", revokeAuth2), revokeAuth2); err != nil {
		t.Fatalf("revoke auth2: %v", err)
	} else if res.Authorization.Revoked == nil {
		t.Fatalf("auth2 not marked revoked: %+v", res.Authorization)
	}
	again := CanonicalRevokeDeliveryRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 7), AuthorizationOperationID: auth2.OperationID, Reason: "x"}
	if _, err := c.RevokeDeliveryAuthorization(mustOperation(t, "op-revoke-again", again), again); !errors.Is(err, ErrConflict) {
		t.Fatalf("second revoke = %v, want ErrConflict", err)
	}

	// Revoke replay is idempotent.
	revokeReq := CanonicalRevokeDeliveryRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4), AuthorizationOperationID: auth1.OperationID, Reason: "delivery abandoned"}
	revokeOp := mustOperation(t, "op-revoke-t1", revokeReq)
	replayed, err := c.RevokeDeliveryAuthorization(revokeOp, revokeReq)
	if err != nil {
		t.Fatalf("revoke replay: %v", err)
	}
	if !replayed.Replayed || replayed.Authorization.OperationID != auth1.OperationID {
		t.Fatalf("revoke replay = %+v", replayed)
	}
}

// TestCanonicalDeliveryOutcomeLifecycle proves the closed-set outcome statuses
// validate, replay idempotently, conflict when distinct/incompatible, and
// preserve prior outcome evidence across retryable re-attempts.
func TestCanonicalDeliveryOutcomeLifecycle(t *testing.T) {
	// completed binds the journal operation, authorization identity,
	// generation, evidence, detail, and commit time.
	t.Run("completed", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		auth := mustAuthorize(t, c, "t1", 3, "op-auth-t1")

		req := CanonicalDeliveryOutcomeRequest{
			HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4),
			AuthorizationOperationID: auth.OperationID,
			Status:                   DeliveryOutcomeCompleted,
			Detail:                   "provider merge succeeded",
			MergedSHA:                "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		}
		res, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-completed", req), req)
		if err != nil {
			t.Fatalf("CommitDeliveryOutcome(completed): %v", err)
		}
		if res.Outcome.Status != DeliveryOutcomeCompleted || res.Outcome.Detail != "provider merge succeeded" || res.Outcome.MergedSHA != "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef" {
			t.Fatalf("outcome = %+v", res.Outcome)
		}
		if res.Outcome.AuthorizationOperationID != auth.OperationID || res.Outcome.OperationID != "op-outcome-completed" || res.Outcome.Generation != 1 {
			t.Fatalf("outcome bindings = %+v", res.Outcome)
		}
		if res.Outcome.CommittedAt <= 0 {
			t.Fatalf("outcome missing commit time")
		}
		agg, err := c.Get(mustTaskID(t, "t1"))
		if err != nil {
			t.Fatal(err)
		}
		if agg.Revision != 5 {
			t.Fatalf("aggregate revision after outcome = %d, want 5", agg.Revision)
		}

		// The narrow read returns the committed outcome.
		read, err := c.DeliveryOutcome(mustTaskID(t, "t1"))
		if err != nil {
			t.Fatal(err)
		}
		if read.Status != DeliveryOutcomeCompleted || read.OperationID != res.Outcome.OperationID {
			t.Fatalf("DeliveryOutcome read = %+v", read)
		}

		// Same op + digest replays idempotently.
		replayed, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-completed", req), req)
		if err != nil {
			t.Fatalf("outcome replay: %v", err)
		}
		if !replayed.Replayed || replayed.Outcome.OperationID != res.Outcome.OperationID {
			t.Fatalf("outcome replay = %+v", replayed)
		}

		// A distinct incompatible outcome conflicts after a terminal status.
		other := req
		other.Precondition = preconditionOf(1, 5)
		other.Status = DeliveryOutcomeRetryable
		if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-after-terminal", other), other); !errors.Is(err, ErrConflict) {
			t.Fatalf("distinct outcome after completed = %v, want ErrConflict", err)
		}

		// Reusing the completed op ID with a different intent conflicts.
		changed := req
		changed.Detail = "different detail"
		reused, err := domain.NewOperation(mustOperation(t, "op-outcome-completed", req).ID, changed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.CommitDeliveryOutcome(reused, changed); !errors.Is(err, ErrOperationConflict) {
			t.Fatalf("reused outcome op with different intent = %v, want ErrOperationConflict", err)
		}
	})

	// retryable permits a later distinct authorized attempt; the record
	// preserves both outcomes.
	t.Run("retryable-then-completed", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		auth1 := mustAuthorize(t, c, "t1", 3, "op-auth-t1")
		mustCommitOutcome(t, c, "t1", 4, auth1.OperationID, DeliveryOutcomeRetryable, "provider unreachable")
		// The first outcome advanced the revision; the authorization is no
		// longer current, so Fleet revokes and re-authorizes for the retry.
		mustRevoke(t, c, "t1", 5, auth1.OperationID, "retry")
		auth2 := mustAuthorize(t, c, "t1", 6, "op-auth-t1-2")
		mustCommitOutcome(t, c, "t1", 7, auth2.OperationID, DeliveryOutcomeCompleted, "merge confirmed")

		out, err := c.DeliveryOutcome(mustTaskID(t, "t1"))
		if err != nil {
			t.Fatal(err)
		}
		if out.Status != DeliveryOutcomeCompleted || out.AuthorizationOperationID != auth2.OperationID {
			t.Fatalf("current outcome = %+v", out)
		}
		// The prior retryable outcome remains readable by its operation.
		prior, err := findOutcomeForTest(c, "t1", "op-outcome-t1-retryable")
		if err != nil {
			t.Fatalf("prior outcome lost: %v", err)
		}
		if prior.Status != DeliveryOutcomeRetryable || prior.AuthorizationOperationID != auth1.OperationID {
			t.Fatalf("prior outcome = %+v", prior)
		}
	})

	// partial and remote-unknown are terminal: a distinct outcome conflicts.
	for _, status := range []DeliveryOutcomeStatus{DeliveryOutcomePartial, DeliveryOutcomeRemoteUnknown} {
		t.Run(string(status), func(t *testing.T) {
			c, _, _ := newTestCanonical(t)
			mustDeliveryTask(t, c, "t1")
			auth := mustAuthorize(t, c, "t1", 3, "op-auth-t1")
			mustCommitOutcome(t, c, "t1", 4, auth.OperationID, status, "terminal detail")
			req := CanonicalDeliveryOutcomeRequest{
				HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 5),
				AuthorizationOperationID: auth.OperationID,
				Status:                   DeliveryOutcomeRetryable,
				Detail:                   "distinct",
			}
			if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-distinct-"+string(status), req), req); !errors.Is(err, ErrConflict) {
				t.Fatalf("distinct outcome after %s = %v, want ErrConflict", status, err)
			}
			// remote-unknown also blocks a new authorization.
			if status == DeliveryOutcomeRemoteUnknown {
				ar := authorizeRequest(c, "t1", preconditionOf(1, 5))
				if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-after-unknown", ar), ar); !errors.Is(err, ErrConflict) {
					t.Fatalf("authorize after remote-unknown = %v, want ErrConflict", err)
				}
			}
		})
	}

	// Outcome prerequisites fail closed: no authorization, wrong
	// authorization identity, revoked authorization, non-current
	// authorization, stale precondition, non-current generation.
	t.Run("prerequisites", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")

		// No authorization.
		req := CanonicalDeliveryOutcomeRequest{
			HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 3),
			AuthorizationOperationID: "op-unknown", Status: DeliveryOutcomeCompleted, Detail: "x",
		}
		if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-no-auth", req), req); !errors.Is(err, ErrConflict) {
			t.Fatalf("outcome without authorization = %v, want ErrConflict", err)
		}

		auth := mustAuthorize(t, c, "t1", 3, "op-auth-t1")

		// Wrong authorization identity.
		wrong := req
		wrong.Precondition = preconditionOf(1, 4)
		wrong.AuthorizationOperationID = "op-other-auth"
		if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-wrong-auth", wrong), wrong); !errors.Is(err, ErrConflict) {
			t.Fatalf("outcome with wrong authorization identity = %v, want ErrConflict", err)
		}

		// Non-current authorization: an unrelated task mutation (block)
		// changed the phase after issuance.
		block := CanonicalBlockRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4), Detail: "d", Reason: "block"}
		if _, err := c.Block(mustOperation(t, "op-block-currency", block), block); err != nil {
			t.Fatal(err)
		}
		stale := req
		stale.Precondition = preconditionOf(1, 5)
		stale.AuthorizationOperationID = auth.OperationID
		if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-stale-currency", stale), stale); !errors.Is(err, ErrPrecondition) {
			t.Fatalf("outcome with non-current authorization = %v, want ErrPrecondition", err)
		}

		// Stale task precondition fails closed as a typed conflict.
		unblock := CanonicalUnblockRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 5), Reason: "unblock"}
		if _, err := c.Unblock(mustOperation(t, "op-unblock-currency", unblock), unblock); err != nil {
			t.Fatal(err)
		}
		stalePrec := req
		stalePrec.Precondition = preconditionOf(1, 9)
		stalePrec.AuthorizationOperationID = auth.OperationID
		if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-stale-prec", stalePrec), stalePrec); !errors.Is(err, domain.ErrStalePrecondition) {
			t.Fatalf("outcome with stale precondition = %v, want domain.ErrStalePrecondition", err)
		}

		// Non-current generation fails closed.
		complete := CanonicalCompleteRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 6), To: PhaseDone, Reason: "done"}
		if _, err := c.Complete(mustOperation(t, "op-complete-outcome-gen", complete), complete); err != nil {
			t.Fatal(err)
		}
		reopen := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 7), Reason: "reopen"}
		if _, err := c.Reopen(mustOperation(t, "op-reopen-outcome-gen", reopen), reopen); err != nil {
			t.Fatal(err)
		}
		genStale := req
		genStale.Precondition = preconditionOf(1, 7)
		genStale.AuthorizationOperationID = auth.OperationID
		if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-stale-gen", genStale), genStale); !errors.Is(err, domain.ErrStalePrecondition) {
			t.Fatalf("outcome against non-current generation = %v, want domain.ErrStalePrecondition", err)
		}
	})

	// Outcome intents validate: unknown status, empty detail, unsafe evidence.
	t.Run("invalid-intents", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		auth := mustAuthorize(t, c, "t1", 3, "op-auth-t1")

		unknown := CanonicalDeliveryOutcomeRequest{
			HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4),
			AuthorizationOperationID: auth.OperationID, Status: "exploded", Detail: "x",
		}
		if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-unknown", unknown), unknown); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("unknown status = %v, want ErrInvalidInput", err)
		}
		noDetail := unknown
		noDetail.Status = DeliveryOutcomeCompleted
		noDetail.Detail = "  "
		if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-nodetail", noDetail), noDetail); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("empty detail = %v, want ErrInvalidInput", err)
		}
		badSHA := unknown
		badSHA.Status = DeliveryOutcomeCompleted
		badSHA.Detail = "x"
		badSHA.MergedSHA = "not/a/real/sha"
		if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-badsha", badSHA), badSHA); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("unsafe merged SHA = %v, want ErrInvalidInput", err)
		}
	})
}

// findOutcomeForTest locates a committed outcome by its operation identity
// through the task's delivery record read path.
func findOutcomeForTest(c *Canonical, taskID, operationID string) (DeliveryOutcome, error) {
	rec, exists, err := c.readDeliveryRecord(taskID)
	if err != nil || !exists {
		return DeliveryOutcome{}, err
	}
	out, ok := findOutcome(rec, operationID)
	if !ok {
		return DeliveryOutcome{}, ErrNotFound
	}
	return out, nil
}

// TestCanonicalDeliverySchemaRejectsMalformedRecords proves malformed or
// unknown-schema delivery records fail closed and never bump the current
// pre-public schema identity.
func TestCanonicalDeliverySchemaRejectsMalformedRecords(t *testing.T) {
	plant := func(t *testing.T, c *Canonical, data string) {
		t.Helper()
		if err := writePathForTest(t, c, deliveryKey("t1"), []byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	read := func(t *testing.T, c *Canonical) error {
		if _, err := c.DeliveryAuthorization(mustTaskID(t, "t1")); err == nil {
			return errors.New("DeliveryAuthorization accepted malformed record")
		}
		if _, err := c.DeliveryCurrency(mustTaskID(t, "t1")); err == nil {
			return errors.New("DeliveryCurrency accepted malformed record")
		}
		return nil
	}

	t.Run("unknown-schema", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		plant(t, c, `{"schema_version":"munsu.task-authority/v2","task_id":"t1"}`)
		if err := read(t, c); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("malformed-json", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		plant(t, c, `{not json`)
		if err := read(t, c); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("superseded-active-authorization", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		auth := mustAuthorize(t, c, "t1", 3, "op-auth-t1")
		rec, exists, err := c.readDeliveryRecord("t1")
		if err != nil || !exists {
			t.Fatal("read delivery record")
		}
		bad := rec.clone()
		bad.Authorizations = append(bad.Authorizations, bad.Authorizations[0].clone())
		data, err := json.Marshal(bad)
		if err != nil {
			t.Fatal(err)
		}
		if err := writePathForTest(t, c, deliveryKey("t1"), data); err != nil {
			t.Fatal(err)
		}
		_ = auth
		if err := read(t, c); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("outcome-after-terminal", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		auth := mustAuthorize(t, c, "t1", 3, "op-auth-t1")
		mustCommitOutcome(t, c, "t1", 4, auth.OperationID, DeliveryOutcomeCompleted, "done")
		rec, exists, err := c.readDeliveryRecord("t1")
		if err != nil || !exists {
			t.Fatal("read delivery record")
		}
		bad := rec.clone()
		bad.Outcomes = append(bad.Outcomes, bad.Outcomes[0].clone())
		data, err := json.Marshal(bad)
		if err != nil {
			t.Fatal(err)
		}
		if err := writePathForTest(t, c, deliveryKey("t1"), data); err != nil {
			t.Fatal(err)
		}
		if err := read(t, c); err != nil {
			t.Fatal(err)
		}
	})
}
