package taskauthority

import (
	"errors"
	"strings"
	"testing"
)

// mustMergeAttemptRequest builds a valid RecordMergeAttempt request for one
// task carrying the given outcome and head.
func mustMergeAttemptRequest(taskID string, generation Generation, outcome, headSHA string) RecordMergeAttemptRequest {
	return RecordMergeAttemptRequest{
		OperationID:        "op-merge-attempt-" + taskID + "-" + outcome,
		Actor:              Actor{ID: "test", Rank: "general"},
		TaskID:             taskID,
		ExpectedGeneration: generation,
		Outcome:            outcome,
		HeadSHA:            headSHA,
		MergedSHA:          "",
		Identity: ProviderIdentitySnapshot{
			Provider: "github",
			Owner:    "owner",
			Repo:     "repo",
			Number:   42,
			URL:      "https://github.com/owner/repo/pull/42",
			BaseRef:  "main",
			HeadRef:  "feature/test",
			HeadSHA:  headSHA,
		},
		Reason: "merge delivery",
	}
}

// mergeAttemptAuditCount counts committed merge-outcome audit events for the
// task from the Store view (one typed audit event per operation).
func mergeAttemptAuditCount(t *testing.T, a *Authority, taskID string) int {
	t.Helper()
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range v.Audit {
		if ev.Kind == AuditMergeOutcome && ev.TaskID == taskID {
			n++
		}
	}
	return n
}

// --- RecordMergeAttempt ---

func TestRecordMergeAttemptCommitsGenerationBoundRecord(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)

	res, err := a.RecordMergeAttempt(mustMergeAttemptRequest("t1", 1, MergeOutcomeMerged, head))
	if err != nil {
		t.Fatal(err)
	}
	if res.TaskID != "t1" || res.Generation != 1 || res.Revision != 2 || res.Phase != PhaseQueued || res.Replayed {
		t.Fatalf("merge attempt result = %+v, want revision 2 queued", res)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.MergeAttempt == nil {
		t.Fatal("merge attempt record missing from aggregate")
	}
	if agg.MergeAttempt.Outcome != MergeOutcomeMerged || agg.MergeAttempt.HeadSHA != head ||
		agg.MergeAttempt.Identity.HeadSHA != head || agg.MergeAttempt.Actor != "test" ||
		agg.MergeAttempt.AttemptedAt <= 0 {
		t.Fatalf("merge attempt = %+v", agg.MergeAttempt)
	}
	if mergeAttemptAuditCount(t, a, "t1") != 1 {
		t.Fatalf("expected one merge-outcome audit event")
	}
}

func TestRecordMergeAttemptGenerationFence(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)

	if _, err := a.RecordMergeAttempt(mustMergeAttemptRequest("t1", 1, MergeOutcomeOpen, head)); err != nil {
		t.Fatal(err)
	}
	// A stale generation fence fails closed and mutates nothing.
	if _, err := a.RecordMergeAttempt(mustMergeAttemptRequest("t1", 2, MergeOutcomeMerged, head)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale generation error = %v, want ErrConflict", err)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.MergeAttempt.Outcome != MergeOutcomeOpen {
		t.Fatalf("stale attempt mutated the record: %+v", agg.MergeAttempt)
	}
	if agg.Revision != 2 {
		t.Fatalf("stale attempt advanced revision to %d, want 2", agg.Revision)
	}
}

func TestRecordMergeAttemptMissingTaskFailsClosed(t *testing.T) {
	a := newTestAuthority(t)
	head := strings.Repeat("a", 40)
	if _, err := a.RecordMergeAttempt(mustMergeAttemptRequest("missing", 1, MergeOutcomeMerged, head)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task error = %v, want ErrNotFound", err)
	}
}

func TestRecordMergeAttemptSameOpReplayIsIdempotent(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)
	req := mustMergeAttemptRequest("t1", 1, MergeOutcomeMerged, head)
	req.OperationID = "op-stable-attempt"

	first, err := a.RecordMergeAttempt(req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.RecordMergeAttempt(req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed {
		t.Fatal("same-op replay must return Replayed=true")
	}
	if second.Revision != first.Revision {
		t.Fatalf("replay revision = %d, want %d", second.Revision, first.Revision)
	}
	if mergeAttemptAuditCount(t, a, "t1") != 1 {
		t.Fatalf("replay must not append a second audit event")
	}
	agg, _ := a.Get("t1")
	if agg.Revision != 2 {
		t.Fatalf("replay advanced revision to %d, want 2", agg.Revision)
	}
}

func TestRecordMergeAttemptChangedDigestConflicts(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)
	req := mustMergeAttemptRequest("t1", 1, MergeOutcomeMerged, head)
	req.OperationID = "op-stable-attempt"
	if _, err := a.RecordMergeAttempt(req); err != nil {
		t.Fatal(err)
	}

	// Same Operation ID with a different outcome is a non-retryable conflict.
	changed := req
	changed.Outcome = MergeOutcomeOpen
	if _, err := a.RecordMergeAttempt(changed); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("changed-digest error = %v, want ErrOperationConflict", err)
	}
	agg, _ := a.Get("t1")
	if agg.MergeAttempt.Outcome != MergeOutcomeMerged {
		t.Fatalf("conflict mutated the committed outcome: %+v", agg.MergeAttempt)
	}
}

func TestRecordMergeAttemptAlreadyMergedIsIdempotent(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)

	if _, err := a.RecordMergeAttempt(mustMergeAttemptRequest("t1", 1, MergeOutcomeMerged, head)); err != nil {
		t.Fatal(err)
	}
	// A re-attempt of the merged outcome under a fresh Operation ID is an
	// in-value no-op: the original committed truth is replayed without
	// advancing the revision or appending a second audit.
	res, err := a.RecordMergeAttempt(mustMergeAttemptRequest("t1", 1, MergeOutcomeMerged, head))
	if err != nil {
		t.Fatal(err)
	}
	if res.Revision != 2 {
		t.Fatalf("already-merged re-attempt advanced revision to %d, want 2", res.Revision)
	}
	if mergeAttemptAuditCount(t, a, "t1") != 1 {
		t.Fatalf("already-merged re-attempt appended a second audit event")
	}
	// The already-merged spelling is accepted the same way.
	if _, err := a.RecordMergeAttempt(mustMergeAttemptRequest("t1", 1, MergeOutcomeAlreadyMerged, head)); err != nil {
		t.Fatalf("already-merged re-attempt: %v", err)
	}
	agg, _ := a.Get("t1")
	if agg.Revision != 2 {
		t.Fatalf("already-merged re-attempt advanced revision to %d, want 2", agg.Revision)
	}
}

func TestRecordMergeAttemptProviderFalseNegativeDoesNotRegress(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)

	if _, err := a.RecordMergeAttempt(mustMergeAttemptRequest("t1", 1, MergeOutcomeMerged, head)); err != nil {
		t.Fatal(err)
	}
	// The provider later reports OPEN (eventual consistency false-negative):
	// the verified merged truth is never regressed to review-ready.
	res, err := a.RecordMergeAttempt(mustMergeAttemptRequest("t1", 1, MergeOutcomeOpen, head))
	if err != nil {
		t.Fatal(err)
	}
	if res.Revision != 2 {
		t.Fatalf("false-negative open regressed revision to %d, want 2", res.Revision)
	}
	agg, _ := a.Get("t1")
	if agg.MergeAttempt.Outcome != MergeOutcomeMerged {
		t.Fatalf("false-negative open erased verified merged truth: %+v", agg.MergeAttempt)
	}
}

func TestRecordMergeAttemptRemoteUnknownNeverErasesVerifiedMerged(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)

	if _, err := a.RecordMergeAttempt(mustMergeAttemptRequest("t1", 1, MergeOutcomeMerged, head)); err != nil {
		t.Fatal(err)
	}
	// An ambiguous later read must not erase the verified merged truth.
	res, err := a.RecordMergeAttempt(mustMergeAttemptRequest("t1", 1, MergeOutcomeRemoteUnknown, head))
	if err != nil {
		t.Fatal(err)
	}
	if res.Revision != 2 {
		t.Fatalf("remote-unknown after merged advanced revision to %d, want 2", res.Revision)
	}
	agg, _ := a.Get("t1")
	if agg.MergeAttempt.Outcome != MergeOutcomeMerged {
		t.Fatalf("remote-unknown erased verified merged truth: %+v", agg.MergeAttempt)
	}
}

func TestRecordMergeAttemptRemoteUnknownForbidsFurtherMutation(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)

	if _, err := a.RecordMergeAttempt(mustMergeAttemptRequest("t1", 1, MergeOutcomeRemoteUnknown, head)); err != nil {
		t.Fatal(err)
	}
	// Once remote-unknown is committed, every further provider-mutating
	// attempt is refused with the typed fail-closed error; only read
	// reconciliation (Get) is permitted.
	for _, outcome := range []string{MergeOutcomeMerged, MergeOutcomeOpen, MergeOutcomeFailed, MergeOutcomeRemoteUnknown} {
		req := mustMergeAttemptRequest("t1", 1, outcome, head)
		req.OperationID = "op-refused-" + outcome
		if _, err := a.RecordMergeAttempt(req); !errors.Is(err, ErrMergeMutationRefused) {
			t.Fatalf("attempt after remote-unknown (%s) error = %v, want ErrMergeMutationRefused", outcome, err)
		}
	}
	agg, err := a.Get("t1") // read reconciliation still works
	if err != nil {
		t.Fatal(err)
	}
	if agg.MergeAttempt.Outcome != MergeOutcomeRemoteUnknown {
		t.Fatalf("refused attempts mutated the committed outcome: %+v", agg.MergeAttempt)
	}
	if agg.Revision != 2 {
		t.Fatalf("refused attempts advanced revision to %d, want 2", agg.Revision)
	}
}

func TestRecordMergeAttemptNonTerminalReplacedByFreshAttempt(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)

	if _, err := a.RecordMergeAttempt(mustMergeAttemptRequest("t1", 1, MergeOutcomeOpen, head)); err != nil {
		t.Fatal(err)
	}
	// A fresh attempt after an open (non-terminal) outcome commits and
	// replaces the prior attempt record.
	res, err := a.RecordMergeAttempt(mustMergeAttemptRequest("t1", 1, MergeOutcomeMerged, head))
	if err != nil {
		t.Fatal(err)
	}
	if res.Revision != 3 {
		t.Fatalf("fresh attempt revision = %d, want 3", res.Revision)
	}
	agg, _ := a.Get("t1")
	if agg.MergeAttempt.Outcome != MergeOutcomeMerged {
		t.Fatalf("fresh attempt did not replace the prior open record: %+v", agg.MergeAttempt)
	}
	if mergeAttemptAuditCount(t, a, "t1") != 2 {
		t.Fatalf("expected two audit events (open + merged)")
	}
}

func TestRecordMergeAttemptValidation(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)

	bad := mustMergeAttemptRequest("t1", 1, "bogus", head)
	if _, err := a.RecordMergeAttempt(bad); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown outcome error = %v, want ErrInvalidInput", err)
	}

	mismatch := mustMergeAttemptRequest("t1", 1, MergeOutcomeMerged, head)
	mismatch.Identity.HeadSHA = strings.Repeat("b", 40)
	if _, err := a.RecordMergeAttempt(mismatch); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("head/identity mismatch error = %v, want ErrInvalidInput", err)
	}

	unsafe := mustMergeAttemptRequest("t1", 1, MergeOutcomeMerged, "not/a/sha")
	if _, err := a.RecordMergeAttempt(unsafe); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsafe head error = %v, want ErrInvalidInput", err)
	}

	badSHA := mustMergeAttemptRequest("t1", 1, MergeOutcomeMerged, head)
	badSHA.MergedSHA = "bad/merged"
	if _, err := a.RecordMergeAttempt(badSHA); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsafe merged SHA error = %v, want ErrInvalidInput", err)
	}

	agg, _ := a.Get("t1")
	if agg.MergeAttempt != nil {
		t.Fatalf("invalid requests committed a record: %+v", agg.MergeAttempt)
	}
}
