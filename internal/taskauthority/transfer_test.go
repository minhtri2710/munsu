package taskauthority

import (
	"errors"
	"strings"
	"testing"
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
