package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestPreconditionExpectedRevision(t *testing.T) {
	p := Of(2, 3)
	if p.ExpectedRevision() != 3 {
		t.Errorf("ExpectedRevision = %d, want 3", p.ExpectedRevision())
	}
	// Initial state: revision zero is the fresh-scope expected revision.
	initial := Of(1, 0)
	if initial.ExpectedRevision() != 0 {
		t.Errorf("initial ExpectedRevision = %d, want 0", initial.ExpectedRevision())
	}
}

func TestPreconditionValidate(t *testing.T) {
	// Generation must be positive.
	if err := (Precondition{Generation: 0, Revision: 1}).Validate(); !errors.Is(err, ErrInvalidPrecondition) {
		t.Fatalf("Validate(zero gen): got %v, want ErrInvalidPrecondition", err)
	}
	// Revision zero is valid (initial state).
	if err := (Precondition{Generation: 1, Revision: 0}).Validate(); err != nil {
		t.Fatalf("Validate(rev 0): %v", err)
	}
	if err := (Precondition{Generation: 1, Revision: 1}).Validate(); err != nil {
		t.Fatalf("Validate(valid): %v", err)
	}
}

func TestConflictMatchesStalePrecondition(t *testing.T) {
	id := TaskID("gh-1")
	p := Of(2, 3)
	c := Reconcile(id, p, errConflictSentinel)
	if !errors.Is(c, ErrStalePrecondition) {
		t.Fatalf("errors.Is: got false, want ErrStalePrecondition")
	}
	if !errors.Is(c, errConflictSentinel) {
		t.Fatalf("errors.Is underlying: got false")
	}
	if id != c.ID {
		t.Errorf("conflict ID = %+v, want %+v", c.ID, id)
	}
	if c.ExpectedGeneration != 2 || c.ExpectedRevision != 3 {
		t.Errorf("expected gen/rev = %d/%d, want 2/3", c.ExpectedGeneration, c.ExpectedRevision)
	}
}

func TestConflictWithActualRecordsDiagnostics(t *testing.T) {
	c := Reconcile(TaskID("gh-1"), Of(2, 3), errConflictSentinel)
	c = c.WithActual(2, 9)
	if c.ActualGeneration != 2 || c.ActualRevision != 9 {
		t.Errorf("actual gen/rev = %d/%d, want 2/9", c.ActualGeneration, c.ActualRevision)
	}
	if !strings.Contains(c.Error(), "task:gh-1") {
		t.Errorf("Error = %q, want it to mention the canonical identity", c.Error())
	}
}

func TestConflictUnwrapReturnsUnderlying(t *testing.T) {
	c := Reconcile(TaskID("gh-1"), Of(1, 1), errConflictSentinel)
	if !errors.Is(c, errConflictSentinel) {
		t.Fatal("Unwrap did not surface underlying error")
	}
}

// errConflictSentinel stands in for the module's home.ErrConflict so the
// conflict type can be tested with a comparable underlying error.
var errConflictSentinel = errors.New("home: expected revision mismatch")
