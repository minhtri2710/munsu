package domain

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// errConflictSentinel stands in for the module's home.ErrConflict so the
// conflict type can be tested with a comparable underlying error.
var errConflictSentinel = errors.New("home: expected revision mismatch")

// recognizeConflict is the owning module's conflict verifier: it recognizes
// only a genuine generation/revision mismatch (here the sentinel standing in
// for home.ErrConflict).
func recognizeConflict(err error) bool { return errors.Is(err, errConflictSentinel) }

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

func TestConflictFromVerifiedMismatch(t *testing.T) {
	id := mustTask(t, "gh-1")
	p := Of(2, 3)
	c, ok := ConflictFrom(id, p, errConflictSentinel, recognizeConflict)
	if !ok {
		t.Fatal("ConflictFrom(verified conflict) returned ok=false")
	}
	if !errors.Is(c, ErrStalePrecondition) {
		t.Fatalf("errors.Is: got false, want ErrStalePrecondition")
	}
	if !errors.Is(c, errConflictSentinel) {
		t.Fatalf("errors.Is underlying: got false")
	}
	if c.ID != id {
		t.Errorf("conflict ID = %v, want %v", c.ID, id)
	}
	if c.ExpectedGeneration != 2 || c.ExpectedRevision != 3 {
		t.Errorf("expected gen/rev = %d/%d, want 2/3", c.ExpectedGeneration, c.ExpectedRevision)
	}
}

// TestConflictFromDoesNotClassifyArbitraryErrors is the fail-closed guard for
// R4: only a verified generation/revision mismatch becomes ErrStalePrecondition.
// Unrelated storage, permission, I/O, decoding, and corruption failures keep
// their truthful category and are never mislabeled as stale.
func TestConflictFromDoesNotClassifyArbitraryErrors(t *testing.T) {
	id := mustTask(t, "gh-1")
	p := Of(1, 0)
	arbitrary := []error{
		os.ErrPermission,
		os.ErrNotExist,
		os.ErrDeadlineExceeded,
		errors.New("home: decode lease"),    // decoding failure
		errors.New("home: corrupt journal"), // corruption failure
	}
	for _, err := range arbitrary {
		c, ok := ConflictFrom(id, p, err, recognizeConflict)
		if ok {
			t.Errorf("ConflictFrom(%v) misclassified as stale: %v", err, c)
		}
		if c != nil {
			t.Errorf("ConflictFrom(%v) returned non-nil conflict %v", err, c)
		}
	}
	// A nil error is never a conflict.
	if c, ok := ConflictFrom(id, p, nil, recognizeConflict); ok || c != nil {
		t.Errorf("ConflictFrom(nil) = %v/%v, want nil/false", c, ok)
	}
}

func TestConflictWithActualRecordsDiagnostics(t *testing.T) {
	c, ok := ConflictFrom(mustTask(t, "gh-1"), Of(2, 3), errConflictSentinel, recognizeConflict)
	if !ok {
		t.Fatal("ConflictFrom: ok=false")
	}
	c = c.WithActual(2, 9)
	if c.ActualGeneration != 2 || c.ActualRevision != 9 {
		t.Errorf("actual gen/rev = %d/%d, want 2/9", c.ActualGeneration, c.ActualRevision)
	}
	if !strings.Contains(c.Error(), "task:gh-1") {
		t.Errorf("Error = %q, want it to mention the canonical identity", c.Error())
	}
}

func TestConflictUnwrapReturnsUnderlying(t *testing.T) {
	c, ok := ConflictFrom(mustTask(t, "gh-1"), Of(1, 1), errConflictSentinel, recognizeConflict)
	if !ok {
		t.Fatal("ConflictFrom: ok=false")
	}
	if !errors.Is(c, errConflictSentinel) {
		t.Fatal("Unwrap did not surface underlying error")
	}
}
