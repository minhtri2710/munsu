package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestOperationValidateAcceptsWellFormed(t *testing.T) {
	digest, err := Digest(map[string]string{"a": "b"})
	if err != nil {
		t.Fatal(err)
	}
	op := Operation{ID: OperationID("op-1"), Digest: digest}
	if err := op.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestOperationValidateRejectsNonOperationKind(t *testing.T) {
	digest, _ := Digest("x")
	op := Operation{ID: TaskID("gh-1"), Digest: digest}
	if err := op.Validate(); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("Validate: got %v, want ErrInvalidOperation", err)
	}
}

func TestOperationValidateRejectsMalformedDigest(t *testing.T) {
	// A full-length non-hex digest must fail exactly like a short one.
	for _, d := range []string{"", "abcd", strings.Repeat("z", 64), strings.Repeat("0", 63)} {
		op := Operation{ID: OperationID("op-1"), Digest: d}
		if err := op.Validate(); !errors.Is(err, ErrInvalidOperation) {
			t.Errorf("Validate(digest %q): got %v, want ErrInvalidOperation", d, err)
		}
	}
}

func TestIsSHA256Shape(t *testing.T) {
	good, _ := Digest("x")
	if !IsSHA256(good) {
		t.Errorf("IsSHA256(%q) = false", good)
	}
	// Full-length non-hex is not a digest.
	if IsSHA256(strings.Repeat("g", 64)) {
		t.Error("IsSHA256(non-hex 64) = true")
	}
	if IsSHA256("") || IsSHA256("abc") {
		t.Error("IsSHA256 short = true")
	}
}

func TestDigestIsDeterministicAndStable(t *testing.T) {
	type intent struct {
		Kind string
		N    int
	}
	a, err := Digest(intent{Kind: "bind", N: 3})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Digest(intent{Kind: "bind", N: 3})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("digest not stable: %q vs %q", a, b)
	}
	if !IsSHA256(a) {
		t.Errorf("digest %q is not sha256", a)
	}
}

func TestDigestDiffersForDifferentIntent(t *testing.T) {
	a, _ := Digest(map[string]string{"k": "v1"})
	b, _ := Digest(map[string]string{"k": "v2"})
	if a == b {
		t.Error("different intents produced the same digest")
	}
}

func TestReplayAndConflictSemantics(t *testing.T) {
	d1, _ := Digest("intent-1")
	d2, _ := Digest("intent-2")
	existing := Operation{ID: OperationID("op-1"), Digest: d1}

	same := Operation{ID: OperationID("op-1"), Digest: d1}
	if !Replay(existing, same) {
		t.Error("same ID + same digest should be a replay")
	}
	if ReusedWithDifferentIntent(existing, same) {
		t.Error("same ID + same digest should not be a conflict")
	}

	different := Operation{ID: OperationID("op-1"), Digest: d2}
	if Replay(existing, different) {
		t.Error("same ID + different digest should not be a replay")
	}
	if !ReusedWithDifferentIntent(existing, different) {
		t.Error("same ID + different digest should be a conflicting intent")
	}

	other := Operation{ID: OperationID("op-2"), Digest: d1}
	if Replay(existing, other) {
		t.Error("different ID should not be a replay")
	}
	if ReusedWithDifferentIntent(existing, other) {
		t.Error("different ID should not be a conflict")
	}
}
