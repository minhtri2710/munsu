package fleet

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/backlog"
	"github.com/minhtri2710/munsu/internal/classify"
	"github.com/minhtri2710/munsu/internal/task"
)

// HomeSummary is a bounded structured view of one Captain home.
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

// ChildBrief is one active endpoint under a Captain home.
type ChildBrief struct {
	ID     string
	Status string
	Kind   string
}

// HomeCounts aggregates Captain-home workload.
type HomeCounts struct {
	ActiveChildren int
	Queued         int
	InFlight       int
	Blocked        int
	Done           int
	Endpoints      int
}

const maxActiveChildren = 20

// SummarizeCaptainHome builds a bounded summary for a registered Captain home.
func SummarizeCaptainHome(homeDir string) HomeSummary {
	now := time.Now().UTC().Format(time.RFC3339)
	sum := HomeSummary{
		Schema:    "munsu-captain-home-summary.v1",
		Generated: now,
		Home:      homeDir,
		Valid:     true,
		State:     "no_active_work",
	}
	if homeDir == "" {
		sum.Valid = false
		sum.Reason = "no recorded captain home"
		sum.State = "unknown"
		return sum
	}

	fb := backlog.NewFileBackend(filepath.Join(homeDir, "data", "backlog.md"))
	if items, err := fb.List(backlog.StateQueued); err == nil {
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

	entries, err := task.ListMeta(homeDir)
	if err != nil {
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
			if classify.GeneralRelevant(status) || verb == "needs-decision" {
				captainDecision = true
			}
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
	default:
		sum.State = "no_active_work"
	}
	return sum
}

// LastParentStatus returns the last line of parent state/captain:<id>.status.
func LastParentStatus(parentHome, captainID string) string {
	lines, err := task.ReadStatus(parentHome, "captain:"+captainID)
	if err != nil || len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[len(lines)-1])
}
