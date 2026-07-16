package cli

import (
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/fleet"
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
	sort.Slice(crewmates, func(i, j int) bool { return crewmates[i].TaskID < crewmates[j].TaskID })
	return writeContract(cmd, contract.Response[contract.FleetSnapshotV2]{
		SchemaVersion: contract.SchemaVersion,
		Kind:          "fleet.snapshot",
		Status:        "success",
		Data: contract.FleetSnapshotV2{
			Scope:     ctx.Home,
			Count:     len(crewmates),
			Total:     len(crewmates),
			Crewmates: crewmates,
		},
		Help: []string{"Run `munsu task observe <task-id>` to inspect a crewmate"},
	})
}
