package orchestrator

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrScoutBudgetMissingTimestamp = errors.New("scout runtime budget missing timestamp")
	ErrScoutBudgetExceeded         = errors.New("scout runtime budget exceeded")
)

type ScoutBudgetOutcome string

const (
	ScoutBudgetWithin   ScoutBudgetOutcome = "within-budget"
	ScoutBudgetRejected ScoutBudgetOutcome = "over-budget"
)

type ScoutBudgetEvidence struct {
	Outcome        ScoutBudgetOutcome `json:"outcome"`
	BudgetSecs     int64              `json:"budget_secs"`
	StartedAtUnix  int64              `json:"started_at_unix"`
	ObservedAtUnix int64              `json:"observed_at_unix"`
	ElapsedSecs    int64              `json:"elapsed_secs"`
}

type ScoutBudgetError struct{ Evidence ScoutBudgetEvidence }

func (e *ScoutBudgetError) Error() string {
	return fmt.Sprintf("%v: elapsed=%ds budget=%ds", ErrScoutBudgetExceeded, e.Evidence.ElapsedSecs, e.Evidence.BudgetSecs)
}
func (e *ScoutBudgetError) Unwrap() error { return ErrScoutBudgetExceeded }

func EnforceScoutRuntimeBudget(budgetSecs, startedAtUnix, observedAtUnix int64, now time.Time) (ScoutBudgetEvidence, error) {
	if budgetSecs <= 0 || startedAtUnix <= 0 || observedAtUnix <= 0 {
		return ScoutBudgetEvidence{}, fmt.Errorf("%w: budget=%d started_at=%d observed_at=%d", ErrScoutBudgetMissingTimestamp, budgetSecs, startedAtUnix, observedAtUnix)
	}
	if now.IsZero() {
		now = time.Now()
	}
	observed := time.Unix(observedAtUnix, 0)
	if now.Before(observed) {
		now = observed
	}
	elapsed := int64(now.Sub(time.Unix(startedAtUnix, 0)) / time.Second)
	evidence := ScoutBudgetEvidence{BudgetSecs: budgetSecs, StartedAtUnix: startedAtUnix, ObservedAtUnix: observedAtUnix, ElapsedSecs: elapsed}
	if elapsed > budgetSecs {
		evidence.Outcome = ScoutBudgetRejected
		return evidence, &ScoutBudgetError{Evidence: evidence}
	}
	evidence.Outcome = ScoutBudgetWithin
	return evidence, nil
}
