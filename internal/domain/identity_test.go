package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestKindKnownKindsValidate(t *testing.T) {
	for _, k := range []Kind{KindTask, KindCaptain, KindOperation, KindResource, KindHome} {
		if !k.Valid() {
			t.Errorf("Kind.Valid() = false for %q", k)
		}
	}
}

func TestKindUnknownKindInvalid(t *testing.T) {
	if Kind("bogus").Valid() {
		t.Error("Kind.Valid() = true for unknown kind")
	}
}

func TestScopedIDConstructorsCarryExplicitKind(t *testing.T) {
	cases := []struct {
		name string
		got  ScopedID
		want Kind
	}{
		{"task", TaskID("gh-123"), KindTask},
		{"captain", CaptainID("cap-1"), KindCaptain},
		{"operation", OperationID("op-1"), KindOperation},
		{"resource", ResourceID("wrk-1"), KindResource},
		{"home", HomeID("home-1"), KindHome},
	}
	for _, c := range cases {
		if c.got.Kind != c.want {
			t.Errorf("%s: kind = %q, want %q", c.name, c.got.Kind, c.want)
		}
		if err := c.got.Validate(); err != nil {
			t.Errorf("%s: Validate: %v", c.name, err)
		}
	}
}

func TestCanonicalRoundTrip(t *testing.T) {
	id := TaskID("gh-123")
	if got := id.Canonical(); got != "task:gh-123" {
		t.Errorf("Canonical = %q, want task:gh-123", got)
	}
	parsed, err := ParseCanonical(id.Canonical())
	if err != nil {
		t.Fatalf("ParseCanonical: %v", err)
	}
	if parsed != id {
		t.Errorf("round trip = %+v, want %+v", parsed, id)
	}
}

func TestCanonicalIsStableAcrossConstruction(t *testing.T) {
	a := TaskID("gh-123")
	b := ScopedID{Kind: KindTask, Value: "gh-123"}
	if a.Canonical() != b.Canonical() {
		t.Errorf("canonical forms differ: %q vs %q", a.Canonical(), b.Canonical())
	}
}

func TestResolveRequiresExplicitKind(t *testing.T) {
	// Resolve with an explicit kind and valid value succeeds.
	id, err := Resolve(KindTask, "gh-123")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Kind != KindTask {
		t.Errorf("kind = %q, want task", id.Kind)
	}
	// Resolve with an unknown kind fails closed.
	if _, err := Resolve(Kind("nope"), "gh-123"); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("Resolve unknown kind: got %v, want ErrUnknownKind", err)
	}
	// Resolve with an invalid value fails closed.
	if _, err := Resolve(KindTask, "../x"); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("Resolve invalid value: got %v, want ErrInvalidIdentity", err)
	}
}

func TestParseCanonicalBareIdentityFailsClosed(t *testing.T) {
	// A bare value with no kind is ambiguous and must fail closed.
	for _, raw := range []string{"", "gh-123", "task", ":"} {
		if _, err := ParseCanonical(raw); !errors.Is(err, ErrAmbiguousIdentity) {
			t.Errorf("ParseCanonical(%q): got %v, want ErrAmbiguousIdentity", raw, err)
		}
	}
}

func TestParseCanonicalMalformedFailsClosed(t *testing.T) {
	// Multiple colons or an unknown kind are malformed.
	for _, raw := range []string{"task:a:b", "bogus:gh-123", "task:../x"} {
		if _, err := ParseCanonical(raw); err == nil {
			t.Errorf("ParseCanonical(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestScopedIDRejectsUnsafeValues(t *testing.T) {
	// Values must be contained, non-empty slugs with no path/colon separators.
	bad := []string{"", ".", "..", "a/b", "a\\b", "a:b", "/abs", "a b"}
	for _, v := range bad {
		id := ScopedID{Kind: KindTask, Value: v}
		if err := id.Validate(); !errors.Is(err, ErrInvalidIdentity) {
			t.Errorf("Validate(%q): got %v, want ErrInvalidIdentity", v, err)
		}
	}
}

func TestScopedIDValueIsContained(t *testing.T) {
	// The value must never be usable as a path traversal.
	if err := (ScopedID{Kind: KindTask, Value: "../../etc"}).Validate(); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("Validate: got %v, want ErrInvalidIdentity", err)
	}
	// A valid single-segment value validates.
	if err := (ScopedID{Kind: KindTask, Value: "safe"}).Validate(); err != nil {
		t.Fatalf("Validate(safe): %v", err)
	}
}

func TestScopedIDUnknownKindValidateFails(t *testing.T) {
	id := ScopedID{Kind: Kind("wat"), Value: "x"}
	if err := id.Validate(); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("Validate: got %v, want ErrUnknownKind", err)
	}
}

func TestKindStringIsCanonical(t *testing.T) {
	// The kinds are stable literals used in the canonical form.
	if string(KindTask) != "task" {
		t.Errorf("KindTask = %q", KindTask)
	}
	if strings.Contains(TaskID("x").Canonical(), " ") {
		t.Error("canonical form contains whitespace")
	}
}
