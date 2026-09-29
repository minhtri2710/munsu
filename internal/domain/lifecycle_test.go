package domain

import "testing"

func TestTypedErrorRetainsCategoryAndRetry(t *testing.T) {
	err := NewError(ErrorConflict, "stale generation", RetryNever, nil)
	if err.Category != ErrorConflict || err.Retry != RetryNever || err.Error() != "conflict: stale generation" {
		t.Fatalf("error = %+v (%s)", err, err.Error())
	}
}
