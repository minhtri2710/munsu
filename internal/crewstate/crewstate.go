// Package crewstate reads and reports the current state of a crewmate.
package crewstate

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

// State describes the current state of a crewmate.
type State struct {
	TaskID      string
	Status      string // one of: working, done, failed, paused, blocked, needs-decision, awaiting_approval, resolved, idle, unknown
	Description string // human-readable detail
	PaneAlive   bool   // whether the session pane is still alive
	StatusLines int    // number of status log lines

	// NoMistakesRunStep is the current run-step from the no-mistakes pipeline,
	// when the crewmate's worktree has an active or recently-completed run.
	// Values: running, fixing, ci, awaiting_approval, fix_review, checks-passed,
	// passed, failed, cancelled.
	NoMistakesRunStep string

	// StatusLogSuperseded is true when the last status log line has been
	// superseded by a no-mistakes run-step and should not be treated as
	// current-state truth.
	StatusLogSuperseded bool
}

// Read reads and synthesizes the current crewmate state.
func Read(homeDir string, id string) (*State, error) {
	s := &State{TaskID: id, Status: "unknown"}

	// 1. Read meta
	meta, err := task.ReadMeta(homeDir, id)
	if err != nil {
		// Missing meta means the task was never spawned or has been torn down.
		// Return a soft "unknown" state instead of a hard error.
		s.Status = "unknown"
		s.Description = "torn-down: no meta file for " + id
		return s, nil
	}

	// 2. Check no-mistakes run-step as authoritative source of truth
	wtPath := meta["worktree"]
	if wtPath != "" {
		currentBranch := getGitBranch(wtPath)
		if step, outcome, ok := checkNoMistakesRun(wtPath, currentBranch); ok {
			s.NoMistakesRunStep = step
			s.applyNoMistakesStep(step, outcome)
		}
	}

	// 3. Check pane liveness — only when no run-step was found
	if s.NoMistakesRunStep == "" {
		if windowID, ok := meta["window"]; ok && windowID != "" {
			// Use the backend from task meta, config, or auto-detection.
			bk, _, err := session.BackendForTask(homeDir, meta)
			if err == nil {
				s.PaneAlive = bk.Alive(windowID)
			}
			if s.PaneAlive {
				s.Status = "working"
				s.Description = "pane is alive"
			} else {
				s.Status = "idle"
				s.Description = "pane is gone"
			}
		} else {
			s.Description = "no window in meta"
		}
	}

	// 4. Read status log for the latest state — terminal states only when
	//    the run-step doesn't contradict them.
	statusLines, err := task.ReadStatus(homeDir, id)
	if err == nil && len(statusLines) > 0 {
		s.StatusLines = len(statusLines)
		lastLine := statusLines[len(statusLines)-1]

		// Extract state from last status line: "state: message"
		if colonIdx := strings.Index(lastLine, ":"); colonIdx >= 0 {
			state := strings.TrimSpace(lastLine[:colonIdx])
			msg := strings.TrimSpace(lastLine[colonIdx+1:])

			if s.NoMistakesRunStep != "" {
				// We have a run-step: the log may be superseded.
				if state == "done" || state == "failed" {
					// Terminal log state: check if the run-step contradicts.
					if s.runStepOverrides(state) {
						s.StatusLogSuperseded = true
						// Keep the run-step-derived status and description.
						return s, nil
					}
					// Run-step agrees: use the run-step's state.
					return s, nil
				}
				// Non-terminal log state is always superseded by a run-step.
				s.StatusLogSuperseded = true
				return s, nil
			}

			// No run-step: existing behavior (pane-then-log).
			if state == "done" || state == "failed" {
				// Terminal states override pane check
				s.Status = state
				s.Description = msg
			} else if s.Status == "unknown" || s.Status == "working" || s.Status == "idle" {
				// Non-terminal states only override if we don't have a better source
				if task.IsValidStatusState(state) {
					s.Status = state
					s.Description = msg
				}
			}
		}
	}

	// 5. If no status log and pane is alive, check git branch for context
	if s.StatusLines == 0 && s.PaneAlive {
		if wtPath != "" {
			branchLine := getGitBranch(wtPath)
			if branchLine != "" {
				s.Description = fmt.Sprintf("on branch %s", branchLine)
			}
		}
	}

	return s, nil
}

// applyNoMistakesStep maps a no-mistakes run-step to the crewmate status.
func (s *State) applyNoMistakesStep(step, outcome string) {
	switch step {
	case "running", "fixing", "ci":
		s.Status = "working"
		s.Description = fmt.Sprintf("no-mistakes: %s", step)
	case "awaiting_approval", "fix_review":
		s.Status = "awaiting_approval"
		s.Description = fmt.Sprintf("no-mistakes: %s", step)
	case "checks-passed", "passed":
		s.Status = "done"
		s.Description = fmt.Sprintf("no-mistakes: checks green (%s)", outcome)
	case "failed":
		s.Status = "failed"
		s.Description = fmt.Sprintf("no-mistakes: %s", outcome)
	case "cancelled":
		s.Status = "failed"
		s.Description = "no-mistakes: cancelled"
	}
}

// runStepOverrides checks whether the run-step-derived state would override a
// terminal status log state.
func (s *State) runStepOverrides(logState string) bool {
	switch s.NoMistakesRunStep {
	case "running", "fixing", "ci", "awaiting_approval", "fix_review":
		// Active run-step means the "done" or "failed" log is stale.
		return true
	case "checks-passed", "passed":
		return logState != "done"
	case "failed", "cancelled":
		return logState != "failed"
	}
	return false
}

// noMistakesResult holds parsed results from no-mistakes axi status.
type noMistakesResult struct {
	step    string // conceptual run-step
	outcome string // raw outcome string
	run     string // raw run status
	branch  string // git branch the run is on
}

// checkNoMistakesRun runs no-mistakes axi status from the worktree path and
// returns the conceptual run-step, outcome, and whether the info is relevant.
func checkNoMistakesRun(wtPath, currentBranch string) (step, outcome string, ok bool) {
	cmd := exec.Command("no-mistakes", "axi", "status")
	cmd.Dir = wtPath
	out, err := cmd.Output()
	if err != nil {
		return "", "", false
	}
	r := parseNoMistakesOutput(string(out))
	if r == nil || r.run == "" {
		return "", "", false
	}

	// Only consider runs for the current branch.
	if r.branch != "" && r.branch != currentBranch {
		return "", "", false
	}

	// Determine conceptual run-step.
	step, outcome = r.resolveStep()
	if step == "" {
		return "", "", false
	}
	return step, outcome, true
}

// resolveStep derives the conceptual run-step and outcome from parsed data.
func (r *noMistakesResult) resolveStep() (step, outcome string) {
	switch r.run {
	case "in_progress":
		return r.resolveActiveStep()
	case "completed":
		switch r.outcome {
		case "passed", "checks-passed":
			return r.outcome, r.outcome
		case "failed":
			return "failed", "failed"
		case "cancelled":
			return "cancelled", "cancelled"
		}
	}
	return "", ""
}

// resolveActiveStep finds the current step in an in-progress run.
func (r *noMistakesResult) resolveActiveStep() (step, outcome string) {
	if r.step != "" {
		return r.step, ""
	}
	// No specific step detected: treat as generic running.
	return "running", ""
}

// parseNoMistakesOutput parses the TOON output from no-mistakes axi status.
func parseNoMistakesOutput(output string) *noMistakesResult {
	r := &noMistakesResult{}
	inSteps := false

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		switch {
		case strings.HasPrefix(trimmed, "run:"):
			inSteps = false

		case strings.HasPrefix(trimmed, "steps["):
			inSteps = true

		case inSteps:
			// Check for non-step lines that break the steps block.
			if strings.HasPrefix(trimmed, "outcome:") {
				inSteps = false
				val := strings.TrimSpace(strings.TrimPrefix(trimmed, "outcome:"))
				val = strings.Trim(val, `"`)
				r.outcome = val
				break
			}
			if strings.HasPrefix(trimmed, "run:") {
				inSteps = false
				break
			}
			// Step line: "stepname,status,findings,duration_ms"
			parts := strings.Split(trimmed, ",")
			if len(parts) >= 2 {
				stepName := parts[0]
				stepStatus := parts[1]
				if stepStatus != "completed" && stepStatus != "pending" {
					// Map step status to conceptual run-step.
					// "ci" step with "running" status -> "ci" (step name matters)
					if stepName == "ci" && stepStatus == "running" {
						r.step = "ci"
					} else if stepStatus == "fixing" {
						r.step = "fixing"
					} else if stepStatus == "running" {
						r.step = "running"
					}
				}
			}

		case strings.HasPrefix(trimmed, "status:") && r.run == "":
			// This is inside run:{...} block: "  status: completed"
			// Not a step-level status.
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "status:"))
			val = strings.Trim(val, `"`)
			r.run = val

		case strings.HasPrefix(trimmed, "branch:"):
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "branch:"))
			val = strings.Trim(val, `"`)
			r.branch = val

		case strings.HasPrefix(trimmed, "outcome:"):
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "outcome:"))
			val = strings.Trim(val, `"`)
			r.outcome = val

		case strings.Contains(trimmed, "awaiting_agent:"):
			// Pipeline is parked at a gate.
			r.step = "awaiting_approval"
		}
	}

	// If no active step was found (all completed or no steps parsed) but
	// awaiting_agent was set, keep awaiting_approval.
	if r.step == "awaiting_approval" && r.run == "" {
		r.run = "in_progress"
	}

	if r.run == "" {
		return nil
	}
	return r
}

// getGitBranch returns the current git branch name from the worktree path.
func getGitBranch(wtPath string) string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = wtPath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
