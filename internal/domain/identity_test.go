package domain

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestKindKnownKindsValidate(t *testing.T) {
	for _, k := range []Kind{KindTask, KindCaptain, KindProject, KindHome, KindOperation, KindResource} {
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

func TestConstructorsProduceTypedIdentities(t *testing.T) {
	cases := []struct {
		name string
		got  Scoped
		want Kind
	}{
		{"task", mustTask(t, "gh-123"), KindTask},
		{"captain", mustCaptain(t, "cap-1"), KindCaptain},
		{"project", mustProject(t, "proj-1"), KindProject},
		{"home", mustHome(t, "home-1"), KindHome},
		{"operation", mustOperation(t, "op-1"), KindOperation},
		{"resource", mustResource(t, "wrk-1"), KindResource},
	}
	for _, c := range cases {
		if c.got.Kind() != c.want {
			t.Errorf("%s: Kind = %q, want %q", c.name, c.got.Kind(), c.want)
		}
		if err := c.got.Validate(); err != nil {
			t.Errorf("%s: Validate: %v", c.name, err)
		}
	}
}

// TestCompileTimeDistinctIdentityTypes proves the identity types are distinct
// nominal types, so the compiler rejects substituting a Home, Project, Task,
// Captain, operation, or resource where a different one is expected at an
// owner boundary. Typed owner functions take the exact concrete type; passing
// a differently-typed identity is a compile error.
func TestCompileTimeDistinctIdentityTypes(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(TaskID{}),
		reflect.TypeOf(CaptainID{}),
		reflect.TypeOf(ProjectID{}),
		reflect.TypeOf(HomeID{}),
		reflect.TypeOf(OperationID{}),
		reflect.TypeOf(ResourceID{}),
	}
	for i := range types {
		for j := range types {
			if i != j && types[i] == types[j] {
				t.Errorf("identity types are not distinct: %v == %v", types[i], types[j])
			}
		}
	}
	// A typed owner boundary accepts only its exact type.
	if err := takeTask(TaskID{}); err != nil {
		t.Fatalf("takeTask(TaskID): %v", err)
	}
}

// takeTask is a typed owner boundary: it accepts only a TaskID, not a
// CaptainID, ProjectID, HomeID, OperationID, or ResourceID. Passing a
// differently-typed identity here is a compile-time error.
func takeTask(_ TaskID) error { return nil }

func TestCanonicalRoundTrip(t *testing.T) {
	id := mustTask(t, "gh-123")
	if got := id.Canonical(); got != "task:gh-123" {
		t.Errorf("Canonical = %q, want task:gh-123", got)
	}
	parsed, err := ParseCanonical(id.Canonical())
	if err != nil {
		t.Fatalf("ParseCanonical: %v", err)
	}
	if reflect.TypeOf(parsed) != reflect.TypeOf(TaskID{}) {
		t.Errorf("parsed type = %T, want TaskID", parsed)
	}
	if parsed.Canonical() != id.Canonical() {
		t.Errorf("round trip = %q, want %q", parsed.Canonical(), id.Canonical())
	}
}

func TestParseCanonicalReturnsConcreteKind(t *testing.T) {
	cases := []struct {
		raw  string
		want reflect.Type
	}{
		{"task:gh-1", reflect.TypeOf(TaskID{})},
		{"captain:cap-1", reflect.TypeOf(CaptainID{})},
		{"project:proj-1", reflect.TypeOf(ProjectID{})},
		{"home:home-1", reflect.TypeOf(HomeID{})},
		{"operation:op-1", reflect.TypeOf(OperationID{})},
		{"resource:wrk-1", reflect.TypeOf(ResourceID{})},
	}
	for _, c := range cases {
		id, err := ParseCanonical(c.raw)
		if err != nil {
			t.Errorf("ParseCanonical(%q): %v", c.raw, err)
			continue
		}
		if reflect.TypeOf(id) != c.want {
			t.Errorf("ParseCanonical(%q) = %T, want %v", c.raw, id, c.want)
		}
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
	// Multiple colons, an unknown kind, or an unsafe value are malformed.
	for _, raw := range []string{"task:a:b", "bogus:gh-123", "task:../x"} {
		if _, err := ParseCanonical(raw); err == nil {
			t.Errorf("ParseCanonical(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestConstructorsRejectUnsafeValues(t *testing.T) {
	// Values must be contained, non-empty slugs with no path/colon separators.
	bad := []string{"", ".", "..", "a/b", "a\\b", "a:b", "/abs", "a b"}
	for _, v := range bad {
		if _, err := NewTaskID(v); !errors.Is(err, ErrInvalidIdentity) {
			t.Errorf("NewTaskID(%q): got %v, want ErrInvalidIdentity", v, err)
		}
	}
}

func TestScopedValueIsContained(t *testing.T) {
	// The value must never be usable as a path traversal.
	if _, err := NewTaskID("../../etc"); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("NewTaskID: got %v, want ErrInvalidIdentity", err)
	}
	if _, err := NewTaskID("safe"); err != nil {
		t.Fatalf("NewTaskID(safe): %v", err)
	}
}

func TestZeroValueValidateFails(t *testing.T) {
	// A zero-valued identity (never constructed via New*) fails closed.
	if err := (TaskID{}).Validate(); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("Validate zero TaskID: got %v, want ErrInvalidIdentity", err)
	}
}

func TestKindStringIsCanonical(t *testing.T) {
	if string(KindTask) != "task" {
		t.Errorf("KindTask = %q", KindTask)
	}
	if strings.Contains(mustTask(t, "x").Canonical(), " ") {
		t.Error("canonical form contains whitespace")
	}
}

// TestNoAmbientAuthorityInference is the fail-closed guard for authority
// inference: a typed identity is produced only from an explicit kind and
// value. There is no function that infers a kind from a bare string,
// environment, PATH, working directory, or home scanning; a bare value is
// always rejected.
func TestNoAmbientAuthorityInference(t *testing.T) {
	// A bare value never resolves to a typed identity without naming a kind.
	if _, err := ParseCanonical("gh-123"); !errors.Is(err, ErrAmbiguousIdentity) {
		t.Errorf("bare value resolved: %v", err)
	}
	// Naming the kind is explicit: the same value under different kinds is a
	// different identity, never inferred from context.
	task := mustTask(t, "x-1")
	captain := mustCaptain(t, "x-1")
	if task.Canonical() == captain.Canonical() {
		t.Error("same value produced the same canonical form across kinds")
	}
}

func mustTask(t *testing.T, v string) TaskID {
	t.Helper()
	id, err := NewTaskID(v)
	if err != nil {
		t.Fatalf("NewTaskID(%q): %v", v, err)
	}
	return id
}

func mustCaptain(t *testing.T, v string) CaptainID {
	t.Helper()
	id, err := NewCaptainID(v)
	if err != nil {
		t.Fatalf("NewCaptainID(%q): %v", v, err)
	}
	return id
}

func mustProject(t *testing.T, v string) ProjectID {
	t.Helper()
	id, err := NewProjectID(v)
	if err != nil {
		t.Fatalf("NewProjectID(%q): %v", v, err)
	}
	return id
}

func mustHome(t *testing.T, v string) HomeID {
	t.Helper()
	id, err := NewHomeID(v)
	if err != nil {
		t.Fatalf("NewHomeID(%q): %v", v, err)
	}
	return id
}

func mustOperation(t *testing.T, v string) OperationID {
	t.Helper()
	id, err := NewOperationID(v)
	if err != nil {
		t.Fatalf("NewOperationID(%q): %v", v, err)
	}
	return id
}

func mustResource(t *testing.T, v string) ResourceID {
	t.Helper()
	id, err := NewResourceID(v)
	if err != nil {
		t.Fatalf("NewResourceID(%q): %v", v, err)
	}
	return id
}
