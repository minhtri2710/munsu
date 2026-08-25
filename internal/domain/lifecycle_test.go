package domain

import (
	"testing"
	"time"
)

func TestRetryDisposition(t *testing.T) {
	if RetryNever.ShouldRetry() || !RetryLater.ShouldRetry() {
		t.Fatal("retry policy mismatch")
	}
	if (RetryDisposition{Kind: "after", After: 3 * time.Second}).ShouldRetry() != true {
		t.Fatal("after retry disposition should be retryable")
	}
}

func TestTypedErrorRetainsCategoryAndRetry(t *testing.T) {
	err := NewError(ErrorConflict, "stale generation", RetryNever, nil)
	if err.Category != ErrorConflict || err.Retry.ShouldRetry() || err.Error() != "conflict: stale generation" {
		t.Fatalf("error = %+v (%s)", err, err.Error())
	}
}
