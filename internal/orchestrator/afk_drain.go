package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// FleetTaskSnapshot is the typed task reading for fleet peek.
type FleetTaskSnapshot struct {
	ID         string
	Kind       string
	LastStatus string
	Window     string
}

// FleetSnapshotProvider resolves fleet tasks for advisory peeking.
type FleetSnapshotProvider func(homeDir string) ([]FleetTaskSnapshot, error)

// DrainReport summarizes one General drain cycle: claimed signal wakes
// classified into actionable vs routine, plus a fleet peek of in-flight
// phase. It carries guidance strings so the General can steer without
// reading child chat.
type DrainReport struct {
	Consumer     string          `json:"consumer"`
	LeaseID      string          `json:"lease_id"`
	Actionable   []DrainWake     `json:"actionable"`
	RoutineCount int             `json:"routine_count"`
	Reclaimed    int             `json:"reclaimed"`
	FleetPeek    *DrainFleetPeek `json:"fleet_peek,omitempty"`
	Guidance     []string        `json:"guidance,omitempty"`
}

// DrainWake is one classified wake in the drain report.
type DrainWake struct {
	EventID string `json:"event_id"` // epoch:seq
	Key     string `json:"key"`
	Payload string `json:"payload"`
}

// DrainFleetPeek is the actionable subset of the fleet snapshot.
type DrainFleetPeek struct {
	InFlight int            `json:"in_flight"`
	Alive    int            `json:"alive"`
	Dead     int            `json:"dead"`
	Phases   []DrainTaskRow `json:"phases,omitempty"`
}

// DrainTaskRow is one in-flight task row in the peek.
type DrainTaskRow struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Phase  string `json:"phase"`
}

// HasActionable reports whether the drain cycle surfaced anything that
// needs General attention before resuming normal work. Actionable signals
// come from claimed wakes (general-relevant payloads).
func (r *DrainReport) HasActionable() bool {
	if r == nil {
		return false
	}
	return len(r.Actionable) > 0
}

// String renders the drain report for an operator: only actionable wakes,
// a compact fleet peek, and guidance lines. Routines are summarized as a
// count, not enumerated, to reduce noise.
func (r *DrainReport) String() string {
	if r == nil {
		return "AFK drain: nothing claimed\n"
	}
	var b strings.Builder
	b.WriteString("AFK drain report\n")
	b.WriteString(fmt.Sprintf("  claimed: %d actionable, %d routine (reclaimed %d)\n",
		len(r.Actionable), r.RoutineCount, r.Reclaimed))

	if len(r.Actionable) > 0 {
		b.WriteString(fmt.Sprintf("  actionable (%d):\n", len(r.Actionable)))
		for _, w := range r.Actionable {
			b.WriteString(fmt.Sprintf("    - [%s] %s: %s\n", w.EventID, w.Key, w.Payload))
		}
	}

	if r.FleetPeek != nil {
		b.WriteString(fmt.Sprintf("  fleet: %d in-flight (%d alive, %d dead)\n",
			r.FleetPeek.InFlight, r.FleetPeek.Alive, r.FleetPeek.Dead))
		for _, row := range r.FleetPeek.Phases {
			b.WriteString(fmt.Sprintf("    - %s: %s (%s)\n", row.ID, row.Status, row.Phase))
		}
	}

	if len(r.Guidance) > 0 {
		b.WriteString("  guidance:\n")
		for _, g := range r.Guidance {
			b.WriteString(fmt.Sprintf("    - %s\n", g))
		}
	}

	if r.HasActionable() {
		b.WriteString("\nActionable items remain — steer before resuming normal work.\n")
	} else {
		b.WriteString("All clear — ready to resume normal work.\n")
	}
	return b.String()
}

// DrainCycleOptions controls one drain cycle.
type DrainCycleOptions struct {
	HomeDir       string
	Consumer      string // required
	LeaseCaptains int
	Limit         int
	PeekFleet     bool // include a fleet snapshot peek
	FleetSnapshot FleetSnapshotProvider
}

// DrainCycle performs one General drain cycle:
//  1. Reclaim expired leases and claim up to Limit signal wakes under a lease.
//  2. Classify each claimed wake: general-relevant → actionable, else routine.
//  3. Optionally peek the fleet for in-flight phase counts and dead panes.
//  4. Emit guidance strings for steering.
//
// Routines are counted, not enumerated, to reduce wake rot noise.
// The caller acks actionable wakes after steering (munsu wake ack).
func DrainCycle(opts DrainCycleOptions) (*DrainReport, error) {
	if opts.Consumer == "" {
		return nil, fmt.Errorf("drain: consumer is required")
	}
	leaseCaptains := opts.LeaseCaptains
	if leaseCaptains <= 0 {
		leaseCaptains = 60
	}
	limit := opts.Limit
	if limit < 1 {
		limit = 10
	}

	claim, err := ClaimWakes(opts.HomeDir, opts.Consumer, leaseCaptains, limit)
	if err != nil {
		return nil, fmt.Errorf("claiming wakes: %w", err)
	}

	report := &DrainReport{
		Consumer:  claim.Consumer,
		LeaseID:   claim.LeaseID,
		Reclaimed: claim.Reclaimed,
	}

	for _, w := range claim.Wakes {
		eventID := w.Epoch + ":" + w.Seq
		dw := DrainWake{
			EventID: eventID,
			Key:     w.Key,
			Payload: w.Payload,
		}
		if domain.GeneralRelevant(w.Payload) {
			report.Actionable = append(report.Actionable, dw)
		} else {
			report.RoutineCount++
		}
	}

	if opts.PeekFleet {
		peek, perr := peekFleet(opts.HomeDir, opts.FleetSnapshot)
		if perr != nil {
			// Fleet peek is advisory; never fail the drain on it.
			report.Guidance = append(report.Guidance, "fleet peek failed: "+perr.Error())
		} else {
			report.FleetPeek = peek
		}
	}

	emitDrainGuidance(report)

	return report, nil
}

// peekFleet builds a compact actionable fleet peek from the snapshot.
func peekFleet(homeDir string, provider FleetSnapshotProvider) (*DrainFleetPeek, error) {
	var tasks []FleetTaskSnapshot
	if provider != nil {
		var err error
		tasks, err = provider(homeDir)
		if err != nil {
			return nil, err
		}
	} else {
		// Default fallback: scan state/*.meta
		stateDir := filepath.Join(homeDir, "state")
		entries, err := os.ReadDir(stateDir)
		if err == nil {
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".meta") && !strings.HasPrefix(e.Name(), ".") {
					id, err := home.ReverseDurableKey(strings.TrimSuffix(e.Name(), ".meta"))
					if err != nil {
						continue
					}
					meta, err := home.ReadMeta(homeDir, id)
					if err == nil {
						tasks = append(tasks, FleetTaskSnapshot{
							ID:         id,
							Kind:       meta["kind"],
							LastStatus: meta["last_status"],
							Window:     meta["window"],
						})
					}
				}
			}
		}
	}

	peek := &DrainFleetPeek{}
	for _, ts := range tasks {
		if ts.Kind != "ship" && ts.Kind != "scout" {
			continue
		}
		phase := "alive"
		if ts.Window == "" {
			phase = "no-window"
		}
		peek.InFlight++
		if phase == "alive" {
			peek.Alive++
		} else {
			peek.Dead++
		}
		status := ts.LastStatus
		if status == "" {
			status = "no status"
		}
		peek.Phases = append(peek.Phases, DrainTaskRow{
			ID:     ts.ID,
			Status: status,
			Phase:  phase,
		})
	}
	return peek, nil
}

// emitDrainGuidance appends steering hints based on actionable signals.
func emitDrainGuidance(r *DrainReport) {
	if r.FleetPeek != nil && r.FleetPeek.Dead > 0 {
		r.Guidance = append(r.Guidance,
			fmt.Sprintf("%d in-flight task(s) have dead panes — inspect or relaunch", r.FleetPeek.Dead))
	}
	if len(r.Actionable) > 0 {
		r.Guidance = append(r.Guidance,
			fmt.Sprintf("ack claimed wakes after steering: munsu wake ack %s <event-id...>", r.LeaseID))
	}
}
