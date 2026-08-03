package domain

import (
	"errors"
	"fmt"
)

// ErrAuthorityAmbiguous is returned when a mutation request does not carry a
// fully resolved scoped identity and would otherwise require ambient authority
// inference.
var ErrAuthorityAmbiguous = errors.New("munsu: authority is ambiguous; a fully resolved identity is required")

// Mutation is the fully-resolved identity context required for one cross-module
// mutation. It is the fail-closed envelope: every field is an explicit scoped
// identity and precondition. Core modules never infer authority from the
// environment, working directory, PATH, or home scanning; a caller that omits
// a required identity is rejected before any side effect.
type Mutation struct {
	Home         ScopedID
	Target       ScopedID
	Operation    Operation
	Precondition Precondition
	FenceToken   uint64
}

// Validate fails closed if any required identity is missing or ambiguous.
// FenceToken is required because a mutation must always carry the fencing
// generation that authorizes it.
func (m Mutation) Validate() error {
	if err := m.Home.Validate(); err != nil {
		return fmt.Errorf("%w: home identity: %v", ErrAuthorityAmbiguous, err)
	}
	if err := m.Target.Validate(); err != nil {
		return fmt.Errorf("%w: target identity: %v", ErrAuthorityAmbiguous, err)
	}
	if err := m.Operation.Validate(); err != nil {
		return err
	}
	if err := m.Precondition.Validate(); err != nil {
		return err
	}
	if m.FenceToken == 0 {
		return fmt.Errorf("%w: fencing token is required", ErrAuthorityAmbiguous)
	}
	return nil
}
