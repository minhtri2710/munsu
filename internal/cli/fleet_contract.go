package cli

import (
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/decisionhold"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/second"
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
	crews := make([]contract.Crew, 0, len(snapshot.Tasks))
	for _, entry := range snapshot.Tasks {
		status := entry.LastStatus
		if index := strings.Index(status, ":"); index >= 0 {
			status = strings.TrimSpace(status[:index])
		}
		if status == "" {
			status = fleet.PhaseFromMeta(entry.Window, entry.PaneAlive)
		}
		row := contract.Crew{TaskID: entry.ID, Status: status}
		if fields["branch"] {
			row.Branch = branchFor(map[string]string{"worktree": entry.Worktree})
		}
		crews = append(crews, row)
	}
	// Collect second entries
	matedata, err := second.List(ctx.Home)
	var seconds []contract.SecondEntry
	if err == nil {
		for _, m := range matedata {
			status := fleet.SecondStatus(m.Home)
			seconds = append(seconds, contract.SecondEntry{
				ID:     m.ID,
				Scope:  m.Scope,
				Status: status,
			})
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

	sort.Slice(crews, func(i, j int) bool { return crews[i].TaskID < crews[j].TaskID })
	return writeContract(cmd, contract.Response[contract.FleetSnapshotV2]{
		SchemaVersion: contract.SchemaVersion,
		Kind:          "fleet.snapshot",
		Status:        "success",
		Data: contract.FleetSnapshotV2{
			Scope:           ctx.Home,
			Count:           len(crews),
			Total:           len(crews),
			Crews:           crews,
			Seconds:         seconds,
			UnresolvedHolds: unresolvedHolds,
		},
		Help: []string{"Run `munsu task observe <task-id>` to inspect a crew"},
	})
}
