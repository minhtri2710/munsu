package taskauthority

import (
	"errors"
	"testing"
)

// gateScene is a task authorized for delivery at deliveryHead (revision 5,
// authorization operation "op-auth-gate").
func gateScene(t *testing.T) (*Canonical, DeliveryAuthorization) {
	t.Helper()
	c, _, _ := newTestCanonical(t)
	rev := mustDeliveryTask(t, c, "t1")
	return c, mustAuthorize(t, c, "t1", rev, "op-auth-gate")
}

func gateRequest(t *testing.T, c *Canonical, auth DeliveryAuthorization) CanonicalRecordGateRequest {
	t.Helper()
	return CanonicalRecordGateRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, deliveryReadyRev+1),
		AuthorizationOperationID: auth.OperationID, Operation: DeliveryAuthorizationProviderMerge,
		HeadSHA: deliveryHead, Words: testWords(),
	}
}

func recordGate(t *testing.T, c *Canonical, opID string, req CanonicalRecordGateRequest) (GateRecord, error) {
	t.Helper()
	return c.RecordGate(mustOperation(t, opID, req), req)
}

func assertNoGateRecord(t *testing.T, c *Canonical, opID string) {
	t.Helper()
	if _, found, err := c.readGateRecord("t1", opID); err != nil || found {
		t.Fatalf("gate record %s: found=%v err=%v, want none", opID, found, err)
	}
}

func TestCanonicalRecordGateAppendsImmutableEvidenceWithoutChangingTheTask(t *testing.T) {
	c, auth := gateScene(t)
	req := gateRequest(t, c, auth)

	g, err := recordGate(t, c, "op-gate-1", req)
	if err != nil {
		t.Fatalf("RecordGate: %v", err)
	}
	if g.TaskID != "t1" || g.Generation != 1 || g.AuthorizationOperationID != auth.OperationID || g.OperationID != "op-gate-1" ||
		g.Operation != DeliveryAuthorizationProviderMerge || g.HeadSHA != deliveryHead || g.Words != testWords() || g.RecordedAt <= 0 {
		t.Fatalf("gate record = %+v, want the operation, head, authorization and words of the request", g)
	}
	stored, found, err := c.readGateRecord("t1", "op-gate-1")
	if err != nil || !found || stored != g {
		t.Fatalf("stored gate record = %+v found=%v err=%v, want %+v", stored, found, err, g)
	}

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if uint64(agg.Revision) != deliveryReadyRev+1 {
		t.Fatalf("task revision after a gate record = %d, want %d unchanged", agg.Revision, deliveryReadyRev+1)
	}
	if cur, err := c.DeliveryCurrency(mustTaskID(t, "t1")); err != nil || !cur.Valid {
		t.Fatalf("authorization currency after a gate record = %+v, %v, want it still valid", cur, err)
	}

	replay, err := recordGate(t, c, "op-gate-1", req)
	if err != nil || replay != g {
		t.Fatalf("replay = %+v, %v, want the recorded %+v", replay, err, g)
	}
	other := req
	other.Words.Quote = "a different quote"
	if _, err := recordGate(t, c, "op-gate-1", other); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("same operation with other words = %v, want ErrOperationConflict", err)
	}

	second, err := recordGate(t, c, "op-gate-2", req)
	if err != nil || second.OperationID != "op-gate-2" {
		t.Fatalf("second gate record under another operation = %+v, %v", second, err)
	}
	if kept, _, _ := c.readGateRecord("t1", "op-gate-1"); kept != g {
		t.Fatalf("first gate record after a second = %+v, want it unchanged", kept)
	}
}

func TestCanonicalRecordGateRefusals(t *testing.T) {
	validation := []struct {
		name   string
		mutate func(*CanonicalRecordGateRequest)
		want   string
	}{
		{"unknown operation", func(r *CanonicalRecordGateRequest) { r.Operation = "force-push" }, `invalid gate operation "force-push"`},
		{"unsafe authorization identity", func(r *CanonicalRecordGateRequest) { r.AuthorizationOperationID = "a/b" }, "gate record requires the exact authorization operation identity"},
		{"no authorization identity", func(r *CanonicalRecordGateRequest) { r.AuthorizationOperationID = "" }, "gate record requires the exact authorization operation identity"},
		{"unsafe head", func(r *CanonicalRecordGateRequest) { r.HeadSHA = " " }, "gate record head SHA must be a safe non-empty value"},
		{"no grantor", func(r *CanonicalRecordGateRequest) { r.Words.Grantor = "" }, "gate record: words: grantor is required"},
		{"no channel", func(r *CanonicalRecordGateRequest) { r.Words.Channel = "" }, "gate record: words: channel is required"},
		{"no quote", func(r *CanonicalRecordGateRequest) { r.Words.Quote = "" }, "gate record: words: quote is required"},
	}
	for _, tc := range validation {
		t.Run(tc.name, func(t *testing.T) {
			c, auth := gateScene(t)
			req := gateRequest(t, c, auth)
			tc.mutate(&req)
			_, err := recordGate(t, c, "op-gate-refused", req)
			wantErrSubstring(t, err, tc.want, "RecordGate")
			assertNoGateRecord(t, c, "op-gate-refused")
		})
	}

	t.Run("another authorization than the current one", func(t *testing.T) {
		c, auth := gateScene(t)
		req := gateRequest(t, c, auth)
		req.AuthorizationOperationID = "op-auth-stale"
		_, err := recordGate(t, c, "op-gate-refused", req)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("RecordGate naming another authorization = %v, want ErrConflict", err)
		}
		wantErrSubstring(t, err, "gate record authorization identity mismatch", "RecordGate naming another authorization")
		assertNoGateRecord(t, c, "op-gate-refused")
	})

	t.Run("a head the authorization does not bind", func(t *testing.T) {
		c, auth := gateScene(t)
		req := gateRequest(t, c, auth)
		req.HeadSHA = reviewBaseSHA
		_, err := recordGate(t, c, "op-gate-refused", req)
		if !errors.Is(err, ErrPrecondition) {
			t.Fatalf("RecordGate of another head = %v, want ErrPrecondition", err)
		}
		wantErrSubstring(t, err, "is not the authorized head", "RecordGate of another head")
		assertNoGateRecord(t, c, "op-gate-refused")
	})

	t.Run("a revoked authorization", func(t *testing.T) {
		c, auth := gateScene(t)
		mustRevoke(t, c, "t1", deliveryReadyRev+1, auth.OperationID, "withdrawn", "op-revoke-gate")
		req := gateRequest(t, c, auth)
		req.Precondition = preconditionOf(1, deliveryReadyRev+2)
		_, err := recordGate(t, c, "op-gate-refused", req)
		wantErrSubstring(t, err, "is revoked or already terminal", "RecordGate on a revoked authorization")
		assertNoGateRecord(t, c, "op-gate-refused")
	})

	t.Run("a terminal delivery outcome", func(t *testing.T) {
		c, auth := gateScene(t)
		mustCommitOutcome(t, c, "t1", deliveryReadyRev+1, auth.OperationID, DeliveryOutcomeCompleted, "merged", "op-outcome-gate")
		req := gateRequest(t, c, auth)
		req.Precondition = preconditionOf(1, deliveryReadyRev+2)
		_, err := recordGate(t, c, "op-gate-refused", req)
		wantErrSubstring(t, err, "is revoked or already terminal", "RecordGate after a terminal outcome")
		assertNoGateRecord(t, c, "op-gate-refused")
	})

	t.Run("a delivery hold on the task", func(t *testing.T) {
		c, auth := gateScene(t)
		hold := CanonicalAddHoldRequest{HomeID: c.HomeID(), HoldID: "hold-gate", Scope: DispatchHoldScope{TaskIDs: []string{"t1"}}, Actions: []DispatchAction{DispatchActionDelivery}, Reason: "freeze"}
		if _, err := c.AddHold(mustOperation(t, "op-add-hold-gate", hold), hold); err != nil {
			t.Fatal(err)
		}
		_, err := recordGate(t, c, "op-gate-refused", gateRequest(t, c, auth))
		if !errors.Is(err, ErrPrecondition) {
			t.Fatalf("RecordGate under a delivery hold = %v, want ErrPrecondition", err)
		}
		wantErrSubstring(t, err, "delivery authorization is not current for task t1", "RecordGate under a delivery hold")
		assertNoGateRecord(t, c, "op-gate-refused")
	})

	t.Run("words are refused before any state is read", func(t *testing.T) {
		c, auth := gateScene(t)
		req := gateRequest(t, c, auth)
		req.TaskID = mustTaskID(t, "no-such-task")
		req.Words.Quote = ""
		_, err := c.RecordGate(mustOperation(t, "op-gate-unread", req), req)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("RecordGate with empty words for an unknown task = %v, want ErrInvalidInput before the task is read", err)
		}
		wantErrSubstring(t, err, "gate record: words: quote is required", "RecordGate with empty words for an unknown task")
	})
}
