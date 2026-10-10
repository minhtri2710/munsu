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
	// Values: running, fixing, ci, awaiting_approval, checks-passed, passed,
	// passed-with-skips, failed, cancelled.
	NoMistakesRunStep string

	// PR is the read-time PR of the task; see PRView.
	PR PRView

	// StatusLogSuperseded is true when the last status log line has been
	// superseded by a higher-precedence source.
	StatusLogSuperseded bool

	// OpenActivities are still-open keyed work phases from the status event log
	// (working/paused open; done/failed/resolved/etc. close). Evidence only —
	// Status is the current-state authority.
	OpenActivities []domain.Activity
}

// PRView is the PR of a task as derived at read time. It is a projection, never
// delivery truth: no authority record or meta key is written from it.
// State is PRDelivered when the no-mistakes run in the task worktree verifies
// the PR (run branch is the task branch and the worktree branch, run head is
// the worktree HEAD), PRReported when only an unverified PR URL from the status
// log is available (Reason names the failed condition), and empty otherwise.
type PRView struct {
	State  string
	URL    string
	Head   string
	Reason string
}

const (
	PRDelivered = "delivered"
	PRReported  = "reported"
)

// Line renders the view for display, or "" when there is no PR.
func (v PRView) Line() string {
	switch v.State {
	case PRDelivered:
		return "delivered: PR " + v.URL + " @ " + v.Head
	case PRReported:
		return "reported: PR " + v.URL + " (unverified: " + v.Reason + ")"
	}
	return ""
}

// ReadTaskPR derives the PRView of one task from its meta worktree and status
// log, running the no-mistakes status read once.
func ReadTaskPR(homeDir, id string) PRView {
	meta, _ := home.ReadMeta(homeDir, id)
	lines, _ := home.ReadStatus(homeDir, id)
	return readWorktree(meta["worktree"]).pr(id, lines)
}

// worktreeFacts is what one read of a task worktree observes.
type worktreeFacts struct {
	path   string
	branch string
	head   string
	run    *RunStatus
}

func readWorktree(wtPath string) worktreeFacts {
	w := worktreeFacts{path: wtPath}
	if wtPath == "" {
		return w
	}
	w.branch = getGitBranch(wtPath)
	w.head = getGitHead(wtPath)
	w.run, _ = Read(wtPath)
	return w
}

// pr is a stopgap read-time view. It is removed when teardown accepts a
// GitHub-side merge verified through gh; the uplink then records pr_* and this
// derivation is deleted.
func (w worktreeFacts) pr(id string, statusLines []string) PRView {
	var reason string
	switch {
	case w.path == "":
		reason = "no worktree"
	case w.run == nil:
		reason = "no no-mistakes run"
	case w.run.PR == "":
		reason = "run has no PR"
	case w.run.Branch != "mu/"+id:
		reason = "run branch " + w.run.Branch + " is not the task branch mu/" + id
	case w.run.Branch != w.branch:
		reason = "worktree is on " + w.branch + ", not the run branch " + w.run.Branch
	case w.run.Head != w.head:
		reason = "run head " + w.run.Head + " is not the worktree HEAD " + w.head
	default:
		return PRView{State: PRDelivered, URL: w.run.PR, Head: w.head}
	}
	for i := len(statusLines) - 1; i >= 0; i-- {
		u := strings.TrimRight(extractPRURL(statusLines[i]), ").,;")
		if strings.Contains(u, "/pull/") {
			return PRView{State: PRReported, URL: u, Reason: reason}
		}
	}
	return PRView{}
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
			return nil, fmt.Errorf("reading authoritative current state for task %q in home %s: %w", id, homeDir, tauth.ErrNotFound)
		}
		return nil, fmt.Errorf("reading authoritative current state for task %q in home %s: %w", id, homeDir, err)
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
	if len(statusLines) > 0 {
		s.StatusLines = len(statusLines)
	}
	if statusPath, err := home.StatusFilePath(homeDir, id); err == nil {
		s.OpenActivities = home.OpenActivities(statusPath)
	}

	// No-mistakes run-step is diagnostic evidence only; it never changes the
	// canonical phase.
	wt := readWorktree(meta["worktree"])
	if step, _, ok := runStep(wt.run, wt.branch); ok {
		s.NoMistakesRunStep = step
	}
	s.PR = wt.pr(id, statusLines)

	// Endpoint liveness is diagnostic only. It never changes the canonical
	// lifecycle phase: a dead/unverifiable endpoint does not turn canonical
	// working into dead/unknown.
	if windowID, ok := meta["window"]; ok && windowID != "" && probe != nil {
		alive, err := probe.Probe(homeDir, meta)
		s.PaneAlive = err == nil && alive
	}

	return s, nil
}

// runStep returns the conceptual run-step, outcome, and whether the run is
// relevant to the current branch.
func runStep(r *RunStatus, currentBranch string) (step, outcome string, ok bool) {
	if r == nil {
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

// getGitHead returns the HEAD commit sha of the worktree path.
func getGitHead(wtPath string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = wtPath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
