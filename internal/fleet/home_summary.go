package fleet

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/backlog"
	"github.com/minhtri2710/munsu/internal/classify"
	"github.com/minhtri2710/munsu/internal/task"
)

// HomeSummary is a bounded structured view of one Second home.
// Schema mirrors firstmate's fm-secondmate-home-summary.v1 at a shallow depth:
// registered home state is authoritative; parent status is separate evidence.
type HomeSummary struct {
	Schema         string
	Generated      string
	Home           string
	Valid          bool
	Reason         string
	State          string // no_active_work | active_child_work | captain_decision | externally_held | unknown
	ActiveChildren []ChildBrief
	Counts         HomeCounts
}

// ChildBrief is one active endpoint under a Second home.
type ChildBrief struct {
	ID     string
	Status string
	Kind   string
}

// HomeCounts aggregates Second-home workload.
type HomeCounts struct {
	ActiveChildren int
	Queued         int
	InFlight       int
	Blocked        int
	Done           int
	Endpoints      int
}

const maxActiveChildren = 20

// SummarizeSecondHome builds a bounded summary for a registered Second home.
func SummarizeSecondHome(homeDir string) HomeSummary {
	now := time.Now().UTC().Format(time.RFC3339)
	sum := HomeSummary{
		Schema:    "munsu-second-home-summary.v1",
		Generated: now,
		Home:      homeDir,
		Valid:     true,
		State:     "no_active_work",
	}
	if homeDir == "" {
		sum.Valid = false
		sum.Reason = "no recorded second home"
		sum.State = "unknown"
		return sum
	}

	// Backlog counts (manual file backend; empty if missing).
	fb := backlog.NewFileBackend(filepath.Join(homeDir, "data", "backlog.md"))
	if items, err := fb.List(backlog.StateQueued); err == nil {
		// List(StateQueued) returns all items by backend convention.
		for _, item := range items {
			switch item.State {
			case backlog.StateQueued:
				sum.Counts.Queued++
			case backlog.StateInFlight:
				sum.Counts.InFlight++
			case backlog.StateBlocked:
				sum.Counts.Blocked++
			case backlog.StateDone:
				sum.Counts.Done++
			}
		}
	}

	// Endpoint / child task scan from Second home state/*.meta
	entries, err := task.ListMeta(homeDir)
	if err != nil {
		// non-fatal: home may only have backlog
		entries = nil
	}
	sum.Counts.Endpoints = len(entries)

	var active []ChildBrief
	captainDecision := false
	for _, e := range entries {
		status := strings.TrimSpace(e.LastStatus)
		verb := status
		if i := strings.Index(status, ":"); i >= 0 {
			verb = strings.TrimSpace(status[:i])
		}
		switch verb {
		case "working", "parked", "paused", "blocked", "needs-decision":
			if len(active) < maxActiveChildren {
				active = append(active, ChildBrief{ID: e.ID, Status: status, Kind: e.Kind})
			}
			sum.Counts.ActiveChildren++
			if classify.CaptainRelevant(status) || verb == "needs-decision" {
				captainDecision = true
			}
		case "done", "failed":
			// terminal endpoints stay out of active_children
		}
	}
	sum.ActiveChildren = active

	switch {
	case captainDecision:
		sum.State = "captain_decision"
	case sum.Counts.ActiveChildren > 0 || sum.Counts.InFlight > 0:
		sum.State = "active_child_work"
	case sum.Counts.Blocked > 0:
		sum.State = "externally_held"
	case sum.Counts.Queued > 0:
		sum.State = "no_active_work" // idle with queue still healthy resting? firstmate uses queued separately
	default:
		sum.State = "no_active_work"
	}
	return sum
}

// LastParentStatus returns the last line of parent state/secondmate:<id>.status.
func LastParentStatus(parentHome, secondID string) string {
	lines, err := task.ReadStatus(parentHome, "secondmate:"+secondID)
	if err != nil || len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[len(lines)-1])
}
