package taskauthority

import (
	"errors"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

const (
	pushGrantHead  = "6503e5e9852b028c95c8d1c9cd9178667506ac7b"
	pushGrantOther = "0123456789abcdef0123456789abcdef01234567"
)

func pushGrantRequest(t *testing.T, c *Canonical, taskID, head string, prec domain.Precondition) CanonicalRecordPushGrantRequest {
	t.Helper()
	return CanonicalRecordPushGrantRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: prec,
		HeadSHA: head, Words: domain.Words{Grantor: "Human", Channel: "supervisor-relay:typed", Quote: "a"},
	}
}

func recordPushGrant(t *testing.T, c *Canonical, opID string, req CanonicalRecordPushGrantRequest) (PushGrantRecord, error) {
	t.Helper()
	return c.RecordPushGrant(mustOperation(t, opID, req), req)
}

func TestCanonicalRecordPushGrantIsExactAndIdempotent(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	req := pushGrantRequest(t, c, "t1", pushGrantHead, preconditionOf(1, 1))

	grant, err := recordPushGrant(t, c, "op-push-grant-1", req)
	if err != nil {
		t.Fatalf("RecordPushGrant: %v", err)
	}
	if grant.TaskID != "t1" || grant.Generation != 1 || grant.OperationID != "op-push-grant-1" ||
		grant.HeadSHA != pushGrantHead || grant.Words != req.Words || grant.RecordedAt <= 0 {
		t.Fatalf("push grant = %+v, want current task, generation, exact head, operation and words", grant)
	}
	if found, err := c.HasPushGrant(mustTaskID(t, "t1"), pushGrantHead); err != nil || !found {
		t.Fatalf("HasPushGrant(exact head) = %v, %v; want true", found, err)
	}
	if found, err := c.HasPushGrant(mustTaskID(t, "t1"), pushGrantOther); err != nil || found {
		t.Fatalf("HasPushGrant(other head) = %v, %v; want false", found, err)
	}

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 1 {
		t.Fatalf("grant changed task revision to %d, want 1", agg.Revision)
	}

	replay, err := recordPushGrant(t, c, "op-push-grant-1", req)
	if err != nil || replay != grant {
		t.Fatalf("same-operation replay = %+v, %v; want %+v", replay, err, grant)
	}
	changed := req
	changed.Words.Quote = "different words"
	if _, err := recordPushGrant(t, c, "op-push-grant-1", changed); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("reused operation with different words = %v, want ErrOperationConflict", err)
	}

	second := pushGrantRequest(t, c, "t1", pushGrantOther, preconditionOf(1, 1))
	if _, err := recordPushGrant(t, c, "op-push-grant-2", second); err != nil {
		t.Fatalf("RecordPushGrant(second head): %v", err)
	}
	for _, head := range []string{pushGrantHead, pushGrantOther} {
		if found, err := c.HasPushGrant(mustTaskID(t, "t1"), head); err != nil || !found {
			t.Fatalf("HasPushGrant(%s) = %v, %v; want true", head, found, err)
		}
	}
}

func TestCanonicalRecordPushGrantRejectsMalformedInput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*CanonicalRecordPushGrantRequest)
	}{
		{"short SHA", func(r *CanonicalRecordPushGrantRequest) { r.HeadSHA = "1234" }},
		{"non-hex SHA", func(r *CanonicalRecordPushGrantRequest) { r.HeadSHA = strings.Repeat("g", 40) }},
		{"64-character SHA", func(r *CanonicalRecordPushGrantRequest) { r.HeadSHA = strings.Repeat("a", 64) }},
		{"missing grantor", func(r *CanonicalRecordPushGrantRequest) { r.Words.Grantor = " " }},
		{"missing channel", func(r *CanonicalRecordPushGrantRequest) { r.Words.Channel = "" }},
		{"missing quote", func(r *CanonicalRecordPushGrantRequest) { r.Words.Quote = "\t" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := newTestCanonical(t)
			mustCreate(t, c, "t1")
			req := pushGrantRequest(t, c, "t1", pushGrantHead, preconditionOf(1, 1))
			tc.mutate(&req)
			if _, err := recordPushGrant(t, c, "op-push-grant-invalid", req); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("RecordPushGrant(invalid request) = %v, want ErrInvalidInput", err)
			}
			if found, err := c.HasPushGrant(mustTaskID(t, "t1"), pushGrantHead); err != nil || found {
				t.Fatalf("invalid request left a grant: found=%v err=%v", found, err)
			}
		})
	}
}

func TestCanonicalPushGrantDoesNotCarryAcrossTaskGeneration(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	req := pushGrantRequest(t, c, "t1", pushGrantHead, preconditionOf(1, 1))
	if _, err := recordPushGrant(t, c, "op-push-grant-old-generation", req); err != nil {
		t.Fatal(err)
	}

	complete := CanonicalCompleteRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 1),
		To: PhaseDone, Reason: "complete before reopen",
	}
	if _, err := c.Complete(mustOperation(t, "op-push-grant-complete", complete), complete); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	reopen := CanonicalReopenRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 2), Reason: "new generation",
	}
	if _, err := c.Reopen(mustOperation(t, "op-push-grant-reopen", reopen), reopen); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if found, err := c.HasPushGrant(mustTaskID(t, "t1"), pushGrantHead); err != nil || found {
		t.Fatalf("prior-generation grant matched reopened task: found=%v err=%v", found, err)
	}
}
