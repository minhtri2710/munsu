package fleet

import (
	"strings"
	"testing"
)

// --- TOON parser fixtures ---

const realisticNoMistakesOutputRunning = `run:
  id: "01JTEST"
  branch: feat/my-feature
  status: in_progress
  steps[3]{step,status,findings,duration_ms}:
    intent,completed,0,5000
    review,running,0,10000
    test,pending,0,0
outcome:`

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

const realisticNoMistakesOutputFailed = `run:
  id: "01JTEST"
  branch: bugfix/regression
  status: completed
  steps[3]{step,status,findings,duration_ms}:
    intent,completed,0,5000
    review,failed,5,100000
    ci,pending,0,0
outcome: failed`

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

const noMistakesOutputActiveRunning = `run:
  id: "01TEST"
  branch: feat/test
  status: in_progress
  steps[3]{step,status,findings,duration_ms}:
    intent,completed,0,5000
    review,running,0,10000
    test,pending,0,0
outcome:`

const noMistakesOutputActiveFixing = `run:
  id: "01TEST"
  branch: feat/test
  status: in_progress
  steps[3]{step,status,findings,duration_ms}:
    intent,completed,0,5000
    review,fixing,0,10000
    test,pending,0,0
outcome:`

const noMistakesOutputActiveCI = `run:
  id: "01TEST"
  branch: feat/test
  status: in_progress
  steps[3]{step,status,findings,duration_ms}:
    intent,completed,0,5000
    review,completed,0,10000
    ci,running,0,0
outcome:`

const noMistakesOutputAwaitingApproval = `run:
  id: "01TEST"
  branch: feat/test
  status: in_progress
  awaiting_agent: parked 2m30s
  steps[3]{step,status,findings,duration_ms}:
    intent,completed,0,5000
    review,completed,0,10000
    test,completed,0,0
outcome:`

const noMistakesOutputCompletedPassed = `run:
  id: "01TEST"
  branch: feat/test
  status: completed
  head: abc123
  pr: "https://github.com/org/repo/pull/1"
  steps[3]{step,status,findings,duration_ms}:
    intent,completed,0,5000
    review,completed,3,100000
    ci,completed,0,500000
outcome: passed`

const noMistakesOutputCompletedFailed = `run:
  id: "01TEST"
  branch: feat/test
  status: completed
  steps[3]{step,status,findings,duration_ms}:
    intent,completed,0,5000
    review,failed,5,100000
    ci,pending,0,0
outcome: failed`

const noMistakesOutputChecksPassed = `run:
  id: "01TEST"
  branch: feat/test
  status: completed
  head: abc123
  pr: "https://github.com/org/repo/pull/1"
  findings: 0
  steps[3]{step,status,findings,duration_ms}:
    intent,completed,0,5000
    review,completed,0,10000
    ci,completed,0,500000
outcome: checks-passed`

const noMistakesOutputNoRun = `bin: ~/.no-mistakes/bin/no-mistakes
daemon: running
runs[1]{id,branch,status,head,pr}:
  "01LEGACY",main,completed,abc123,""`

// --- Tests ---

func TestParse_RealisticRunning(t *testing.T) {
	r, err := Parse(realisticNoMistakesOutputRunning)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "in_progress" {
		t.Errorf("Status = %q, want in_progress", r.Status)
	}
	if r.Branch != "feat/my-feature" {
		t.Errorf("Branch = %q, want feat/my-feature", r.Branch)
	}
	step, _ := r.ConceptualStep()
	if step != "running" {
		t.Errorf("ConceptualStep = %q, want running", step)
	}
}

func TestParse_RealisticPassed(t *testing.T) {
	r, err := Parse(realisticNoMistakesOutputPassed)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "completed" {
		t.Errorf("Status = %q, want completed", r.Status)
	}
	if r.Outcome != "passed" {
		t.Errorf("Outcome = %q, want passed", r.Outcome)
	}
	if r.Branch != "feat/my-feature" {
		t.Errorf("Branch = %q, want feat/my-feature", r.Branch)
	}
	if r.Head != "abc123def456" {
		t.Errorf("Head = %q, want abc123def456", r.Head)
	}
}

func TestParse_RealisticFailed(t *testing.T) {
	r, err := Parse(realisticNoMistakesOutputFailed)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "completed" {
		t.Errorf("Status = %q, want completed", r.Status)
	}
	if r.Outcome != "failed" {
		t.Errorf("Outcome = %q, want failed", r.Outcome)
	}
	if r.Branch != "bugfix/regression" {
		t.Errorf("Branch = %q, want bugfix/regression", r.Branch)
	}
}

func TestParse_RealisticChecksPassed(t *testing.T) {
	r, err := Parse(realisticNoMistakesOutputChecksPassed)
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != "checks-passed" {
		t.Errorf("Outcome = %q, want checks-passed", r.Outcome)
	}
}

func TestParse_RealisticAwaiting(t *testing.T) {
	r, err := Parse(realisticNoMistakesOutputAwaiting)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "in_progress" {
		t.Errorf("Status = %q, want in_progress", r.Status)
	}
	if r.AwaitingAgent != "parked 2m30s" {
		t.Errorf("AwaitingAgent = %q, want parked 2m30s", r.AwaitingAgent)
	}
	step, _ := r.ConceptualStep()
	if step != "awaiting_approval" {
		t.Errorf("ConceptualStep = %q, want awaiting_approval", step)
	}
}

func TestParse_ActiveRunning(t *testing.T) {
	r, err := Parse(noMistakesOutputActiveRunning)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "in_progress" {
		t.Errorf("Status = %q, want in_progress", r.Status)
	}
	if r.Branch != "feat/test" {
		t.Errorf("Branch = %q, want feat/test", r.Branch)
	}
	step, _ := r.ConceptualStep()
	if step != "running" {
		t.Errorf("ConceptualStep = %q, want running", step)
	}
}

func TestParse_ActiveFixing(t *testing.T) {
	r, err := Parse(noMistakesOutputActiveFixing)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "in_progress" {
		t.Errorf("Status = %q, want in_progress", r.Status)
	}
	step, _ := r.ConceptualStep()
	if step != "fixing" {
		t.Errorf("ConceptualStep = %q, want fixing", step)
	}
}

func TestParse_ActiveCI(t *testing.T) {
	r, err := Parse(noMistakesOutputActiveCI)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "in_progress" {
		t.Errorf("Status = %q, want in_progress", r.Status)
	}
	step, _ := r.ConceptualStep()
	if step != "ci" {
		t.Errorf("ConceptualStep = %q, want ci", step)
	}
}

func TestParse_AwaitingApproval(t *testing.T) {
	r, err := Parse(noMistakesOutputAwaitingApproval)
	if err != nil {
		t.Fatal(err)
	}
	step, _ := r.ConceptualStep()
	if step != "awaiting_approval" {
		t.Errorf("ConceptualStep = %q, want awaiting_approval", step)
	}
}

func TestParse_CompletedPassed(t *testing.T) {
	r, err := Parse(noMistakesOutputCompletedPassed)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "completed" {
		t.Errorf("Status = %q, want completed", r.Status)
	}
	if r.Outcome != "passed" {
		t.Errorf("Outcome = %q, want passed", r.Outcome)
	}
}

func TestParse_CompletedFailed(t *testing.T) {
	r, err := Parse(noMistakesOutputCompletedFailed)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "completed" {
		t.Errorf("Status = %q, want completed", r.Status)
	}
	if r.Outcome != "failed" {
		t.Errorf("Outcome = %q, want failed", r.Outcome)
	}
}

func TestParse_ChecksPassed(t *testing.T) {
	r, err := Parse(noMistakesOutputChecksPassed)
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != "checks-passed" {
		t.Errorf("Outcome = %q, want checks-passed", r.Outcome)
	}
}

func TestParse_NoRun(t *testing.T) {
	r, err := Parse(noMistakesOutputNoRun)
	if r != nil {
		t.Error("expected nil result for no-run output")
	}
	if err != ErrNoActiveRun {
		t.Errorf("err = %v, want ErrNoActiveRun", err)
	}
}

func TestParse_ErrorOutput(t *testing.T) {
	output := `run:
  id: "01TEST"
  branch: feat/test
  status: failed
  head: abc123
  findings: none
  steps[0]:
outcome: failed
error: "gate agent \"pi\" does not neutralize target repo agent-instruction files"`
	r, err := Parse(output)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "failed" {
		t.Errorf("Status = %q, want failed", r.Status)
	}
	if r.Outcome != "failed" {
		t.Errorf("Outcome = %q, want failed", r.Outcome)
	}
	if !strings.Contains(r.Error, "gate agent") {
		t.Errorf("Error should mention 'gate agent', got %q", r.Error)
	}
}

// --- ConceptualStep tests ---

func TestConceptualStep_ActiveRunning(t *testing.T) {
	r := &RunStatus{Status: "in_progress"}
	r.Steps = []Step{{Name: "intent", Status: "completed"}, {Name: "review", Status: "running"}}
	step, outcome := r.ConceptualStep()
	if step != "running" {
		t.Errorf("step = %q, want running", step)
	}
	if outcome != "" {
		t.Errorf("outcome = %q, want empty", outcome)
	}
}

func TestConceptualStep_CompletedPassed(t *testing.T) {
	r := &RunStatus{Status: "completed", Outcome: "passed"}
	step, outcome := r.ConceptualStep()
	if step != "passed" {
		t.Errorf("step = %q, want passed", step)
	}
	if outcome != "passed" {
		t.Errorf("outcome = %q, want passed", outcome)
	}
}

func TestConceptualStep_CompletedChecksPassed(t *testing.T) {
	r := &RunStatus{Status: "completed", Outcome: "checks-passed"}
	step, outcome := r.ConceptualStep()
	if step != "checks-passed" {
		t.Errorf("step = %q, want checks-passed", step)
	}
	if outcome != "checks-passed" {
		t.Errorf("outcome = %q, want checks-passed", outcome)
	}
}

func TestConceptualStep_CompletedFailed(t *testing.T) {
	r := &RunStatus{Status: "completed", Outcome: "failed"}
	step, outcome := r.ConceptualStep()
	if step != "failed" {
		t.Errorf("step = %q, want failed", step)
	}
	if outcome != "failed" {
		t.Errorf("outcome = %q, want failed", outcome)
	}
}

func TestConceptualStep_CompletedCancelled(t *testing.T) {
	r := &RunStatus{Status: "completed", Outcome: "cancelled"}
	step, outcome := r.ConceptualStep()
	if step != "cancelled" {
		t.Errorf("step = %q, want cancelled", step)
	}
	if outcome != "cancelled" {
		t.Errorf("outcome = %q, want cancelled", outcome)
	}
}

func TestConceptualStep_UnknownStatus(t *testing.T) {
	r := &RunStatus{Status: "unknown"}
	step, outcome := r.ConceptualStep()
	if step != "" {
		t.Errorf("step = %q, want empty", step)
	}
	if outcome != "" {
		t.Errorf("outcome = %q, want empty", outcome)
	}
}

func TestParse_StepsList(t *testing.T) {
	r, err := Parse(realisticNoMistakesOutputRunning)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(r.Steps))
	}
	expected := []struct {
		name   string
		status string
	}{
		{"intent", "completed"},
		{"review", "running"},
		{"test", "pending"},
	}
	for i, s := range r.Steps {
		if s.Name != expected[i].name {
			t.Errorf("Steps[%d].Name = %q, want %q", i, s.Name, expected[i].name)
		}
		if s.Status != expected[i].status {
			t.Errorf("Steps[%d].Status = %q, want %q", i, s.Status, expected[i].status)
		}
	}
}
