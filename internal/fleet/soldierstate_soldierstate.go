// Package soldierstate reads and reports the current state of a soldier.
//
// The canonical Task Authority record is the only lifecycle authority (clean
// break): `Status`/`Description` derive solely from the authoritative
// `taskauthority.Aggregate.Phase`/`PhaseDetail`. `.meta`, `.status`, provider
// PR, no-mistakes and endpoint evidence are diagnostic/evidence only and can
// never change lifecycle state without a canonical record. A missing or
// corrupt canonical record is an operation error, never a projection fallback.
package fleet

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	tauth "github.com/minhtri2710/munsu/internal/taskauthority"
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
	OpenActivities []domain.Activity
}

type StateEndpointProbe interface {
	Probe(homeDir string, meta map[string]string) (bool, error)
}

func ReadWithProbe(homeDir string, id string, probe StateEndpointProbe) (*State, error) {
	s := &State{TaskID: id, Status: "unknown"}

	// Canonical Task Authority is the only lifecycle authority (clean break,
	// Task 7.8). A missing or corrupt canonical record is an operation error
	// with task/home context, never a .meta/.status projection fallback.
	agg, err := currentCanonical(homeDir, id)
	if err != nil {
		if errors.Is(err, tauth.ErrNotFound) {
			return nil, fmt.Errorf("reading authoritative current state for task %q in home %q: %w", id, homeDir, tauth.ErrNotFound)
		}
		return nil, fmt.Errorf("reading authoritative current state for task %q in home %q: %w", id, homeDir, err)
	}

	// Canonical phase is state truth.
	s.Status = string(agg.Phase)
	if s.Status == "" {
		s.Status = "unknown"
	}
	s.Description = agg.PhaseDetail
	if s.Description == "" {
		s.Description = agg.Definition.Description
	}
	// The canonical record is the top-precedence source: the status log is
	// always superseded display, never state truth.
	s.StatusLogSuperseded = true

	// .meta provides operational/display data (diagnostic only).
	meta, _ := home.ReadMeta(homeDir, id)

	// .status contributes diagnostic evidence only (never lifecycle state).
	statusLines, _ := home.ReadStatus(homeDir, id)
	statusPath := filepath.Join(home.StateDir(homeDir), id+".status")
	if len(statusLines) > 0 {
		s.StatusLines = len(statusLines)
	}
	s.OpenActivities = home.OpenActivities(statusPath)

	// No-mistakes run-step is diagnostic evidence only; it never changes the
	// canonical phase.
	if wtPath := meta["worktree"]; wtPath != "" {
		currentBranch := getGitBranch(wtPath)
		if step, _, ok := checkNoMistakesRun(wtPath, currentBranch); ok {
			s.NoMistakesRunStep = step
		}
	}

	// Endpoint liveness is diagnostic only. It never changes the canonical
	// lifecycle phase: a dead/unverifiable endpoint does not turn canonical
	// working into dead/unknown.
	if windowID, ok := meta["window"]; ok && windowID != "" && probe != nil {
		alive, err := probe.Probe(homeDir, meta)
		s.PaneAlive = err == nil && alive
	}

	return s, nil
}

// checkNoMistakesRun reads no-mistakes run status from the worktree path, using
// the structured nostatus package at the CLI boundary, and returns the conceptual
// run-step, outcome, and whether the info is relevant.
func checkNoMistakesRun(wtPath, currentBranch string) (step, outcome string, ok bool) {
	r, err := Read(wtPath)
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
