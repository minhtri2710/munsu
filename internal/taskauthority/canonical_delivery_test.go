package taskauthority

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

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

// mustRevoke revokes the current authorization of the task under the given
// revocation operation identity and returns the immutable revocation evidence.
func mustRevoke(t *testing.T, c *Canonical, taskID string, rev uint64, authOpID, reason, opID string) DeliveryRevocation {
	t.Helper()
	req := CanonicalRevokeDeliveryRequest{
		HomeID:                   c.HomeID(),
		TaskID:                   mustTaskID(t, taskID),
		Precondition:             preconditionOf(1, rev),
		AuthorizationOperationID: authOpID,
		Reason:                   reason,
	}
	res, err := c.RevokeDeliveryAuthorization(mustOperation(t, opID, req), req)
	if err != nil {
		t.Fatalf("RevokeDeliveryAuthorization(%s): %v", taskID, err)
	}
	if res.Replayed {
		t.Fatalf("fresh revoke(%s) marked replayed", taskID)
	}
	return res.Revocation
}

func mustCommitOutcome(t *testing.T, c *Canonical, taskID string, rev uint64, authOpID string, status DeliveryOutcomeStatus, detail, opID string) DeliveryOutcome {
	t.Helper()
	req := CanonicalDeliveryOutcomeRequest{
		HomeID:                   c.HomeID(),
		TaskID:                   mustTaskID(t, taskID),
		Precondition:             preconditionOf(1, rev),
		AuthorizationOperationID: authOpID,
		Status:                   status,
		Detail:                   detail,
	}
	res, err := c.CommitDeliveryOutcome(mustOperation(t, opID, req), req)
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

// countDeliveryEvidenceFiles counts the committed immutable evidence
// documents under one delivery subdirectory of the task.
func countDeliveryEvidenceFiles(t *testing.T, c *Canonical, subdir string) int {
	t.Helper()
	p, err := c.h.Path(home.RootState, deliveryDir+"/t1/"+subdir)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0
		}
		t.Fatal(err)
	}
	return len(entries)
}

// TestCanonicalDeliveryAuthorizationIssuancePinsPostIssuanceState proves a
// successful issuance records the committed post-issuance Task revision, the
// exact working phase, ownership, typed identity/head, kind, expected state,
// binding digest, holds digest, and preconditions, and commits an immutable
// evidence document plus a bounded index pointer.
func TestCanonicalDeliveryAuthorizationIssuancePinsPostIssuanceState(t *testing.T) {
	c, h, _ := newTestCanonical(t)
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

	// The immutable evidence document is committed at its exact key and the
	// bounded index points at it.
	evData, ok, err := readDocForTest(h, deliveryAuthorizationKey("t1", "op-auth-t1"))
	if err != nil || !ok {
		t.Fatalf("read issuance evidence: ok=%v err=%v", ok, err)
	}
	var onDisk DeliveryAuthorization
	if err := json.Unmarshal(evData, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.OperationID != auth.OperationID || onDisk.Revision != auth.Revision || onDisk.Identity != auth.Identity {
		t.Fatalf("on-disk evidence = %+v, want the committed record", onDisk)
	}
	idxData, ok, err := readDocForTest(h, deliveryCurrentKey("t1"))
	if err != nil || !ok {
		t.Fatalf("read delivery index: ok=%v err=%v", ok, err)
	}
	var index DeliveryIndex
	if err := json.Unmarshal(idxData, &index); err != nil {
		t.Fatal(err)
	}
	if index.AuthorizationOpID != "op-auth-t1" || index.TaskID != "t1" || index.SchemaVersion != TaskAuthoritySchema {
		t.Fatalf("delivery index = %+v", index)
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
			// A retired task now carries an active cleanup claim, so the delivery
			// mutation fails closed on the claim fence (ErrConflict) rather than
			// the phase gate (ErrPrecondition); either way delivery is rejected.
			if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-fail-"+tc.name, req), req); err == nil || (!errors.Is(err, ErrPrecondition) && !errors.Is(err, ErrConflict)) {
				t.Fatalf("authorize on %s task = %v, want ErrPrecondition or ErrConflict", tc.name, err)
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
		mustCommitOutcome(t, c, "t1", 4, auth.OperationID, DeliveryOutcomeCompleted, "merged", "op-outcome-terminal")
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

		unknownKind := authorizeRequest(c, "t1", preconditionOf(1, 3))
		unknownKind.Kind = DeliveryAuthorizationKind("not-a-kind")
		if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-unknown-kind", unknownKind), unknownKind); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("unknown authorization kind = %v, want ErrInvalidInput", err)
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
// Operation ID + digest replays the durable immutable evidence idempotently
// and a reused Operation ID with a different intent conflicts.
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

	// Same Operation ID with a different intent (different precondition set).
	other := authorizeRequest(c, "t1", preconditionOf(1, 3))
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
		mustRevoke(t, c, taskID, 4, "op-auth-t1", "abandoned", "op-revoke-t1")
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

func TestIndexedDeliveryQueriesWaitForTaskScopeLock(t *testing.T) {
	queries := []struct {
		name string
		call func(*Canonical, domain.TaskID) error
	}{
		{name: "authorization", call: func(c *Canonical, id domain.TaskID) error {
			_, err := c.DeliveryAuthorization(id)
			return err
		}},
		{name: "outcome", call: func(c *Canonical, id domain.TaskID) error {
			_, err := c.DeliveryOutcome(id)
			return err
		}},
		{name: "currency", call: func(c *Canonical, id domain.TaskID) error {
			_, err := c.DeliveryCurrency(id)
			return err
		}},
	}
	for _, query := range queries {
		t.Run(query.name, func(t *testing.T) {
			c, _, _ := newTestCanonical(t)
			mustDeliveryTask(t, c, "t1")
			mustAuthorize(t, c, "t1", 3, "op-auth-lock-"+query.name)
			id := mustTaskID(t, "t1")
			lk, err := c.h.Lock(taskScope(id.Value()))
			if err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			result := make(chan error, 1)
			go func() {
				close(started)
				result <- query.call(c, id)
			}()
			<-started
			select {
			case err := <-result:
				t.Fatalf("%s query returned while task scope was locked: %v", query.name, err)
			case <-time.After(100 * time.Millisecond):
			}
			if err := lk.Release(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if query.name == "outcome" {
					if !errors.Is(err, ErrNotFound) {
						t.Fatalf("outcome query after lock release: %v, want ErrNotFound", err)
					}
				} else if err != nil {
					t.Fatalf("%s query after lock release: %v", query.name, err)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("%s query did not complete after task scope unlock", query.name)
			}
		})
	}
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
// mutates state, never creates receipts, and never touches the bounded index
// or the immutable evidence documents.
func TestCanonicalDeliveryCurrencyReadOnly(t *testing.T) {
	c, h, _ := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")
	mustAuthorize(t, c, "t1", 3, "op-auth-t1")

	idxBefore, ok, err := readDocForTest(h, deliveryCurrentKey("t1"))
	if err != nil || !ok {
		t.Fatalf("read index before: ok=%v err=%v", ok, err)
	}
	if _, err := c.DeliveryCurrency(mustTaskID(t, "t1")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeliveryCurrency(mustTaskID(t, "missing")); err != nil {
		t.Fatalf("currency on missing task = %v, want not-found result", err)
	}
	if _, err := c.DeliveryCurrency(mustTaskID(t, "t1")); err != nil {
		t.Fatal(err)
	}

	idxAfter, ok, err := readDocForTest(h, deliveryCurrentKey("t1"))
	if err != nil || !ok {
		t.Fatalf("read index after: ok=%v err=%v", ok, err)
	}
	if !bytes.Equal(idxBefore, idxAfter) {
		t.Fatalf("currency read mutated the delivery index")
	}
	// No receipts were created by any currency read.
	path, err := h.Path(home.RootState, receiptsDir)
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
// commits immutable evidence (never rewriting the issuance document) and a
// later distinct authorization is a new issuance with a distinct identity;
// every authorization and revocation stays directly readable by its exact
// operation identity and the index never holds a current/superseded
// ambiguity.
func TestCanonicalDeliveryRevokeReauthorizePreservesAudit(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")

	auth1 := mustAuthorize(t, c, "t1", 3, "op-auth-t1")
	revocation := mustRevoke(t, c, "t1", 4, auth1.OperationID, "delivery abandoned", "op-revoke-t1")
	if revocation.AuthorizationOperationID != auth1.OperationID {
		t.Fatalf("revocation evidence = %+v, want authorization %s", revocation, auth1.OperationID)
	}
	if revocation.OperationID != "op-revoke-t1" || revocation.Reason != "delivery abandoned" || revocation.RevokedAt <= 0 {
		t.Fatalf("revocation evidence = %+v", revocation)
	}

	// The issuance evidence document was NOT rewritten: it carries no
	// revocation state and is byte-identical to what issuance committed.
	auth1Again, err := c.DeliveryAuthorizationByOperation(mustTaskID(t, "t1"), auth1.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if auth1Again.OperationID != auth1.OperationID || auth1Again.Revision != auth1.Revision {
		t.Fatalf("issuance evidence mutated: %+v", auth1Again)
	}
	// The revocation evidence is directly readable by its operation identity.
	revAgain, err := c.DeliveryRevocationByOperation(mustTaskID(t, "t1"), "op-revoke-t1")
	if err != nil {
		t.Fatal(err)
	}
	if revAgain.AuthorizationOperationID != auth1.OperationID || revAgain.OperationID != "op-revoke-t1" {
		t.Fatalf("revocation read = %+v", revAgain)
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

	// The current read returns the second authorization; the prior revoked
	// authorization stays readable by its operation identity.
	cur, err := c.DeliveryAuthorization(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if cur.OperationID != auth2.OperationID {
		t.Fatalf("current authorization = %+v, want %s", cur, auth2.OperationID)
	}
	prior, err := c.DeliveryAuthorizationByOperation(mustTaskID(t, "t1"), auth1.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if prior.OperationID != auth1.OperationID || prior.Identity.Number != 42 {
		t.Fatalf("prior authorization = %+v", prior)
	}

	// Revoking with the wrong authorization identity fails closed.
	wrong := CanonicalRevokeDeliveryRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 6), AuthorizationOperationID: "op-unknown-auth", Reason: "x"}
	if _, err := c.RevokeDeliveryAuthorization(mustOperation(t, "op-revoke-wrong", wrong), wrong); !errors.Is(err, ErrConflict) {
		t.Fatalf("revoke with wrong identity = %v, want ErrConflict", err)
	}

	// Revoking the active authorization succeeds, and a repeated revoke of
	// the already-revoked authorization conflicts (no active auth remains).
	if res, err := c.RevokeDeliveryAuthorization(mustOperation(t, "op-revoke-auth2", CanonicalRevokeDeliveryRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 6), AuthorizationOperationID: auth2.OperationID, Reason: "superseded"}), CanonicalRevokeDeliveryRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 6), AuthorizationOperationID: auth2.OperationID, Reason: "superseded"}); err != nil {
		t.Fatalf("revoke auth2: %v", err)
	} else if res.Revocation.AuthorizationOperationID != auth2.OperationID {
		t.Fatalf("auth2 revocation = %+v", res.Revocation)
	}
	again := CanonicalRevokeDeliveryRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 7), AuthorizationOperationID: auth2.OperationID, Reason: "x"}
	if _, err := c.RevokeDeliveryAuthorization(mustOperation(t, "op-revoke-again", again), again); !errors.Is(err, ErrConflict) {
		t.Fatalf("second revoke = %v, want ErrConflict", err)
	}

	// Revoke replay is idempotent and reconstructs the revocation evidence.
	revokeReq := CanonicalRevokeDeliveryRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4), AuthorizationOperationID: auth1.OperationID, Reason: "delivery abandoned"}
	revokeOp := mustOperation(t, "op-revoke-t1", revokeReq)
	replayed, err := c.RevokeDeliveryAuthorization(revokeOp, revokeReq)
	if err != nil {
		t.Fatalf("revoke replay: %v", err)
	}
	if !replayed.Replayed || replayed.Revocation.OperationID != "op-revoke-t1" || replayed.Revocation.AuthorizationOperationID != auth1.OperationID {
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

		// The narrow read returns the committed outcome via the index pointer.
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
		mustCommitOutcome(t, c, "t1", 4, auth1.OperationID, DeliveryOutcomeRetryable, "provider unreachable", "op-outcome-retry-1")
		// The first outcome advanced the revision; the authorization is no
		// longer current, so Fleet revokes and re-authorizes for the retry.
		mustRevoke(t, c, "t1", 5, auth1.OperationID, "retry", "op-revoke-retry-1")
		auth2 := mustAuthorize(t, c, "t1", 6, "op-auth-t1-2")
		mustCommitOutcome(t, c, "t1", 7, auth2.OperationID, DeliveryOutcomeCompleted, "merge confirmed", "op-outcome-complete-2")

		out, err := c.DeliveryOutcome(mustTaskID(t, "t1"))
		if err != nil {
			t.Fatal(err)
		}
		if out.Status != DeliveryOutcomeCompleted || out.AuthorizationOperationID != auth2.OperationID {
			t.Fatalf("current outcome = %+v", out)
		}
		// The prior retryable outcome remains readable by its operation.
		prior, err := c.DeliveryOutcomeByOperation(mustTaskID(t, "t1"), "op-outcome-retry-1")
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
			mustCommitOutcome(t, c, "t1", 4, auth.OperationID, status, "terminal detail", "op-outcome-"+string(status))
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

		// Revoked authorization: no outcome can be committed against it.
		mustRevoke(t, c, "t1", 4, auth.OperationID, "abandoned", "op-revoke-prereq")
		revokedReq := req
		revokedReq.Precondition = preconditionOf(1, 5)
		revokedReq.AuthorizationOperationID = auth.OperationID
		if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-revoked", revokedReq), revokedReq); !errors.Is(err, ErrConflict) {
			t.Fatalf("outcome against revoked authorization = %v, want ErrConflict", err)
		}

		// Re-authorize, then a non-current authorization (an unrelated task
		// mutation changed the phase) fails the commit prerequisite.
		auth2 := mustAuthorize(t, c, "t1", 5, "op-auth-t1-2")
		block := CanonicalBlockRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 6), Detail: "d", Reason: "block"}
		if _, err := c.Block(mustOperation(t, "op-block-currency", block), block); err != nil {
			t.Fatal(err)
		}
		stale := req
		stale.Precondition = preconditionOf(1, 7)
		stale.AuthorizationOperationID = auth2.OperationID
		if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-stale-currency", stale), stale); !errors.Is(err, ErrPrecondition) {
			t.Fatalf("outcome with non-current authorization = %v, want ErrPrecondition", err)
		}

		// Stale task precondition fails closed as a typed conflict.
		unblock := CanonicalUnblockRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 7), Reason: "unblock"}
		if _, err := c.Unblock(mustOperation(t, "op-unblock-currency", unblock), unblock); err != nil {
			t.Fatal(err)
		}
		stalePrec := req
		stalePrec.Precondition = preconditionOf(1, 9)
		stalePrec.AuthorizationOperationID = auth2.OperationID
		if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-stale-prec", stalePrec), stalePrec); !errors.Is(err, domain.ErrStalePrecondition) {
			t.Fatalf("outcome with stale precondition = %v, want domain.ErrStalePrecondition", err)
		}

		// Non-current generation fails closed.
		complete := CanonicalCompleteRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 8), To: PhaseDone, Reason: "done"}
		if _, err := c.Complete(mustOperation(t, "op-complete-outcome-gen", complete), complete); err != nil {
			t.Fatal(err)
		}
		reopen := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 9), Reason: "reopen"}
		if _, err := c.Reopen(mustOperation(t, "op-reopen-outcome-gen", reopen), reopen); err != nil {
			t.Fatal(err)
		}
		genStale := req
		genStale.Precondition = preconditionOf(1, 9)
		genStale.AuthorizationOperationID = auth2.OperationID
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

// TestCanonicalDeliveryIndexStaysBounded proves many retryable -> revoke ->
// reauthorize cycles leave the per-task index/current document at constant
// structural size (no authorization/outcome slices or history growth), while
// every prior authorization, outcome, and revocation remains directly
// readable by its exact operation identity.
func TestCanonicalDeliveryIndexStaysBounded(t *testing.T) {
	c, h, _ := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")

	const cycles = 6
	var rev uint64 = 3
	var indexBytes []byte
	for i := 0; i < cycles; i++ {
		authOp := fmt.Sprintf("op-auth-%d", i)
		outOp := fmt.Sprintf("op-outcome-%d", i)
		revOp := fmt.Sprintf("op-revoke-%d", i)
		auth := mustAuthorize(t, c, "t1", rev, authOp)
		rev++
		mustCommitOutcome(t, c, "t1", rev, auth.OperationID, DeliveryOutcomeRetryable, "retry", outOp)
		rev++
		mustRevoke(t, c, "t1", rev, auth.OperationID, "cycle", revOp)
		rev++

		data, ok, err := readDocForTest(h, deliveryCurrentKey("t1"))
		if err != nil || !ok {
			t.Fatalf("read index after cycle %d: ok=%v err=%v", i, ok, err)
		}
		// The current document keeps constant structural size across cycles:
		// identical byte length with fixed-length operation pointers and an
		// exactly bounded field set (no history slices).
		if i == 0 {
			indexBytes = data
		} else if len(data) != len(indexBytes) {
			t.Fatalf("index changed size after cycle %d: %d vs %d bytes", i, len(data), len(indexBytes))
		}
		if bytes.Contains(data, []byte(`"authorizations"`)) || bytes.Contains(data, []byte(`"outcomes"`)) {
			t.Fatalf("index carries history slices: %s", data)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		for key := range raw {
			switch key {
			case "schema_version", "task_id", "authorization_op_id", "revocation_op_id", "outcome_op_id", "terminal":
			default:
				t.Fatalf("index carries unexpected field %q: %s", key, data)
			}
		}
		var index DeliveryIndex
		if err := json.Unmarshal(data, &index); err != nil {
			t.Fatal(err)
		}
		if index.AuthorizationOpID != authOp || index.RevocationOpID != revOp || index.OutcomeOpID != outOp || index.Terminal {
			t.Fatalf("index pointers after cycle %d = %+v", i, index)
		}
	}

	// Every evidence document grew one file per operation, never one doc.
	if got := countDeliveryEvidenceFiles(t, c, "authorizations"); got != cycles {
		t.Fatalf("authorization evidence files = %d, want %d", got, cycles)
	}
	if got := countDeliveryEvidenceFiles(t, c, "outcomes"); got != cycles {
		t.Fatalf("outcome evidence files = %d, want %d", got, cycles)
	}
	if got := countDeliveryEvidenceFiles(t, c, "revocations"); got != cycles {
		t.Fatalf("revocation evidence files = %d, want %d", got, cycles)
	}

	// Every prior authorization/outcome/revocation remains readable by its
	// exact operation identity.
	for i := 0; i < cycles; i++ {
		auth, err := c.DeliveryAuthorizationByOperation(mustTaskID(t, "t1"), fmt.Sprintf("op-auth-%d", i))
		if err != nil {
			t.Fatalf("prior authorization %d lost: %v", i, err)
		}
		if uint64(auth.Revision) != uint64(4+i*3) {
			t.Fatalf("prior authorization %d revision = %d, want %d", i, auth.Revision, 4+i*3)
		}
		out, err := c.DeliveryOutcomeByOperation(mustTaskID(t, "t1"), fmt.Sprintf("op-outcome-%d", i))
		if err != nil {
			t.Fatalf("prior outcome %d lost: %v", i, err)
		}
		if out.Status != DeliveryOutcomeRetryable || out.AuthorizationOperationID != fmt.Sprintf("op-auth-%d", i) {
			t.Fatalf("prior outcome %d = %+v", i, out)
		}
		revocation, err := c.DeliveryRevocationByOperation(mustTaskID(t, "t1"), fmt.Sprintf("op-revoke-%d", i))
		if err != nil {
			t.Fatalf("prior revocation %d lost: %v", i, err)
		}
		if revocation.AuthorizationOperationID != fmt.Sprintf("op-auth-%d", i) {
			t.Fatalf("prior revocation %d = %+v", i, revocation)
		}
	}

	// The current read follows the bounded pointer to the last authorization.
	cur, err := c.DeliveryAuthorization(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if cur.OperationID != fmt.Sprintf("op-auth-%d", cycles-1) {
		t.Fatalf("current authorization = %+v", cur)
	}
	currency, err := c.DeliveryCurrency(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if currency.Valid || !hasCurrencyReason(currency, DeliveryCurrencyRevoked) {
		t.Fatalf("currency after cycles = %+v, want revoked", currency)
	}
}

// TestCanonicalDeliveryOperationKeyCollisionAndPathSafety proves operation
// identities are collision-safe: an operation identity is bound to exactly
// one intent in the home (reuse across tasks conflicts at the receipt gate),
// distinct operations on different tasks commit distinct immutable evidence
// documents under their own task keys, and unsafe identities fail closed.
func TestCanonicalDeliveryOperationKeyCollisionAndPathSafety(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")
	mustDeliveryTask(t, c, "t2")

	// Distinct operation identities under two tasks commit distinct evidence
	// documents; each resolves its own.
	auth1 := mustAuthorize(t, c, "t1", 3, "op-auth-t1")
	auth2 := mustAuthorize(t, c, "t2", 3, "op-auth-t2")
	a1, err := c.DeliveryAuthorizationByOperation(mustTaskID(t, "t1"), "op-auth-t1")
	if err != nil {
		t.Fatal(err)
	}
	a2, err := c.DeliveryAuthorizationByOperation(mustTaskID(t, "t2"), "op-auth-t2")
	if err != nil {
		t.Fatal(err)
	}
	if a1.TaskID != "t1" || a2.TaskID != "t2" || a1.OperationID != auth1.OperationID || a2.OperationID != auth2.OperationID {
		t.Fatalf("key collision: a1=%+v a2=%+v", a1, a2)
	}
	// A document under one task's key is never resolved for the other task.
	if _, err := c.DeliveryAuthorizationByOperation(mustTaskID(t, "t2"), "op-auth-t1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-task authorization resolution = %v, want ErrNotFound", err)
	}

	// Reusing an operation identity for a different task intent conflicts at
	// the operation receipt gate: an operation identity is collision-safe
	// because it can never bind two different intents in the home.
	dupReq := authorizeRequest(c, "t2", preconditionOf(1, 3))
	dup, err := domain.NewOperation(mustOperation(t, "op-auth-t1", authorizeRequest(c, "t1", preconditionOf(1, 3))).ID, dupReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AuthorizeDelivery(dup, dupReq); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("reused op identity across tasks = %v, want ErrOperationConflict", err)
	}

	// Unsafe operation identities in revocation and outcome requests fail
	// validation before any key is formed.
	rr := CanonicalRevokeDeliveryRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4), AuthorizationOperationID: "a/b", Reason: "x"}
	if _, err := c.RevokeDeliveryAuthorization(mustOperation(t, "op-revoke-unsafe", rr), rr); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("revoke with unsafe authorization identity = %v, want ErrInvalidInput", err)
	}
	or := CanonicalDeliveryOutcomeRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4), AuthorizationOperationID: "a\\b", Status: DeliveryOutcomeCompleted, Detail: "x"}
	if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-outcome-unsafe", or), or); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("outcome with unsafe authorization identity = %v, want ErrInvalidInput", err)
	}
	if _, err := c.DeliveryOutcomeByOperation(mustTaskID(t, "t1"), "../escape"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("outcome read with unsafe operation identity = %v, want ErrInvalidInput", err)
	}
}

// TestCanonicalDeliveryGenerationReopenDoesNotMakeOldAuthorizationCurrent
// proves a generation reopen preserves the old operation evidence while the
// currency read never reports the old generation's authorization as current
// truth.
func TestCanonicalDeliveryGenerationReopenDoesNotMakeOldAuthorizationCurrent(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")
	auth := mustAuthorize(t, c, "t1", 3, "op-auth-gen1")

	complete := CanonicalCompleteRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4), To: PhaseDone, Reason: "done"}
	if _, err := c.Complete(mustOperation(t, "op-complete-reopen", complete), complete); err != nil {
		t.Fatal(err)
	}
	reopen := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 5), Reason: "reopen"}
	if _, err := c.Reopen(mustOperation(t, "op-reopen-delivery", reopen), reopen); err != nil {
		t.Fatal(err)
	}

	// The old authorization evidence remains readable by its operation.
	prior, err := c.DeliveryAuthorizationByOperation(mustTaskID(t, "t1"), auth.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if prior.Generation != 1 || prior.OperationID != auth.OperationID {
		t.Fatalf("prior authorization = %+v", prior)
	}
	// The current pointer still resolves it, but the currency read reports it
	// is not current-generation truth.
	cur, err := c.DeliveryCurrency(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if cur.Valid || !hasCurrencyReason(cur, DeliveryCurrencyGeneration) {
		t.Fatalf("currency after reopen = %+v, want generation-mismatch", cur)
	}
}

// TestCanonicalDeliverySchemaRejectsMalformedRecords proves malformed,
// unknown-schema, or legacy/unbounded-shaped delivery current documents fail
// closed with no compatibility fallback, and that current reads reject
// missing or substituted evidence.
func TestCanonicalDeliverySchemaRejectsMalformedRecords(t *testing.T) {
	plant := func(t *testing.T, c *Canonical, key, data string) {
		t.Helper()
		if err := writePathForTest(t, c, key, []byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	readFails := func(t *testing.T, c *Canonical) {
		t.Helper()
		if _, err := c.DeliveryAuthorization(mustTaskID(t, "t1")); err == nil {
			t.Fatal("DeliveryAuthorization accepted malformed record")
		}
		if _, err := c.DeliveryCurrency(mustTaskID(t, "t1")); err == nil {
			t.Fatal("DeliveryCurrency accepted malformed record")
		}
	}

	t.Run("unknown-schema-index", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		plant(t, c, deliveryCurrentKey("t1"), `{"schema_version":"munsu.task-authority/v2","task_id":"t1"}`)
		readFails(t, c)
	})

	t.Run("malformed-json-index", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		plant(t, c, deliveryCurrentKey("t1"), `{not json`)
		readFails(t, c)
	})

	// The rejected pre-rework append-only format: an unbounded current
	// document with authorization/outcome slices must fail closed at the
	// current path with no compatibility fallback.
	t.Run("legacy-unbounded-current-document", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		plant(t, c, deliveryCurrentKey("t1"), `{"schema_version":"munsu.task-authority/v1","task_id":"t1","authorizations":[],"outcomes":[]}`)
		readFails(t, c)
	})

	// An index pointer to a missing immutable evidence document fails closed.
	t.Run("missing-evidence", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		plant(t, c, deliveryCurrentKey("t1"), `{"schema_version":"munsu.task-authority/v1","task_id":"t1","authorization_op_id":"op-nonexistent"}`)
		readFails(t, c)
	})

	// Substituted evidence: the index resolves an evidence document bound to
	// a different task; current reads fail closed.
	t.Run("substituted-evidence", func(t *testing.T) {
		c, h, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		mustAuthorize(t, c, "t1", 3, "op-auth-t1")
		evData, ok, err := readDocForTest(h, deliveryAuthorizationKey("t1", "op-auth-t1"))
		if err != nil || !ok {
			t.Fatalf("read evidence: ok=%v err=%v", ok, err)
		}
		var authDoc DeliveryAuthorization
		if err := json.Unmarshal(evData, &authDoc); err != nil {
			t.Fatal(err)
		}
		authDoc.TaskID = "other-task"
		bad, err := json.Marshal(authDoc)
		if err != nil {
			t.Fatal(err)
		}
		if err := writePathForTest(t, c, deliveryAuthorizationKey("t1", "op-auth-t1"), bad); err != nil {
			t.Fatal(err)
		}
		readFails(t, c)
	})

	// A terminal marker incoherent with the pointed outcome evidence fails
	// closed on the current outcome read.
	t.Run("incoherent-terminal-marker", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		auth := mustAuthorize(t, c, "t1", 3, "op-auth-t1")
		mustCommitOutcome(t, c, "t1", 4, auth.OperationID, DeliveryOutcomeRetryable, "retry", "op-outcome-t1")
		plant(t, c, deliveryCurrentKey("t1"), `{"schema_version":"munsu.task-authority/v1","task_id":"t1","authorization_op_id":"op-auth-t1","outcome_op_id":"op-outcome-t1","terminal":true}`)
		if _, err := c.DeliveryOutcome(mustTaskID(t, "t1")); err == nil {
			t.Fatal("DeliveryOutcome accepted incoherent terminal marker")
		}
		if _, err := c.DeliveryCurrency(mustTaskID(t, "t1")); err == nil {
			t.Fatal("DeliveryCurrency accepted incoherent terminal marker")
		}
	})

	// Malformed evidence documents themselves fail closed.
	t.Run("malformed-evidence-document", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustDeliveryTask(t, c, "t1")
		mustAuthorize(t, c, "t1", 3, "op-auth-t1")
		plant(t, c, deliveryAuthorizationKey("t1", "op-auth-t1"), `{not json`)
		readFails(t, c)
	})
}

// TestCanonicalDeliveryRejectedWhileCleanupClaimActive proves the delivery
// mutations (AuthorizeDelivery, RevokeDeliveryAuthorization,
// CommitDeliveryOutcome) are gated by the active cleanup claim: a retired task
// with an in-flight cleanup can never mutate delivery state or advance the
// revision the cleanup revalidates against (BEO-16/P1a medium finding).
func TestCanonicalDeliveryRejectedWhileCleanupClaimActive(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1") // working, revision 3

	// Retire generation 1: the durable active cleanup claim is committed.
	retire := retireRequest(t, c, "t1", preconditionOf(1, 3))
	if _, err := c.Retire(mustOperation(t, "op-delclaim-retire", retire), retire); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != CleanupActive {
		t.Fatalf("cleanup claim not active: %+v", agg.CleanupClaim)
	}
	rev := uint64(agg.Revision)

	// AuthorizeDelivery fails closed on the claim fence.
	authReq := authorizeRequest(c, "t1", preconditionOf(1, rev))
	if _, err := c.AuthorizeDelivery(mustOperation(t, "op-delclaim-auth", authReq), authReq); !errors.Is(err, ErrConflict) {
		t.Fatalf("AuthorizeDelivery with active claim = %v, want ErrConflict", err)
	}

	// RevokeDeliveryAuthorization fails closed on the claim fence.
	revokeReq := CanonicalRevokeDeliveryRequest{
		HomeID:                   c.HomeID(),
		TaskID:                   mustTaskID(t, "t1"),
		Precondition:             preconditionOf(1, rev),
		AuthorizationOperationID: "op-auth-any",
		Reason:                   "revoke",
	}
	if _, err := c.RevokeDeliveryAuthorization(mustOperation(t, "op-delclaim-revoke", revokeReq), revokeReq); !errors.Is(err, ErrConflict) {
		t.Fatalf("RevokeDeliveryAuthorization with active claim = %v, want ErrConflict", err)
	}

	// CommitDeliveryOutcome fails closed on the claim fence.
	outcomeReq := CanonicalDeliveryOutcomeRequest{
		HomeID:                   c.HomeID(),
		TaskID:                   mustTaskID(t, "t1"),
		Precondition:             preconditionOf(1, rev),
		AuthorizationOperationID: "op-auth-any",
		Status:                   DeliveryOutcomeCompleted,
		Detail:                   "outcome",
	}
	if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-delclaim-commit", outcomeReq), outcomeReq); !errors.Is(err, ErrConflict) {
		t.Fatalf("CommitDeliveryOutcome with active claim = %v, want ErrConflict", err)
	}

	// The claim fence is identity-fenced for continuations and the aggregate
	// revision never advanced: the delivery mutation could not invalidate the
	// cleanup revalidation snapshot.
	agg, err = c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if uint64(agg.Revision) != rev {
		t.Fatalf("revision advanced %d -> %d despite claim rejection", rev, agg.Revision)
	}
}
