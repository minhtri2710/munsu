package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// testIntent is an owner-defined typed mutation intent for tests. It
// implements Intent by supplying its canonical bytes. This is the only way a
// digest is produced; there is no generic map[string]any payload seam.
type testIntent struct {
	Kind string
	N    int
}

func (t testIntent) DigestBytes() ([]byte, error) { return json.Marshal(t) }

func TestOperationValidateAcceptsWellFormed(t *testing.T) {
	op, err := NewOperation(mustOperation(t, "op-1"), testIntent{Kind: "bind", N: 1})
	if err != nil {
		t.Fatalf("NewOperation: %v", err)
	}
	if err := op.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestOperationValidateRejectsMalformedDigest(t *testing.T) {
	// A full-length non-hex digest must fail exactly like a short one.
	for _, d := range []string{"", "abcd", strings.Repeat("z", 64), strings.Repeat("0", 63)} {
		op := Operation{ID: mustOperation(t, "op-1"), Digest: d}
		if err := op.Validate(); !errors.Is(err, ErrInvalidOperation) {
			t.Errorf("Validate(digest %q): got %v, want ErrInvalidOperation", d, err)
		}
	}
}

func TestIsSHA256Shape(t *testing.T) {
	good, err := Digest(testIntent{Kind: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !IsSHA256(good) {
		t.Errorf("IsSHA256(%q) = false", good)
	}
	if IsSHA256(strings.Repeat("g", 64)) {
		t.Error("IsSHA256(non-hex 64) = true")
	}
	if IsSHA256("") || IsSHA256("abc") {
		t.Error("IsSHA256 short = true")
	}
}

// TestDigestBoundaryIsTyped ensures the digest is derived from an owner-defined
// typed intent, not a generic map[string]any payload. A map does not implement
// Intent, so Digest(map[string]any{...}) is a compile-time error; only a typed
// Intent can be digested.
func TestDigestBoundaryIsTyped(t *testing.T) {
	if _, err := Digest(testIntent{Kind: "bind", N: 3}); err != nil {
		t.Fatalf("Digest(typed intent): %v", err)
	}
}

func TestDigestIsDeterministicAndStable(t *testing.T) {
	a, err := Digest(testIntent{Kind: "bind", N: 3})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Digest(testIntent{Kind: "bind", N: 3})
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
	a, _ := Digest(testIntent{Kind: "bind", N: 1})
	b, _ := Digest(testIntent{Kind: "bind", N: 2})
	if a == b {
		t.Error("different intents produced the same digest")
	}
}

func TestNewOperationDerivesDigestFromIntent(t *testing.T) {
	op, err := NewOperation(mustOperation(t, "op-1"), testIntent{Kind: "bind", N: 1})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := Digest(testIntent{Kind: "bind", N: 1})
	if op.Digest != want {
		t.Errorf("op.Digest = %q, want %q", op.Digest, want)
	}
}

func TestReplayAndConflictSemantics(t *testing.T) {
	existing := mustOp(t, "op-1")
	d1, _ := Digest(testIntent{Kind: "a"})
	d2, _ := Digest(testIntent{Kind: "b"})
	existing.Digest = d1

	same := Operation{ID: existing.ID, Digest: d1}
	if !Replay(existing, same) {
		t.Error("same ID + same digest should be a replay")
	}
	if ReusedWithDifferentIntent(existing, same) {
		t.Error("same ID + same digest should not be a conflict")
	}

	different := Operation{ID: existing.ID, Digest: d2}
	if Replay(existing, different) {
		t.Error("same ID + different digest should not be a replay")
	}
	if !ReusedWithDifferentIntent(existing, different) {
		t.Error("same ID + different digest should be a conflicting intent")
	}

	other := Operation{ID: mustOperation(t, "op-2"), Digest: d1}
	if Replay(existing, other) {
		t.Error("different ID should not be a replay")
	}
	if ReusedWithDifferentIntent(existing, other) {
		t.Error("different ID should not be a conflict")
	}
}

func mustOp(t *testing.T, v string) Operation {
	t.Helper()
	id, err := NewOperationID(v)
	if err != nil {
		t.Fatalf("NewOperationID(%q): %v", v, err)
	}
	return Operation{ID: id}
}
