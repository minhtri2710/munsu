package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// refuseInSoldierSeat wraps the run of a dispatcher command a soldier never
// runs: munsu session-start takes the dispatcher's session lock and munsu wake
// claim|ack|resolve settle its wake queue. A soldier reports with munsu report
// instead.
//
// It is an accident guard, not a boundary: MUNSU_ROLE is the seat's own
// environment, so it stops a soldier (or a hook in its seat) that runs these by
// mistake, early and with a clear error instead of EPERM halfway through. The
// write fence is what keeps a soldier out of those files.
//
// Human words (domain.Words): grantor the Human, channel
// supervisor-relay:dialog, words=selected, G1086: "Duyệt, có R3 (Recommended):
// option 1 + 3 PRs with section 1 conditions; R3 worded as an accident guard
// (not a boundary), error names munsu report <state>; gate record = munsu
// task-authority gates, no-mistakes gate state a separate fence task."
func refuseInSoldierSeat(command string, run func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if os.Getenv("MUNSU_ROLE") == "soldier" {
			return operationError("soldier_seat",
				"Report from a soldier seat with `munsu report <state> <msg>`",
				fmt.Sprintf("munsu %s is a dispatcher command and MUNSU_ROLE=soldier: a soldier uses munsu report <state> instead", command))
		}
		return run(cmd, args)
	}
}
