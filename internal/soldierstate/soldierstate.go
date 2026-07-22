// Package soldierstate reads and reports the current state of a soldier.
// The precedence hierarchy for current-state truth is:
//  1. Backlog state (tasks-axi or manual backlog: done/blocked)
//  2. Task meta file (status + description from last report)
//  3. Provider state (GitHub PR merged/closed/open)
//  4. Typed event log (keyed open/close transitions)
//  5. Status file fallback (append-only .status lines)
//
// Pane prose is diagnostic only — never derived as current-state truth.
package soldierstate

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/backlog"
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
	PaneAlive   bool   // whether the session pane is still alive (diagnostic only — not truth)
	StatusLines int    // number of status log lines

	// NoMistakesRunStep is the current run-step from the no-mistakes pipeline,
	// when the soldier's worktree has an active or recently-completed run.
	// Values: running, fixing, ci, awaiting_approval, fix_review, checks-passed,
	// passed, failed, cancelled.
	NoMistakesRunStep string

	// StatusLogSuperseded is true when the last status log line has been
	// superseded by a higher-precedence source.
	StatusLogSuperseded bool

	// OpenActivities are still-open keyed work phases from the status event log
	// (working/paused open; done/failed/resolved/etc. close). Evidence only —
	// Status is the current-state authority.
	OpenActivities []classify.Activity

	// BacklogState is the task state from the backlog file when available.
	BacklogState string `json:"backlog_state,omitempty"`
}

// Read reads and synthesizes the current soldier state using the
// precedence hierarchy: backlog > meta/last-report > PR state > typed events > status fallback.
func Read(homeDir string, id string) (*State, error) {
	s := &State{TaskID: id, Status: "unknown"}

	// Read meta (always needed for context)
	meta, err := task.ReadMeta(homeDir, id)
	if err != nil {
		// Missing meta means the task was never spawned or has been torn down.
		// Return a soft "unknown" state instead of a hard error.
		s.Status = "unknown"
		s.Description = "torn-down: no meta file for " + id
		return s, nil
	}

	// Read status lines (needed by multiple tiers)
	statusLines, _ := task.ReadStatus(homeDir, id)
	statusPath := filepath.Join(task.StateDir(homeDir), id+".status")
	if len(statusLines) > 0 {
		s.StatusLines = len(statusLines)
	}
	s.OpenActivities = classify.OpenActivities(statusPath)

	// --- No-mistakes run-step (pipeline state) ---
	// This is a fast local check that feeds into the hierarchy below.
	wtPath := meta["worktree"]
	if wtPath != "" {
		currentBranch := getGitBranch(wtPath)
		if step, outcome, ok := checkNoMistakesRun(wtPath, currentBranch); ok {
			s.NoMistakesRunStep = step
			s.applyNoMistakesStep(step, outcome)
		}
	}

	// --- TIER 1: Backlog state ---
	// Highest precedence: tasks-axi or manual backlog is the captain's source of truth.
	if backlogState, ok := readBacklogState(homeDir, id); ok {
		s.BacklogState = backlogState.String()
		switch backlogState {
		case backlog.StateDone:
			s.Status = "done"
			s.Description = "backlog: done"
			s.StatusLogSuperseded = true
			return s, nil
		case backlog.StateBlocked:
			s.Status = "blocked"
			s.Description = "backlog: blocked"
			s.StatusLogSuperseded = true
			return s, nil
		case backlog.StateQueued:
			// Queued means not yet started — report as-is when no higher work state.
			if s.Status == "unknown" {
				s.Status = "queued"
				s.Description = "backlog: queued"
			}
		case backlog.StateInFlight:
			// In-flight — continue to lower tiers for detail.
		}
	}

	// --- TIER 2: Last report (status file meta) ---
	// The last state-bearing line from the status log is the soldier's last report.
	// Terminal states (done, failed) at this tier resolve before lower tiers.
	if len(statusLines) > 0 {
		lastLine := lastStateBearingLine(statusLines)
		if lastLine != "" {
			state, msg := statusVerbAndNote(lastLine)
			switch state {
			case "done", "failed":
				s.Status = state
				s.Description = msg
				return s, nil
			case "blocked", "needs-decision", "awaiting_approval":
				s.Status = state
				s.Description = msg
				return s, nil
			case "working", "paused":
				s.Status = state
				s.Description = msg
				// Continue to lower tiers for additional evidence
			}
		}
	}

	// --- TIER 3: Provider PR state ---
	// When the task has a PR, the provider state is a strong signal.
	if prStatus, ok := readPRState(meta); ok {
		switch prStatus {
		case "MERGED":
			s.Status = "done"
			s.Description = "PR merged"
			return s, nil
		case "CLOSED":
			// PR was closed without merge — treat as failed when no other
			// higher-priority state contradicts.
			if s.Status == "unknown" {
				s.Status = "failed"
				s.Description = "PR closed without merge"
			}
		case "OPEN":
			// PR is still open — keep current state.
		}
	}

	// --- TIER 4: Typed event log ---
	// OpenActivities are evidence of keyed transitions. Already populated above.
	// If we have open activities but no state, default to working.
	if len(s.OpenActivities) > 0 && s.Status == "unknown" {
		s.Status = "working"
		s.Description = "has open activities"
	}

	// --- TIER 5: Status file fallback ---
	// Raw last state-bearing line when nothing above resolved.
	if s.Status == "unknown" && len(statusLines) > 0 {
		lastLine := lastStateBearingLine(statusLines)
		if lastLine != "" {
			state, msg := statusVerbAndNote(lastLine)
			if task.IsValidStatusState(state) && state != "resolved" {
				s.Status = state
				s.Description = msg
			}
		}
	}

	// --- Pane liveness (diagnostic only) ---
	// Terminal output is NOT truth. Pane liveness is captured for diagnostic
	// display but never used as current-state authority.
	if windowID, ok := meta["window"]; ok && windowID != "" {
		if bk, _, err := session.BackendForTask(homeDir, meta); err == nil && bk != nil {
			s.PaneAlive = bk.Alive(windowID)
		} else {
			s.PaneAlive = false
		}
	}

	return s, nil
}

// readBacklogState reads the task's state from the selected backlog authority.
func readBacklogState(homeDir, id string) (backlog.TaskState, bool) {
	item, found, err := backlog.GetItem(homeDir, id)
	if err != nil || !found {
		return backlog.StateQueued, false
	}
	return item.State, true
}

// readPRState checks the GitHub PR provider state for a task.
// Returns the PR state (MERGED/CLOSED/OPEN) and whether the check succeeded.
func readPRState(meta map[string]string) (string, bool) {
	provider := meta["pr_provider"]
	owner := meta["pr_owner"]
	repo := meta["pr_repo"]
	num := meta["pr_number"]
	if provider == "" || owner == "" || repo == "" || num == "" {
		return "", false
	}

	// Use gh CLI to query PR state.
	cmd := exec.Command("gh", "pr", "view",
		num,
		"--repo", fmt.Sprintf("%s/%s", owner, repo),
		"--json", "state",
	)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}

	var result struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return "", false
	}
	return result.State, true
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
// and must not become Status or resurrect an earlier line.
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
