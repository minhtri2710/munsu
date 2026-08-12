package cli

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

// This file is the read-only A-01 seam for the root "fleet summary" output.
// It separates the query of runtime state (fleet snapshot + watcher beat)
// from its presentation so the default CLI path stays a thin adapter. The
// rendered text is byte-identical to the previous inline implementation and
// is locked by golden tests in root_summary_test.go.

// rootSummaryTask is a presentation-only view of one task in the summary.
type rootSummaryTask struct {
	id      string
	phase   string
	project string
	status  string
}

// rootSummaryView is the presentation model rendered by renderRootSummary.
type rootSummaryView struct {
	homeDir    string
	totalTasks int
	inFlight   int
	watcher    string
	tasks      []rootSummaryTask
}

// loadRootSummary queries the fleet snapshot and watcher beat status for the
// given home and maps them into a presentation model. Unreadable Task truth
// fails closed (returns an error) rather than rendering an empty fleet.
func loadRootSummary(homeDir string) (rootSummaryView, error) {
	v := rootSummaryView{homeDir: homeDir, watcher: "--"}

	snap, err := fleet.Snapshot(homeDir, snapshotDeps())
	if err != nil {
		// A home that is simply not initialized has no fleet to summarize; this
		// is absence, not unreadable Task truth.
		if errors.Is(err, home.ErrNotInitialized) {
			return v, nil
		}
		return v, fmt.Errorf("reading authoritative fleet state: %w", err)
	}
	v.totalTasks = len(snap.Tasks)
	for _, ts := range snap.Tasks {
		if ts.Kind == "ship" || ts.Kind == "scout" {
			v.inFlight++
		}
		task := rootSummaryTask{
			id:      ts.ID,
			phase:   fleet.PhaseFromProjection(ts),
			project: ts.Project,
			status:  ts.CurrentState,
		}
		if task.project == "" {
			task.project = "-"
		}
		if task.status == "" {
			task.status = task.phase
		}
		v.tasks = append(v.tasks, task)
	}

	beat := orchestrator.ReadBeatStatus(homeDir, time.Now())
	if beat.Exists {
		if beat.Stale {
			v.watcher = "stale"
		} else {
			v.watcher = "alive"
		}
	}
	return v, nil
}

// renderRootSummary prints the compact fleet/orientation snapshot.
func renderRootSummary(w io.Writer, v rootSummaryView) {
	fmt.Fprintf(w, "munsu @ %s\n\n", v.homeDir)
	fmt.Fprintf(w, "fleet: %d tasks (%d in-flight) | watcher: %s | holds: --\n", v.totalTasks, v.inFlight, v.watcher)

	if len(v.tasks) > 0 {
		fmt.Fprintln(w)
		for _, ts := range v.tasks {
			fmt.Fprintf(w, "  %-20s [%-10s] %s\n", ts.id, ts.phase, ts.project)
			if ts.status != "" && ts.status != ts.phase {
				fmt.Fprintf(w, "  %-20s  %s\n", "", ts.status)
			}
		}
	} else {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "No tasks. Start with `munsu task add <id> \"<description>\"`.")
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Next: munsu fleet bearings | munsu peek <id> | munsu --help")
}
