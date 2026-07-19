// Package soldierstate reads and reports the current state of a soldier.
package soldierstate

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/classify"
	"github.com/minhtri2710/munsu/internal/nostatus"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

// State describes the current state of a soldier.
type State struct {
	TaskID      string
	Status      string // one of: working, done, failed, paused, blocked, needs-decision, awaiting_approval, idle, unknown
	Description string // human-readable detail
	PaneAlive   bool   // whether the session pane is still alive
	StatusLines int    // number of status log lines

	// NoMistakesRunStep is the current run-step from the no-mistakes pipeline,
	// when the soldier's worktree has an active or recently-completed run.
	// Values: running, fixing, ci, awaiting_approval, fix_review, checks-passed,
	// passed, failed, cancelled.
	NoMistakesRunStep string

	// StatusLogSuperseded is true when the last status log line has been
	// superseded by a no-mistakes run-step and should not be treated as
	// current-state truth.
	StatusLogSuperseded bool

	// OpenActivities are still-open keyed work phases from the status event log
	// (working/paused open; done/failed/resolved/etc. close). Evidence only —
	// Status is the current-state authority (no-mistakes / pane / last state verb).
	OpenActivities []classify.Activity
}

// Read reads and synthesizes the current soldier state.
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

	// 4. Status file is an append-only event log. Fold open keyed activities,
	// then map the last state-bearing line (not a pure close event like resolved).
	statusLines, err := task.ReadStatus(homeDir, id)
	if err == nil && len(statusLines) > 0 {
		s.StatusLines = len(statusLines)
		statusPath := filepath.Join(task.StateDir(homeDir), id+".status")
		s.OpenActivities = classify.OpenActivities(statusPath)

		lastLine := lastStateBearingLine(statusLines)
		if lastLine != "" {
			state, msg := statusVerbAndNote(lastLine)

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

			// No run-step: pane-then-log, skipping pure close verbs.
			if state == "done" || state == "failed" {
				s.Status = state
				s.Description = msg
			} else if s.Status == "unknown" || s.Status == "working" || s.Status == "idle" {
				if task.IsValidStatusState(state) && state != "resolved" {
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

// applyNoMistakesStep maps a no-mistakes run-step to the soldier status.
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

// checkNoMistakesRun reads no-mistakes run status from the worktree path, using
// the structured nostatus package at the CLI boundary, and returns the conceptual
// run-step, outcome, and whether the info is relevant.
func checkNoMistakesRun(wtPath, currentBranch string) (step, outcome string, ok bool) {
	r, err := nostatus.Read(wtPath)
	if err != nil {
		return "", "", false
	}

	// Only consider runs for the current branch.
	if r.Branch != "" && r.Branch != currentBranch {
		return "", "", false
	}

	// Determine conceptual run-step.
	step, outcome = r.ConceptualStep()
	if step == "" {
		return "", "", false
	}
	return step, outcome, true
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

// lastStateBearingLine returns the last non-empty status line when it carries a
// current-state verb. Pure close events (resolved, captain-held) only fold keys
// and must not become Status or resurrect an earlier line (firstmate crew-state).
func lastStateBearingLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		verb, _ := statusVerbAndNote(line)
		switch verb {
		case "resolved", "captain-held":
			// Close-only event: no status-log current state.
			return ""
		case "working", "paused", "blocked", "needs-decision", "done", "failed", "awaiting_approval":
			return line
		default:
			if task.IsValidStatusState(verb) && verb != "resolved" {
				return line
			}
			if strings.Contains(line, ":") {
				return line
			}
			return ""
		}
	}
	return ""
}

// statusVerbAndNote extracts the leading verb (optional [key=…] stripped) and note.
func statusVerbAndNote(line string) (verb, note string) {
	line = strings.TrimSpace(line)
	before, after, found := strings.Cut(line, ":")
	if idx := strings.Index(before, "[key="); idx >= 0 {
		before = strings.TrimSpace(before[:idx])
	}
	verb = strings.TrimSpace(before)
	if found {
		return verb, strings.TrimSpace(after)
	}
	return verb, ""
}
