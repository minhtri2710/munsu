package domain

import (
	"fmt"
	"time"
)

type Attention string

const (
	AttentionNone          Attention = "none"
	AttentionNeedsOperator Attention = "needs-operator"
	AttentionQuarantined   Attention = "quarantined"
)

type UplinkPhase string

const (
	UplinkIntent  UplinkPhase = "intent"
	UplinkDurable UplinkPhase = "durable"
	UplinkAcked   UplinkPhase = "acked"
	UplinkRetired UplinkPhase = "retired"
)

func NextGeneration(current uint64) (uint64, error) {
	if current == 0 || current == ^uint64(0) {
		return 0, fmt.Errorf("invalid current generation %d", current)
	}
	return current + 1, nil
}

func ValidateUplinkTransition(from, to UplinkPhase) error {
	valid := (from == UplinkIntent && to == UplinkDurable) ||
		(from == UplinkDurable && to == UplinkAcked) ||
		(from == UplinkAcked && to == UplinkRetired)
	if !valid {
		return NewError(ErrorConflict, fmt.Sprintf("invalid Uplink transition %s -> %s", from, to), RetryNever, nil)
	}
	return nil
}

type ErrorCategory string

const (
	ErrorValidation   ErrorCategory = "validation"
	ErrorConflict     ErrorCategory = "conflict"
	ErrorUnavailable  ErrorCategory = "unavailable"
	ErrorCorruptState ErrorCategory = "corrupt-state"
	ErrorUnsafe       ErrorCategory = "unsafe"
	ErrorInternal     ErrorCategory = "internal"
)

type RetryDisposition struct {
	Kind  string
	After time.Duration
}

var (
	RetryNever = RetryDisposition{Kind: "never"}
	RetryLater = RetryDisposition{Kind: "later"}
)

func RetryAfter(after time.Duration) RetryDisposition {
	return RetryDisposition{Kind: "after", After: after}
}

func (r RetryDisposition) ShouldRetry() bool {
	return r.Kind == "later" || r.Kind == "after"
}

type Error struct {
	Category ErrorCategory
	Message  string
	Retry    RetryDisposition
	Cause    error
}

func NewError(category ErrorCategory, message string, retry RetryDisposition, cause error) *Error {
	return &Error{Category: category, Message: message, Retry: retry, Cause: cause}
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Category, e.Message)
}

func (e *Error) Unwrap() error {
	return e.Cause
}
