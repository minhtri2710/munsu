package crewstate

import (
	"testing"
)

// --- TOON parser fixture tests ---

// realisticNoMistakesOutputRunning is a multi-line fixture resembling a real
// no-mistakes axi status TOON output with an in-progress run.
const realisticNoMistakesOutputRunning = `run:
  id: "01JTEST"
  branch: feat/my-feature
  status: in_progress
  steps[3]{step,status,findings,duration_ms}:
    intent,completed,0,5000
    review,running,0,10000
    test,pending,0,0
outcome:`

func TestParseNoMistakesOutput_RealisticRunning(t *testing.T) {
	r := parseNoMistakesOutput(realisticNoMistakesOutputRunning)
	if r == nil {
		t.Fatal("expected parsed result for realistic running fixture")
	}
	if r.run != "in_progress" {
		t.Errorf("run = %q, want in_progress", r.run)
	}
	if r.branch != "feat/my-feature" {
		t.Errorf("branch = %q, want feat/my-feature", r.branch)
	}
	if r.step != "running" {
		t.Errorf("step = %q, want running", r.step)
	}
}

// realisticNoMistakesOutputPassed is a fixture for a completed run with outcome=passed.
const realisticNoMistakesOutputPassed = `run:
  id: "01JTEST"
  branch: feat/my-feature
  status: completed
  head: abc123def456
  pr: "https://github.com/org/repo/pull/42"
  steps[3]{step,status,findings,duration_ms}:
    intent,completed,0,5000
    review,completed,0,100000
    ci,completed,0,500000
outcome: passed`

func TestParseNoMistakesOutput_RealisticPassed(t *testing.T) {
	r := parseNoMistakesOutput(realisticNoMistakesOutputPassed)
	if r == nil {
		t.Fatal("expected parsed result for realistic passed fixture")
	}
	if r.run != "completed" {
		t.Errorf("run = %q, want completed", r.run)
	}
	if r.outcome != "passed" {
		t.Errorf("outcome = %q, want passed", r.outcome)
	}
	if r.branch != "feat/my-feature" {
		t.Errorf("branch = %q, want feat/my-feature", r.branch)
	}
}

// realisticNoMistakesOutputFailed is a fixture for a completed run with outcome=failed.
const realisticNoMistakesOutputFailed = `run:
  id: "01JTEST"
  branch: bugfix/regression
  status: completed
  steps[3]{step,status,findings,duration_ms}:
    intent,completed,0,5000
    review,failed,5,100000
    ci,pending,0,0
outcome: failed`

func TestParseNoMistakesOutput_RealisticFailed(t *testing.T) {
	r := parseNoMistakesOutput(realisticNoMistakesOutputFailed)
	if r == nil {
		t.Fatal("expected parsed result for realistic failed fixture")
	}
	if r.run != "completed" {
		t.Errorf("run = %q, want completed", r.run)
	}
	if r.outcome != "failed" {
		t.Errorf("outcome = %q, want failed", r.outcome)
	}
}

// realisticNoMistakesOutputChecksPassed is a fixture for checks-passed outcome.
const realisticNoMistakesOutputChecksPassed = `run:
  id: "01JTEST"
  branch: feat/my-feature
  status: completed
  head: abc123def456
  pr: "https://github.com/org/repo/pull/42"
  findings: 0
  steps[3]{step,status,findings,duration_ms}:
    intent,completed,0,5000
    review,completed,0,10000
    ci,completed,0,500000
outcome: checks-passed`

func TestParseNoMistakesOutput_RealisticChecksPassed(t *testing.T) {
	r := parseNoMistakesOutput(realisticNoMistakesOutputChecksPassed)
	if r == nil {
		t.Fatal("expected parsed result for realistic checks-passed fixture")
	}
	if r.outcome != "checks-passed" {
		t.Errorf("outcome = %q, want checks-passed", r.outcome)
	}
}

// realisticNoMistakesOutputAwaiting is a fixture for a run awaiting approval.
const realisticNoMistakesOutputAwaiting = `run:
  id: "01JTEST"
  branch: feat/my-feature
  status: in_progress
  awaiting_agent: parked 2m30s
  steps[3]{step,status,findings,duration_ms}:
    intent,completed,0,5000
    review,completed,0,10000
    test,completed,0,0
outcome:`

func TestParseNoMistakesOutput_RealisticAwaiting(t *testing.T) {
	r := parseNoMistakesOutput(realisticNoMistakesOutputAwaiting)
	if r == nil {
		t.Fatal("expected parsed result for realistic awaiting fixture")
	}
	if r.step != "awaiting_approval" {
		t.Errorf("step = %q, want awaiting_approval", r.step)
	}
	if r.run != "in_progress" {
		t.Errorf("run = %q, want in_progress", r.run)
	}
}
