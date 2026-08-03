package taskauthority

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
)

// TestTransferIntentValidation proves the intent rejects empty or mismatched
// home identities, unsafe task IDs, invalid Generations, malformed request
// digests, and unsafe Operation IDs (ADR-0007 §10).
func TestTransferIntentValidation(t *testing.T) {
	valid := TransferIntent{
		SourceHome:             "/homes/source",
		DestinationHome:        "/homes/destination",
		TaskID:                 "TASK-1",
		Generation:             7,
		RequestDigest:          strings.Repeat("a", 64),
		SourceOperationID:      "handoff-source-TASK-1-7",
		DestinationOperationID: "handoff-dest-TASK-1-7",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid intent rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*TransferIntent)
		want   error
	}{
		{"empty source identity", func(ti *TransferIntent) { ti.SourceHome = "" }, ErrInvalidInput},
		{"empty destination identity", func(ti *TransferIntent) { ti.DestinationHome = "" }, ErrInvalidInput},
		{"mismatched identities (source equals destination)", func(ti *TransferIntent) { ti.DestinationHome = ti.SourceHome }, ErrInvalidInput},
		{"empty task id", func(ti *TransferIntent) { ti.TaskID = "" }, ErrInvalidInput},
		{"unsafe task id", func(ti *TransferIntent) { ti.TaskID = "a/b" }, ErrInvalidInput},
		{"invalid generation", func(ti *TransferIntent) { ti.Generation = 0 }, ErrInvalidGeneration},
		{"empty request digest", func(ti *TransferIntent) { ti.RequestDigest = "" }, ErrInvalidInput},
		{"short request digest", func(ti *TransferIntent) { ti.RequestDigest = strings.Repeat("a", 32) }, ErrInvalidInput},
		{"non-hex request digest", func(ti *TransferIntent) { ti.RequestDigest = strings.Repeat("z", 64) }, ErrInvalidInput},
		{"empty source operation id", func(ti *TransferIntent) { ti.SourceOperationID = "" }, ErrInvalidInput},
		{"unsafe source operation id", func(ti *TransferIntent) { ti.SourceOperationID = "../escape" }, ErrInvalidInput},
		{"empty destination operation id", func(ti *TransferIntent) { ti.DestinationOperationID = "" }, ErrInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ti := valid
			tc.mutate(&ti)
			if err := ti.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestTransferRequestDigestDeterministic proves the request digest is a
// stable sha256 binding of the immutable transfer request and changes when
// any binding changes.
func TestTransferRequestDigestDeterministic(t *testing.T) {
	req := TransferRequest{SourceHome: "/homes/source", DestinationHome: "/homes/destination", TaskID: "TASK-1", Generation: 7}
	first, err := req.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 {
		t.Fatalf("digest length = %d, want 64", len(first))
	}
	second, err := req.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("digest not deterministic: %s vs %s", first, second)
	}

	cases := []struct {
		name   string
		mutate func(*TransferRequest)
	}{
		{"task id", func(r *TransferRequest) { r.TaskID = "TASK-2" }},
		{"generation", func(r *TransferRequest) { r.Generation = 8 }},
		{"source home", func(r *TransferRequest) { r.SourceHome = "/homes/other" }},
		{"destination home", func(r *TransferRequest) { r.DestinationHome = "/homes/other" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := req
			tc.mutate(&r)
			got, err := r.Digest()
			if err != nil {
				t.Fatal(err)
			}
			if got == first {
				t.Fatalf("digest unchanged for %s", tc.name)
			}
		})
	}
}

// TestTransferIntentBindsRequestDigest proves an intent built from a request
// binds the request digest exactly and validates as a complete record.
func TestTransferIntentBindsRequestDigest(t *testing.T) {
	req := TransferRequest{SourceHome: "/homes/source", DestinationHome: "/homes/destination", TaskID: "TASK-1", Generation: 7}
	digest, err := req.Digest()
	if err != nil {
		t.Fatal(err)
	}
	intent := TransferIntent{
		SourceHome:             req.SourceHome,
		DestinationHome:        req.DestinationHome,
		TaskID:                 req.TaskID,
		Generation:             req.Generation,
		RequestDigest:          digest,
		SourceOperationID:      "handoff-source-TASK-1-7",
		DestinationOperationID: "handoff-dest-TASK-1-7",
	}
	if err := intent.Validate(); err != nil {
		t.Fatal(err)
	}
	if intent.RequestDigest != digest {
		t.Fatal("intent does not bind the request digest")
	}
}

func receiveTestIntent(t *testing.T) TransferIntent {
	t.Helper()
	req := TransferRequest{SourceHome: "/homes/source", DestinationHome: "/homes/destination", TaskID: "TASK-1", Generation: 7}
	digest, err := req.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return TransferIntent{
		SourceHome:             req.SourceHome,
		DestinationHome:        req.DestinationHome,
		TaskID:                 req.TaskID,
		Generation:             req.Generation,
		RequestDigest:          digest,
		SourceOperationID:      "handoff-source-TASK-1-7",
		DestinationOperationID: "handoff-dest-TASK-1-7",
	}
}

func receiveTestPayload(t *testing.T, revision Revision) TransferPayload {
	t.Helper()
	agg, err := NewAggregate("TASK-1", "captain:test-sm", "Implement handoff", "ship", "munsu", "")
	if err != nil {
		t.Fatal(err)
	}
	agg.Generation = 7
	agg.Revision = revision
	agg.DispatchInterpretationID = "interpretation-abc"
	agg.DispatchInterpretationDigest = strings.Repeat("d", 64)
	return TransferPayload{
		Aggregate: agg,
		Interpretations: []DispatchInterpretation{{
			SchemaVersion:            TaskAuthoritySchema,
			ID:                       "interpretation-abc",
			RequestedOrder:           []string{"TASK-1"},
			SelectedTasks:            []string{"TASK-1"},
			DependencySnapshotDigest: strings.Repeat("d", 64),
			Outcome:                  DispatchInterpretationAccepted,
			CreatedAt:                time.Now().UnixNano(),
		}},
		History: []AuditEvent{{
			OperationID: "task-create-TASK-1-7",
			Actor:       Actor{ID: "general", Rank: "general"},
			Kind:        AuditLifecycle,
			TaskID:      "TASK-1",
			Generation:  7,
			Reason:      "created",
			After:       PhaseQueued,
			At:          time.Now().UnixNano(),
		}},
	}
}

func TestReceiveTransferCommitsCompleteGenerationInOneTransaction(t *testing.T) {
	s := newMemStore()
	auth := New(s)
	intent := receiveTestIntent(t)
	payload := receiveTestPayload(t, 3)

	result, err := auth.ReceiveTransfer(ReceiveTransferRequest{
		Actor:   Actor{ID: "general", Rank: "general"},
		Intent:  intent,
		Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskID != "TASK-1" || result.Generation != 7 || result.Replayed {
		t.Fatalf("result = %+v", result)
	}

	v, err := s.View()
	if err != nil {
		t.Fatal(err)
	}
	agg, ok := v.Current("TASK-1")
	if !ok {
		t.Fatal("destination does not own the task after receive")
	}
	if agg.Generation != 7 || agg.Definition.Owner != "captain:test-sm" || agg.Phase != PhaseQueued {
		t.Fatalf("received aggregate = %+v", agg)
	}
	if agg.Revision != FirstRevision {
		t.Fatalf("received aggregate revision = %d, want FirstRevision at the destination", agg.Revision)
	}
	if agg.DispatchInterpretationID != "interpretation-abc" || agg.DispatchInterpretationDigest != strings.Repeat("d", 64) {
		t.Fatalf("received aggregate lost dispatch binding: %+v", agg)
	}
	if len(v.Interpretations) != 1 || v.Interpretations[0].ID != "interpretation-abc" {
		t.Fatalf("interpretations = %+v, want transferred record", v.Interpretations)
	}
	if len(v.Audit) != 2 {
		t.Fatalf("audit = %+v, want transferred history plus receive audit", v.Audit)
	}
	seen := map[string]bool{}
	for _, ev := range v.Audit {
		seen[ev.OperationID] = true
	}
	if !seen["task-create-TASK-1-7"] {
		t.Fatalf("history event missing: %+v", v.Audit)
	}
	if !seen[intent.DestinationOperationID] {
		t.Fatalf("receive audit event missing: %+v", v.Audit)
	}
	if len(v.Receipts) != 1 || v.Receipts[0].OperationID != intent.DestinationOperationID || v.Receipts[0].Digest != intent.RequestDigest {
		t.Fatalf("receipts = %+v, want destination operation receipt pinned to request digest", v.Receipts)
	}
}

func TestReceiveTransferIdempotentReplay(t *testing.T) {
	s := newMemStore()
	auth := New(s)
	intent := receiveTestIntent(t)
	payload := receiveTestPayload(t, 3)

	first, err := auth.ReceiveTransfer(ReceiveTransferRequest{Actor: Actor{ID: "general", Rank: "general"}, Intent: intent, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	second, err := auth.ReceiveTransfer(ReceiveTransferRequest{Actor: Actor{ID: "general", Rank: "general"}, Intent: intent, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || first.TaskID != second.TaskID || first.Generation != second.Generation {
		t.Fatalf("replay = %+v, want original outcome with Replayed=true", second)
	}
	v, err := s.View()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Aggregates) != 1 || len(v.Receipts) != 1 {
		t.Fatalf("replay duplicated state: aggregates=%d receipts=%d", len(v.Aggregates), len(v.Receipts))
	}
}

func TestReceiveTransferChangedDigestConflictsNonRetryably(t *testing.T) {
	s := newMemStore()
	auth := New(s)
	intent := receiveTestIntent(t)
	payload := receiveTestPayload(t, 3)

	if _, err := auth.ReceiveTransfer(ReceiveTransferRequest{Actor: Actor{ID: "general", Rank: "general"}, Intent: intent, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	conflict := intent
	conflict.RequestDigest = strings.Repeat("b", 64)
	if _, err := auth.ReceiveTransfer(ReceiveTransferRequest{Actor: Actor{ID: "general", Rank: "general"}, Intent: conflict, Payload: payload}); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("changed digest = %v, want ErrOperationConflict", err)
	}
	v, err := s.View()
	if err != nil {
		t.Fatal(err)
	}
	agg, _ := v.Current("TASK-1")
	if agg.Definition.Description != "Implement handoff" {
		t.Fatalf("conflicting receive overwrote destination truth: %+v", agg)
	}
}

func TestReceiveTransferDestinationOwnerConflictFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		destGen  uint64
		destDesc string
	}{
		{"same generation", 7, "destination truth"},
		{"newer generation", 8, "destination truth"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newMemStore()
			auth := New(s)
			owner := New(s)
			if _, err := owner.Create(CreateRequest{OperationID: "dest-seed-" + tc.name, Actor: Actor{ID: "captain:sm", Rank: "captain"}, TaskID: "TASK-1", Owner: "captain:sm", Description: tc.destDesc, Kind: "ship", Project: "munsu", Reason: "seed"}); err != nil {
				t.Fatal(err)
			}
			agg, err := owner.Get("TASK-1")
			if err != nil {
				t.Fatal(err)
			}
			for agg.Generation < Generation(tc.destGen) {
				if _, err := owner.Complete(CompleteRequest{OperationID: fmt.Sprintf("dest-complete-%d", agg.Generation), Actor: Actor{ID: "captain:sm", Rank: "captain"}, TaskID: "TASK-1", ExpectedGeneration: agg.Generation, To: PhaseDone, Reason: "seed"}); err != nil {
					t.Fatal(err)
				}
				if _, err := owner.Reopen(ReopenRequest{OperationID: fmt.Sprintf("dest-reopen-%d", agg.Generation), Actor: Actor{ID: "captain:sm", Rank: "captain"}, TaskID: "TASK-1", ExpectedGeneration: agg.Generation, Reason: "seed"}); err != nil {
					t.Fatal(err)
				}
				agg, err = owner.Get("TASK-1")
				if err != nil {
					t.Fatal(err)
				}
			}

			intent := receiveTestIntent(t)
			intent.Generation = 7
			payload := receiveTestPayload(t, 3)
			if tc.destGen == 8 {
				payload.Aggregate.Generation = 7
			}
			_, err = auth.ReceiveTransfer(ReceiveTransferRequest{Actor: Actor{ID: "general", Rank: "general"}, Intent: intent, Payload: payload})
			if err == nil {
				t.Fatal("expected typed conflict")
			}
			var de *domain.Error
			if !errors.As(err, &de) || de.Category != domain.ErrorConflict {
				t.Fatalf("error = %v, want typed domain conflict", err)
			}
			v, _ := s.View()
			cur, _ := v.Current("TASK-1")
			if cur.Definition.Description != tc.destDesc {
				t.Fatalf("destination truth overwritten: %+v", cur)
			}
		})
	}
}

func TestReceiveTransferPayloadMismatchRejected(t *testing.T) {
	s := newMemStore()
	auth := New(s)
	intent := receiveTestIntent(t)
	payload := receiveTestPayload(t, 3)

	bad := payload
	bad.Aggregate.TaskID = "TASK-2"
	if _, err := auth.ReceiveTransfer(ReceiveTransferRequest{Actor: Actor{ID: "general", Rank: "general"}, Intent: intent, Payload: bad}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("task mismatch = %v, want ErrInvalidInput", err)
	}

	bad = payload
	bad.Aggregate.Generation = 8
	if _, err := auth.ReceiveTransfer(ReceiveTransferRequest{Actor: Actor{ID: "general", Rank: "general"}, Intent: intent, Payload: bad}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("generation mismatch = %v, want ErrInvalidInput", err)
	}
	v, _ := s.View()
	if len(v.Aggregates) != 0 {
		t.Fatalf("mismatched payload mutated destination: %+v", v.Aggregates)
	}
}

// TestTransferReceiptSemanticsThroughStore proves the destination Authority
// Store already delivers the durable, idempotent receipt semantics required
// for the destination receipt (ADR-0007 §10): the same Operation ID with the
// same request digest replays the original receipt, and a changed digest is
// a non-retryable typed conflict.
func TestTransferReceiptSemanticsThroughStore(t *testing.T) {
	s := newMemStore()
	req := TransferRequest{SourceHome: "/homes/source", DestinationHome: "/homes/destination", TaskID: "t1", Generation: 7}
	digest, err := req.Digest()
	if err != nil {
		t.Fatal(err)
	}
	op := Operation{ID: "transfer-receipt-t1-7", Digest: digest, Actor: Actor{ID: "general", Rank: "general"}}

	first, err := s.Update(op, func(tx *Tx) error {
		agg, err := NewAggregate("t1", "captain:destination", "work", "ship", "", "")
		if err != nil {
			return err
		}
		agg.Generation = 7
		return tx.PutAggregate(agg)
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID != op.ID || first.Digest != digest || first.TaskID != "t1" || first.Generation != 7 {
		t.Fatalf("receipt = %+v, want pinned operation, digest, and task identity", first)
	}

	second, err := s.Update(op, func(tx *Tx) error {
		t.Fatal("replay must not re-run the transaction")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || second.CommittedAt != first.CommittedAt || second.Digest != first.Digest {
		t.Fatalf("replay receipt = %+v, want original receipt", second)
	}

	conflict := op
	conflict.Digest = strings.Repeat("b", 64)
	if _, err := s.Update(conflict, func(tx *Tx) error { return nil }); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("changed digest = %v, want ErrOperationConflict", err)
	}
}
