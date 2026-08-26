package domain

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestKindKnownKindsValidate(t *testing.T) {
	for _, k := range []Kind{KindTask, KindCaptain, KindProject, KindHome, KindOperation} {
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

func TestTypedIdentityCanonicalFormsRemainDistinct(t *testing.T) {
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
