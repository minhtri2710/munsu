package cli

import (
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/captain"
	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/decisionhold"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/spf13/cobra"
)

func init() {
	fleet.SetPaneAliveProbe(func(parentHome string, meta map[string]string) (bool, error) {
		bk, _, err := session.BackendForTask(parentHome, meta)
		if err != nil {
			return false, err
		}
		return bk.Alive(meta["window"]), nil
	})
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
	snapshot, err := fleet.Snapshot(ctx.Home)
	if err != nil {
		return operationError("internal", "Run `munsu fleet snapshot --version 2` again", "Unable to read fleet state")
	}
	soldiers := make([]contract.Soldier, 0, len(snapshot.Tasks))
	for _, entry := range snapshot.Tasks {
		status := entry.LastStatus
		if index := strings.Index(status, ":"); index >= 0 {
			status = strings.TrimSpace(status[:index])
		}
		if status == "" {
			status = fleet.PhaseFromMeta(entry.Window, entry.PaneAlive)
		}
		row := contract.Soldier{TaskID: entry.ID, Status: status}
		if fields["branch"] {
			row.Branch = branchFor(map[string]string{"worktree": entry.Worktree})
		}
		soldiers = append(soldiers, row)
	}
	// Collect captain entries with home-summary + parent return-channel status.
	matedata, err := captain.List(ctx.Home)
	var captains []contract.CaptainEntry
	if err == nil {
		for _, m := range matedata {
			status := fleet.CaptainStatus(ctx.Home, m.ID, m.Home)
			entry := contract.CaptainEntry{
				ID:               m.ID,
				Home:             m.Home,
				Scope:            m.Scope,
				Status:           status,
				LastParentStatus: fleet.LastParentStatus(ctx.Home, m.ID),
			}
			sum := fleet.SummarizeCaptainHome(m.Home)
			if sum.Valid {
				entry.CurrentState = sum.State
				entry.CurrentReason = sum.Reason
				entry.Provenance = "structured-home"
				entry.Counts = &contract.CaptainHomeCounts{
					ActiveChildren: sum.Counts.ActiveChildren,
					Queued:         sum.Counts.Queued,
					InFlight:       sum.Counts.InFlight,
					Blocked:        sum.Counts.Blocked,
					Done:           sum.Counts.Done,
					Endpoints:      sum.Counts.Endpoints,
				}
				for _, c := range sum.ActiveChildren {
					entry.ActiveChildren = append(entry.ActiveChildren, contract.CaptainChildBrief{
						ID: c.ID, Status: c.Status, Kind: c.Kind,
					})
				}
			} else if entry.LastParentStatus != "" {
				entry.Provenance = "parent-status-only"
				entry.CurrentState = "unknown"
				entry.CurrentReason = sum.Reason
			} else {
				entry.Provenance = "unavailable"
				entry.CurrentState = "unknown"
				entry.CurrentReason = sum.Reason
			}
			captains = append(captains, entry)
		}
	}

	// Count unresolved holds across all tasks
	unresolvedHolds := 0
	for _, entry := range snapshot.Tasks {
		holds, err := decisionhold.ListUnresolved(ctx.Home, entry.ID)
		if err == nil {
			unresolvedHolds += len(holds)
		}
	}

	sort.Slice(soldiers, func(i, j int) bool { return soldiers[i].TaskID < soldiers[j].TaskID })
	return writeContract(cmd, contract.Response[contract.FleetSnapshotV2]{
		SchemaVersion: contract.SchemaVersion,
		Kind:          "fleet.snapshot",
		Status:        "success",
		Data: contract.FleetSnapshotV2{
			Scope:           ctx.Home,
			Count:           len(soldiers),
			Total:           len(soldiers),
			Soldiers:        soldiers,
			Captains:        captains,
			UnresolvedHolds: unresolvedHolds,
		},
		Help: []string{"Run `munsu task observe <task-id>` to inspect a soldier"},
	})
}
