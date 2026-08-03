package domain

import (
	"errors"
	"fmt"
)

// ErrStalePrecondition is the typed conflict returned when a mutation's
// expected generation or revision does not match current state. Stale intent
// fails closed instead of being replayed against newer state.
var ErrStalePrecondition = errors.New("munsu: stale mutation precondition")

// ErrInvalidPrecondition reports a malformed mutation precondition.
var ErrInvalidPrecondition = errors.New("munsu: invalid mutation precondition")

// Precondition is the optimistic expected state of a mutation target before a
// cross-module mutation. It binds the expected generation and revision in a
// typed form so a stale intent is detected as a typed conflict rather than
// silently replayed.
//
// Revision zero is the initial expected state of a fresh scope (home.Commit's
// first expectedRevision); generation is always positive for a concrete
// mutation target.
type Precondition struct {
	Generation uint64
	Revision   uint64
}

// Of returns a Precondition for the given expected generation and revision.
func Of(gen, rev uint64) Precondition {
	return Precondition{Generation: gen, Revision: rev}
}

// ExpectedRevision returns the expectedRevision value to pass to home.Commit.
func (p Precondition) ExpectedRevision() uint64 { return p.Revision }

// Validate rejects a precondition whose generation is zero (a mutation must
// always target an explicit generation). Revision zero is the initial state.
func (p Precondition) Validate() error {
	if p.Generation == 0 {
		return fmt.Errorf("%w: generation must be positive", ErrInvalidPrecondition)
	}
	return nil
}

// Conflict is a typed stale-precondition conflict. It wraps the underlying
// storage conflict (home.ErrConflict) so callers can distinguish a stale
// intent from other failures, and it satisfies errors.Is for
// ErrStalePrecondition.
type Conflict struct {
	ID                 ScopedID
	ExpectedGeneration uint64
	ActualGeneration   uint64
	ExpectedRevision   uint64
	ActualRevision     uint64
	Err                error
}

// Reconcile wraps a home.Commit conflict (or any error) into a typed Conflict
// for the given target identity and precondition. The stored actual values are
// left zero until the caller records them with WithActual.
func Reconcile(id ScopedID, p Precondition, err error) *Conflict {
	return &Conflict{
		ID:                 id,
		ExpectedGeneration: p.Generation,
		ExpectedRevision:   p.Revision,
		Err:                err,
	}
}

// WithActual records the current generation and revision observed at conflict
// time for diagnostics.
func (c *Conflict) WithActual(gen, rev uint64) *Conflict {
	c.ActualGeneration = gen
	c.ActualRevision = rev
	return c
}

// Error renders the conflict for diagnostics.
func (c *Conflict) Error() string {
	return fmt.Sprintf(
		"munsu: stale precondition for %s: expected gen=%d rev=%d",
		c.ID.Canonical(), c.ExpectedGeneration, c.ExpectedRevision,
	)
}

// Unwrap returns the underlying storage error so callers can distinguish a
// home conflict from other failures.
func (c *Conflict) Unwrap() error { return c.Err }

// Is makes errors.Is(err, ErrStalePrecondition) true for a Conflict.
func (c *Conflict) Is(target error) bool { return target == ErrStalePrecondition }
