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
	Status      string // one of: working, done, failed, paused, blocked, needs-decision, resolved, idle, unknown
	Description string // human-readable detail
	PaneAlive   bool   // whether the session pane is still alive
	StatusLines int    // number of status log lines
}

// Read reads and synthesizes the current crewmate state.
func Read(homeDir string, id string) (*State, error) {
	s := &State{TaskID: id, Status: "unknown"}

	// 1. Read meta
	meta, err := task.ReadMeta(homeDir, id)
	if err != nil {
		return nil, fmt.Errorf("reading meta for %s: %w", id, err)
	}

	// 2. Check pane liveness
	if windowID, ok := meta["window"]; ok && windowID != "" {
		bk := session.Default()
		s.PaneAlive = bk.Alive(windowID)
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

	// 3. Read status log for the latest state
	statusLines, err := task.ReadStatus(homeDir, id)
	if err == nil && len(statusLines) > 0 {
		s.StatusLines = len(statusLines)
		lastLine := statusLines[len(statusLines)-1]

		// Extract state from last status line: "state: message"
		if colonIdx := strings.Index(lastLine, ":"); colonIdx >= 0 {
			state := strings.TrimSpace(lastLine[:colonIdx])
			if state == "done" || state == "failed" {
				// Terminal states override pane check
				s.Status = state
				s.Description = strings.TrimSpace(lastLine[colonIdx+1:])
			} else if s.Status == "unknown" || s.Status == "working" || s.Status == "idle" {
				// Non-terminal states only override if we don't have a better source
				if task.IsValidStatusState(state) {
					s.Status = state
					s.Description = strings.TrimSpace(lastLine[colonIdx+1:])
				}
			}
		}
	}

	// 4. If no status log and pane is alive, check git branch for context
	if s.StatusLines == 0 && s.PaneAlive {
		if wtPath, ok := meta["worktree"]; ok && wtPath != "" {
			branchLine := getGitBranch(wtPath)
			if branchLine != "" {
				s.Description = fmt.Sprintf("on branch %s", branchLine)
			}
		}
	}

	return s, nil
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
