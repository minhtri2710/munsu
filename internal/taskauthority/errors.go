// Package taskauthority owns task lifecycle, readiness, and durable dispatch
// control as one deep module. Callers use named semantic operations; the
// implementation seam below the business rules is the transactional Store.
package taskauthority

import (
	"errors"
	"fmt"

	"github.com/minhtri2710/munsu/internal/domain"
)

// Sentinel errors reachable through errors.Is on every typed error returned
// by the Authority and the Store adapters.
var (
	// ErrNotFound means no current task generation exists for the task ID.
	ErrNotFound = errors.New("task not found")
	// ErrConflict means the operation violates a lifecycle or identity rule.
	ErrConflict = errors.New("task conflict")
	// ErrPrecondition means a lifecycle precondition failed.
	ErrPrecondition = errors.New("task lifecycle precondition failed")
	// ErrDispatchHeld means a durable Dispatch Hold blocks the action.
	ErrDispatchHeld = errors.New("dispatch is held")
	// ErrHoldNotFound means the named dispatch hold does not exist.
	ErrHoldNotFound = errors.New("dispatch hold not found")
	// ErrDecisionNotFound means the named dispatch decision does not exist.
	ErrDecisionNotFound = errors.New("dispatch decision not found")
	// ErrOperationConflict means an operation ID was reused with a different
	// request digest (non-retryable identity conflict).
	ErrOperationConflict = errors.New("operation identity reused with different intent")
	// ErrInvalidGeneration means a generation value is not a valid positive
	// monotonic identity.
	ErrInvalidGeneration = errors.New("invalid task generation")
	// ErrInvalidInput means a request field or record violates validation.
	ErrInvalidInput = errors.New("invalid input")
	// ErrMergeMutationRefused means a remote-unknown merge outcome is
	// committed and the Authority refuses further provider-mutating merge
	// attempts; only read reconciliation is permitted (Task 7.6).
	ErrMergeMutationRefused = errors.New("merge mutation refused: remote-unknown outcome permits read reconciliation only")
)

// conflictError wraps a sentinel cause in a typed domain error so callers can
// classify by category or match by sentinel.
func conflictError(sentinel error, format string, args ...any) error {
	return domain.NewError(domain.ErrorConflict, fmt.Sprintf(format, args...), domain.RetryNever, sentinel)
}

func validationError(format string, args ...any) error {
	return domain.NewError(domain.ErrorValidation, fmt.Sprintf(format, args...), domain.RetryNever, ErrInvalidInput)
}

func internalError(format string, args ...any) error {
	return domain.NewError(domain.ErrorInternal, fmt.Sprintf(format, args...), domain.RetryNever, nil)
}
