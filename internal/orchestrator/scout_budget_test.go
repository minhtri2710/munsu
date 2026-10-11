package orchestrator

import (
	"errors"
	"testing"
	"time"
)

func TestEnforceScoutRuntimeBudget(t *testing.T) {
	cases := []struct {
		name     string
		started  int64
		observed int64
		now      int64
		want     ScoutBudgetOutcome
		wantErr  error
	}{
		{name: "missing start timestamp", started: 0, observed: 100, now: 100, wantErr: ErrScoutBudgetMissingTimestamp},
		{name: "under-budget", started: 100, observed: 100, now: 109, want: ScoutBudgetWithin},
		{name: "exact-boundary", started: 100, observed: 100, now: 110, want: ScoutBudgetWithin},
		{name: "over-budget", started: 100, observed: 100, now: 111, want: ScoutBudgetRejected, wantErr: ErrScoutBudgetExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evidence, err := EnforceScoutRuntimeBudget(10, tc.started, tc.observed, time.Unix(tc.now, 0))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, want %v", err, tc.wantErr)
			}
			if evidence.Outcome != tc.want {
				t.Fatalf("outcome=%q, want %q", evidence.Outcome, tc.want)
			}
			if tc.wantErr == ErrScoutBudgetMissingTimestamp {
				return
			}
			if evidence.ElapsedSecs != tc.now-tc.started {
				t.Fatalf("elapsed=%d, want %d", evidence.ElapsedSecs, tc.now-tc.started)
			}
		})
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
