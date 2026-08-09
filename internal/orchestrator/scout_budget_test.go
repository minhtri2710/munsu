package orchestrator

import (
	"errors"
	"testing"
	"time"
)

func TestEnforceScoutRuntimeBudget(t *testing.T) {
	start := int64(100)
	cases := []struct {
		name    string
		now     int64
		want    ScoutBudgetOutcome
		wantErr error
	}{
		{"under-budget", 109, ScoutBudgetWithin, nil},
		{"exact-boundary", 110, ScoutBudgetWithin, nil},
		{"over-budget", 111, ScoutBudgetRejected, ErrScoutBudgetExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evidence, err := EnforceScoutRuntimeBudget(10, start, start, time.Unix(tc.now, 0))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, want %v", err, tc.wantErr)
			}
			if evidence.Outcome != tc.want {
				t.Fatalf("outcome=%q, want %q", evidence.Outcome, tc.want)
			}
			if evidence.ElapsedSecs != tc.now-start {
				t.Fatalf("elapsed=%d, want %d", evidence.ElapsedSecs, tc.now-start)
			}
		})
	}
}

func TestEnforceScoutRuntimeBudgetMissingTimestamps(t *testing.T) {
	_, err := EnforceScoutRuntimeBudget(10, 0, 100, time.Unix(100, 0))
	if !errors.Is(err, ErrScoutBudgetMissingTimestamp) {
		t.Fatalf("err=%v", err)
	}
}

func TestEnforceScoutRuntimeBudgetReplayIsIdempotent(t *testing.T) {
	now := time.Unix(111, 0)
	first, firstErr := EnforceScoutRuntimeBudget(10, 100, 100, now)
	second, secondErr := EnforceScoutRuntimeBudget(10, 100, 100, now)
	if first != second {
		t.Fatalf("evidence changed on replay: %#v != %#v", first, second)
	}
	if firstErr == nil || secondErr == nil || !errors.Is(firstErr, ErrScoutBudgetExceeded) || !errors.Is(secondErr, ErrScoutBudgetExceeded) {
		t.Fatalf("replay errors: %v, %v", firstErr, secondErr)
	}
}
