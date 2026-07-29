package domain

import (
	"testing"
	"time"
)

func TestNextGenerationRequiresMonotonicIncrease(t *testing.T) {
	if got, err := NextGeneration(7); err != nil || got != 8 {
		t.Fatalf("NextGeneration(7) = %d, %v", got, err)
	}
	if _, err := NextGeneration(0); err == nil {
		t.Fatal("NextGeneration accepted zero current generation")
	}
}

func TestUplinkTransitionAllowsCanonicalLifecycle(t *testing.T) {
	transitions := [][2]UplinkPhase{{UplinkIntent, UplinkDurable}, {UplinkDurable, UplinkAcked}, {UplinkAcked, UplinkRetired}}
	for _, transition := range transitions {
		if err := ValidateUplinkTransition(transition[0], transition[1]); err != nil {
			t.Fatalf("transition %s -> %s: %v", transition[0], transition[1], err)
		}
	}
}

func TestUplinkTransitionRejectsSkippedAndReverseEdges(t *testing.T) {
	for _, transition := range [][2]UplinkPhase{{UplinkIntent, UplinkAcked}, {UplinkDurable, UplinkIntent}, {UplinkRetired, UplinkIntent}} {
		if err := ValidateUplinkTransition(transition[0], transition[1]); err == nil {
			t.Fatalf("transition %s -> %s accepted", transition[0], transition[1])
		}
	}
}

func TestRetryDisposition(t *testing.T) {
	if RetryNever.ShouldRetry() || !RetryLater.ShouldRetry() {
		t.Fatal("retry policy mismatch")
	}
	after := RetryAfter(3 * time.Second)
	if !after.ShouldRetry() || after.After != 3*time.Second {
		t.Fatalf("RetryAfter = %+v", after)
	}
}

func TestTypedErrorRetainsCategoryAndRetry(t *testing.T) {
	err := NewError(ErrorConflict, "stale generation", RetryNever, nil)
	if err.Category != ErrorConflict || err.Retry.ShouldRetry() || err.Error() != "conflict: stale generation" {
		t.Fatalf("error = %+v (%s)", err, err.Error())
	}
}
