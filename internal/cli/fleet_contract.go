package cli

import (
	"fmt"
	"sort"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/spf13/cobra"
)

func runtimeTaskEndpointProbe() orchestrator.TaskEndpointProbe {
	return cliEndpointProbe{resolve: backend.BackendForTask}
}

type cliEndpointProbe struct {
	resolve func(string, map[string]string) (backend.Backend, string, error)
}

func (p cliEndpointProbe) Probe(home string, meta map[string]string) (bool, error) {
	ownerHome := meta["home"]
	if ownerHome == "" {
		ownerHome = home
	}
	bk, _, err := p.resolve(ownerHome, meta)
	if err != nil {
		return false, err
	}
	result, err := probeCaptainBackend(bk, meta["window"])
	if err != nil {
		return false, err
	}
	return result.PaneAlive && result.AgentAlive, nil
}

func (p cliEndpointProbe) ProbeEndpoint(endpoint fleet.EndpointRef) (fleet.EndpointStatus, error) {
	incomplete := fleet.EndpointStatus{
		Lifecycle:      fleet.LifecycleUnknown,
		Responsiveness: fleet.ResponsivenessUnknown,
		Freshness:      fleet.FreshnessUnknown,
		Activity:       fleet.ActivityUnknown,
		Source:         fleet.SourceDerived,
		Detail:         "bound endpoint identity is incomplete",
	}
	if endpoint.Backend == "" || endpoint.Handle == "" || endpoint.Home == "" {
		return incomplete, fmt.Errorf("bound endpoint identity is incomplete")
	}
	switch endpoint.Backend {
	case "tmux", "herdr", "zellij", "cmux", "orca":
	default:
		return fleet.EndpointStatus{
			Lifecycle:      fleet.LifecycleUnknown,
			Responsiveness: fleet.ResponsivenessUnknown,
			Freshness:      fleet.FreshnessUnknown,
			Activity:       fleet.ActivityUnknown,
			Source:         fleet.SourceDerived,
			Detail:         fmt.Sprintf("unsupported bound backend %q", endpoint.Backend),
		}, fmt.Errorf("unsupported bound backend %q", endpoint.Backend)
	}
	if endpoint.Backend == "herdr" && endpoint.SessionOwner != "" {
		handleSession, _ := backend.ParseWindow(endpoint.Handle)
		if handleSession != "" && handleSession != endpoint.SessionOwner {
			return fleet.EndpointStatus{
				Lifecycle:      fleet.LifecycleUnknown,
				Responsiveness: fleet.ResponsivenessUnknown,
				Freshness:      fleet.FreshnessStale,
				Activity:       fleet.ActivityUnknown,
				Source:         fleet.SourceDerived,
				Detail:         "herdr session ownership mismatch",
			}, nil
		}
	}
	meta := map[string]string{
		"backend":            endpoint.Backend,
		"window":             endpoint.Handle,
		"herdr_session":      endpoint.SessionOwner,
		"herdr_workspace_id": endpoint.WorkspaceID,
		"herdr_tab_id":       endpoint.TabID,
		"home":               endpoint.Home,
	}
	resolve := p.resolve
	if resolve == nil {
		resolve = backend.BackendForTask
	}
	bk, resolved, err := resolve(endpoint.Home, meta)
	if err != nil {
		return fleet.EndpointStatus{
			Lifecycle:      fleet.LifecycleUnknown,
			Responsiveness: fleet.ResponsivenessUnknown,
			Freshness:      fleet.FreshnessUnknown,
			Activity:       fleet.ActivityUnknown,
			Source:         fleet.SourceDerived,
			Detail:         err.Error(),
		}, nil
	}
	if resolved != endpoint.Backend {
		return fleet.EndpointStatus{
			Lifecycle:      fleet.LifecycleUnknown,
			Responsiveness: fleet.ResponsivenessUnknown,
			Freshness:      fleet.FreshnessStale,
			Activity:       fleet.ActivityUnknown,
			Source:         fleet.SourceDerived,
			Detail:         fmt.Sprintf("bound backend resolved as %q", resolved),
		}, nil
	}
	// Produce the raw typed observation of the exact bound endpoint handle.
	// Freshness is concluded by Fleet's authorizeObservation against the exact
	// canonical binding; the CLI never fabricates incarnation/freshness.
	return backend.ObserveEndpoint(bk, endpoint.Handle), nil
}

// snapshotDeps builds the explicit read dependencies for fleet snapshot/guard
// callers: the canonical current-state query is always present, and the
// endpoint probe (diagnostic only) is the CLI adapter over the session backend.
func snapshotDeps() fleet.SnapshotDependencies {
	return fleet.SnapshotDependencies{
		CurrentState: fleet.NewCanonicalCurrentState(),
		Endpoint:     cliEndpointProbe{resolve: backend.BackendForTask},
	}
}

func runFleetSnapshotV2(cmd *cobra.Command, ctx Ctx) error {
	version, _ := cmd.Flags().GetInt("version")
	if version != 2 {
		return usageError("unsupported_input", "Run `munsu fleet snapshot --version 2`", "Only fleet snapshot version 2 is supported by this command")
	}
	fields, err := contractFields(cmd, []string{"branch"})
	if err != nil {
		return err
	}
	full, _ := cmd.Flags().GetBool("full")
	if full {
		return usageError("unsupported_input", "Run `munsu fleet snapshot --version 2`", "--full is unavailable because fleet snapshot rows have no truncated fields")
	}
	if _, err := contractOutput(cmd); err != nil {
		return err
	}
	snapshot, err := fleet.Snapshot(ctx.Home, snapshotDeps())
	if err != nil {
		return operationError("internal", "Run `munsu fleet snapshot --version 2` again", "Unable to read fleet state")
	}
	soldiers := make([]Soldier, 0, len(snapshot.Tasks))
	for _, entry := range snapshot.Tasks {
		if entry.Kind == "captain" {
			continue
		}
		// The contract row reports the resolved current-state projection, not
		// the append-only .status tail: the canonical Task Authority phase (or
		// the current-state resolver output) wins and a stale status verb can
		// never override a newer authoritative lifecycle transition.
		status := fleet.PhaseFromProjection(entry)
		if status == "" {
			status = fleet.PhaseFromMeta(entry.Window, entry.PaneAlive)
		}
		row := Soldier{TaskID: entry.ID, Status: status}
		if fields["branch"] {
			row.Branch = branchFor(map[string]string{"worktree": entry.Worktree})
		}
		soldiers = append(soldiers, row)
	}
	// Collect captain entries with home-summary + parent return-channel status.
	matedata, err := fleet.ListCaptains(ctx.Home)
	var captains []CaptainEntry
	if err == nil {
		for _, m := range matedata {
			status := fleet.CaptainStatus(ctx.Home, m.ID, m.Home, cliEndpointProbe{resolve: backend.BackendForTask})
			entry := CaptainEntry{
				ID:               m.ID,
				Home:             m.Home,
				Scope:            m.Scope,
				Status:           status,
				LastParentStatus: fleet.LastParentStatus(ctx.Home, m.ID),
			}
			sum := fleet.SummarizeCaptainHome(m.Home)
			valid := sum.Valid
			entry.Valid = &valid
			rec := fleet.ReconcileParentStatus(sum, entry.LastParentStatus)
			entry.Provenance = rec.Provenance
			entry.Freshness = rec.Freshness
			entry.ParentEventRole = rec.ParentEventRole
			entry.Contradiction = rec.Contradiction
			entry.ContradictionReason = rec.ContradictionReason
			if sum.Valid || sum.Home != "" {
				entry.CurrentState = sum.State
				entry.CurrentReason = sum.Reason
				// Preserve main's fail-closed provenance nuance when home is readable
				// but invalid and no parent status exists.
				if !sum.Valid && entry.LastParentStatus == "" && sum.Reason != "" {
					entry.Provenance = "structured-home"
					entry.Freshness = "fresh"
					entry.ParentEventRole = "historical-only"
				}
				entry.Counts = &CaptainHomeCounts{
					ActiveChildren: sum.Counts.ActiveChildren,
					DecisionsOpen:  sum.Counts.DecisionsOpen,
					Holds:          sum.Counts.Holds,
					Queued:         sum.Counts.Queued,
					Landed:         sum.Counts.Landed,
					Endpoints:      sum.Counts.Endpoints,
					InFlight:       sum.Counts.InFlight,
					Blocked:        sum.Counts.Blocked,
					Done:           sum.Counts.Done,
				}
				for _, c := range sum.ActiveChildren {
					entry.ActiveChildren = append(entry.ActiveChildren, CaptainChildBrief{
						ID: c.ID, Status: c.Status, Kind: c.Kind, Doing: c.Doing,
					})
				}
				for _, d := range sum.DecisionsOpen {
					entry.DecisionsOpen = append(entry.DecisionsOpen, CaptainDecision{
						ID: d.ID, Key: d.Key, Verb: d.Verb, Summary: d.Summary, Reason: d.Reason, Source: d.Source,
					})
				}
				for _, h := range sum.Holds {
					entry.Holds = append(entry.Holds, CaptainHold{
						ID: h.ID, Title: h.Title, BlockedBy: h.BlockedBy, Reason: h.Reason, Source: h.Source,
					})
				}
				for _, q := range sum.Queued {
					entry.Queued = append(entry.Queued, CaptainQueued{
						ID: q.ID, Title: q.Title, Repo: q.Repo, Kind: q.Kind,
					})
				}
				for _, l := range sum.Landed {
					entry.Landed = append(entry.Landed, CaptainLanded{
						ID: l.ID, Title: l.Title, PRURL: l.PRURL,
					})
				}
				for _, o := range sum.Omitted {
					entry.Omitted = append(entry.Omitted, CaptainOmitted{
						Surface: o.Surface, Count: o.Count,
					})
				}
			} else {
				entry.CurrentState = "unknown"
				entry.CurrentReason = sum.Reason
			}
			captains = append(captains, entry)
		}
	}

	// Count unresolved holds across all tasks. The durable hold truth lives in
	// the Authority Store (Task 5.2); on a legacy v1 home the Authority fails
	// closed with migration-required, which the snapshot reports as no holds
	// rather than failing the whole read.
	unresolvedHolds := 0
	if auth, err := ctx.TaskAuthority(); err == nil {
		holds, err := auth.ListHolds()
		if err == nil {
			for _, entry := range snapshot.Tasks {
				for _, hold := range holds {
					if hold.ReleasedAt == 0 && holdsScopedToTask(hold, entry.ID) {
						unresolvedHolds++
					}
				}
			}
		}
	}

	sort.Slice(soldiers, func(i, j int) bool { return soldiers[i].TaskID < soldiers[j].TaskID })
	return writeContract(cmd, Response[FleetSnapshotV2]{
		SchemaVersion: SchemaVersion,
		Kind:          "fleet.snapshot",
		Status:        "success",
		Data: FleetSnapshotV2{
			Scope:           ctx.Home,
			Count:           len(soldiers),
			Total:           len(soldiers),
			Soldiers:        soldiers,
			Captains:        captains,
			CaptainGuidance: DefaultCaptainGuidance(),
			UnresolvedHolds: unresolvedHolds,
		},
		Help: []string{"Run `munsu task observe <task-id>` to inspect a soldier"},
	})
}
