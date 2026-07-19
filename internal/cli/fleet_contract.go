package cli

import (
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/decisionhold"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/secondmate"
	"github.com/spf13/cobra"
)

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
	crewmates := make([]contract.Crewmate, 0, len(snapshot.Tasks))
	for _, entry := range snapshot.Tasks {
		status := entry.LastStatus
		if index := strings.Index(status, ":"); index >= 0 {
			status = strings.TrimSpace(status[:index])
		}
		if status == "" {
			status = fleet.PhaseFromMeta(entry.Window, entry.PaneAlive)
		}
		row := contract.Crewmate{TaskID: entry.ID, Status: status}
		if fields["branch"] {
			row.Branch = branchFor(map[string]string{"worktree": entry.Worktree})
		}
		crewmates = append(crewmates, row)
	}
	// Collect secondmate entries with home-summary + parent return-channel status.
	matedata, err := secondmate.List(ctx.Home)
	var secondmates []contract.SecondmateEntry
	if err == nil {
		for _, m := range matedata {
			status := fleet.SecondmateStatus(m.Home)
			entry := contract.SecondmateEntry{
				ID:               m.ID,
				Home:             m.Home,
				Scope:            m.Scope,
				Status:           status,
				LastParentStatus: fleet.LastParentStatus(ctx.Home, m.ID),
			}
			sum := fleet.SummarizeSecondHome(m.Home)
			if sum.Valid {
				entry.CurrentState = sum.State
				entry.CurrentReason = sum.Reason
				entry.Provenance = "structured-home"
				entry.Counts = &contract.SecondHomeCounts{
					ActiveChildren: sum.Counts.ActiveChildren,
					Queued:         sum.Counts.Queued,
					InFlight:       sum.Counts.InFlight,
					Blocked:        sum.Counts.Blocked,
					Done:           sum.Counts.Done,
					Endpoints:      sum.Counts.Endpoints,
				}
				for _, c := range sum.ActiveChildren {
					entry.ActiveChildren = append(entry.ActiveChildren, contract.SecondChildBrief{
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
			secondmates = append(secondmates, entry)
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

	sort.Slice(crewmates, func(i, j int) bool { return crewmates[i].TaskID < crewmates[j].TaskID })
	return writeContract(cmd, contract.Response[contract.FleetSnapshotV2]{
		SchemaVersion: contract.SchemaVersion,
		Kind:          "fleet.snapshot",
		Status:        "success",
		Data: contract.FleetSnapshotV2{
			Scope:           ctx.Home,
			Count:           len(crewmates),
			Total:           len(crewmates),
			Crewmates:       crewmates,
			Secondmates:     secondmates,
			UnresolvedHolds: unresolvedHolds,
		},
		Help: []string{"Run `munsu task observe <task-id>` to inspect a crewmate"},
	})
}
