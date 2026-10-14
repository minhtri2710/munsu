package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/spf13/cobra"
)

// newReportCmd creates the `munsu report` command: rank-aware status reporting.
func newReportCmd() *cobra.Command {
	var key string

	cmd := &cobra.Command{
		Use:   "report <state> <msg>",
		Short: "Report status up the rank hierarchy (report up, send down)",
		Long: `Report status with rank-aware routing via MUNSU_ROLE:

  soldier  -> appends a report line to the task .status in the current home (MUNSU_HOME)
  captain  -> appends a report line to General home state/<task>.status (MUNSU_PARENT_STATUS)
  general  -> local append only
A soldier's or captain's material report (done, failed, needs-decision,
blocked) is one line: the watcher or the receiver's next wake claim turns it
into one report wake, and settling that wake is the receiver's acknowledgment.

Use 'munsu send' to steer down; 'munsu report' to report up.`,
		Args: ExactArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			state, msg := args[0], args[1]
			role, taskID := os.Getenv("MUNSU_ROLE"), os.Getenv("MUNSU_TASK_ID")
			replay, err := orchestrator.SubmitReport(orchestrator.ReportRequest{
				Home: ctx.Home, ParentHome: os.Getenv("MUNSU_PARENT_STATUS"),
				Role: role, TaskID: taskID, TaskKind: os.Getenv("MUNSU_TASK_KIND"),
				State: state, Message: msg, Key: key,
			})
			var refusal *orchestrator.ReportRefusal
			if errors.As(err, &refusal) {
				if refusal.Usage {
					return usageError(refusal.Code, refusal.Help, refusal.Detail)
				}
				return operationError(refusal.Code, refusal.Help, refusal.Detail)
			}
			if err != nil {
				return err
			}
			message := fmt.Sprintf("reported %s: %s (role=%s, task=%s)", state, msg, role, taskID)
			if replay {
				message = fmt.Sprintf("not appended: %s: %s is already the last status line of task %s (idempotent)", state, msg, taskID)
			}

			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "report",
				Status:        "success",
				Data: MessageResult{
					Message:         message,
					Noop:            replay,
					WatcherIdentity: identifyWatcher(ctx.Home),
				},
			})
		}),
	}

	configureContractCommand(cmd)
	cmd.Flags().StringVar(&key, "key", "", "Optional status key/slug for correlation and idempotency")
	return cmd
}
