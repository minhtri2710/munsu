package domain

import (
	"fmt"
	"time"
)

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
