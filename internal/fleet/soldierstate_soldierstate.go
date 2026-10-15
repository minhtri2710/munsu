// The canonical Task Authority record is the only lifecycle authority (clean
// break): `Status`/`Description` derive solely from the authoritative
// `taskauthority.Aggregate.Phase`/`PhaseDetail`. `.meta`, `.status`, provider
// PR and endpoint evidence are diagnostic/evidence only and can
// never change lifecycle state without a canonical record. A missing or
// corrupt canonical record is an operation error, never a projection fallback.

package fleet

import (
	"errors"
	"fmt"
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
// delivery truth: no authority record or meta key is written from it. URL is
// the last PR URL the status log reports, unverified, or empty.
type PRView struct {
	URL string
}

// Line renders the view for display, or "" when there is no PR.
func (v PRView) Line() string {
	if v.URL == "" {
		return ""
	}
	return "reported: PR " + v.URL + " (unverified)"
}

// ReadTaskPR derives the PRView of one task from its status log.
func ReadTaskPR(homeDir, id string) PRView {
	lines, _ := home.ReadStatus(homeDir, id)
	return reportedPR(lines)
}

// reportedPR is a stopgap read-time view. It is removed when teardown accepts
// a GitHub-side merge verified through gh; the uplink then records pr_* and
// this derivation is deleted.
func reportedPR(statusLines []string) PRView {
	for i := len(statusLines) - 1; i >= 0; i-- {
		u := strings.TrimRight(extractPRURL(statusLines[i]), ").,;")
		if strings.Contains(u, "/pull/") {
			return PRView{URL: u}
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

	// .meta provides operational/display data (diagnostic only). The endpoint
	// identity is the canonical one, bound or only acquired: a launch whose
	// handshake failed holds a live pane but wrote no .meta.
	meta, _ := home.ReadMeta(homeDir, id)
	if meta == nil {
		meta = map[string]string{}
	}
	overlayEndpointMeta(meta, agg.Endpoint, agg.AcquiredEndpoint)
	if agg.Launch != nil {
		meta["harness"] = agg.Launch.Harness
	}

	// .status contributes diagnostic evidence only (never lifecycle state).
	statusLines, _ := home.ReadStatus(homeDir, id)
	if len(statusLines) > 0 {
		s.StatusLines = len(statusLines)
	}
	if statusPath, err := home.StatusFilePath(homeDir, id); err == nil {
		s.OpenActivities = home.OpenActivities(statusPath)
	}

	s.PR = reportedPR(statusLines)

	// Endpoint liveness is diagnostic only. It never changes the canonical
	// lifecycle phase: a dead/unverifiable endpoint does not turn canonical
	// working into dead/unknown.
	if windowID, ok := meta["window"]; ok && windowID != "" && probe != nil {
		alive, err := probe.Probe(homeDir, meta)
		s.PaneAlive = err == nil && alive
	}

	return s, nil
}
