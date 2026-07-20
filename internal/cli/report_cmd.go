package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/event"
	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/spf13/cobra"
)

// materialStates are the status states that warrant waking a parent supervisor.
var materialStates = map[string]bool{
	"done":           true,
	"failed":         true,
	"needs-decision": true,
	"blocked":        true,
}

// newReportCmd creates the `munsu report` command for rank-aware uplink status reporting.
func newReportCmd() *cobra.Command {
	var key string
	var ring string // "auto" | "ring" | "no-ring"

	cmd := &cobra.Command{
		Use:   "report <state> <msg>",
		Short: "Report status up the rank hierarchy (report up, send down)",
		Long: `Report status with rank-aware routing via MUNSU_ROLE:

  soldier  -> appends to task .status in the current home (MUNSU_HOME)
  captain  -> appends to General home state/captain:<id>.status (MUNSU_PARENT_STATUS)
  general  -> local append only (same as soldier)

Also writes to the typed event log and enqueues a wake for material states
(done, failed, needs-decision, blocked).

Use 'munsu send' for downlink steering; 'munsu report' for uplink status.`,
		Args: ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			state := args[0]
			msg := args[1]

			// Validate state
			if !task.IsValidStatusState(state) {
				return usageError("invalid_argument",
					fmt.Sprintf("Valid states: %s", strings.Join(task.ValidStatusStates, ", ")),
					fmt.Sprintf("Invalid status state %q", state))
			}

			// Resolve role and identity from env
			role := os.Getenv("MUNSU_ROLE")
			taskID := os.Getenv("MUNSU_TASK_ID")
			homeDir := os.Getenv("MUNSU_HOME")

			if homeDir == "" {
				return operationError("invalid_environment",
					"Run inside a munsu-managed task (MUNSU_HOME must be set)",
					"MUNSU_HOME is not set")
			}
			if taskID == "" {
				return operationError("invalid_environment",
					"Run inside a munsu-managed task (MUNSU_TASK_ID must be set)",
					"MUNSU_TASK_ID is not set")
			}

			parentHome := os.Getenv("MUNSU_PARENT_STATUS")
			statusLine := state + ": " + msg
			if key != "" {
				statusLine += " [key=" + key + "]"
			}

			// Determine target home and event/wake home based on role
			targetHome := homeDir // default: local
			eventHome := homeDir
			wakeHome := homeDir

			switch role {
			case "captain":
				if parentHome == "" {
					return fmt.Errorf("report: MUNSU_PARENT_STATUS not set for captain role")
				}
				targetHome = parentHome
				eventHome = parentHome
				wakeHome = parentHome
			}

			// 1. Durable task status append
			if err := task.AppendStatus(targetHome, taskID, statusLine); err != nil {
				return fmt.Errorf("report: appending status: %w", err)
			}

			// 2. Append to typed event log using synthetic event ID
			syntheticID := event.SyntheticEventID()
			if err := event.AppendWithID(eventHome, syntheticID, "task.status", taskID, key, statusLine); err != nil {
				fmt.Fprintf(os.Stderr, "warning: report: event append: %v\n", err)
			}

			// 3. For material states, enqueue a wake in the supervisor's queue
			if materialStates[state] {
				wakePayload := fmt.Sprintf("%s: %s [event=%d]", taskID, statusLine, syntheticID)
				lifecycle.EnqueueWake(wakeHome, "signal", taskID, wakePayload)
			}

			// 4. Ring policy: flag for optional pane injection
			// When ring=ring or ring=auto (and not AFK-batching), a future
			// enhancement will inject to the parent pane when composer is empty.
			// Currently the wake queue signals the parent supervisor.
			ringDecision := resolveRingPolicy(ring, homeDir)
			if ringDecision == "ring" && materialStates[state] {
				// Ring-worthy material event produced — parent supervisor
				// will handle pane injection via wake queue.
				_ = ringDecision
			}

			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "report",
				Status:        "success",
				Data:          contract.MessageResult{Message: fmt.Sprintf("reported %s: %s (role=%s, task=%s)", state, msg, role, taskID)},
			})
		},
	}

	configureContractCommand(cmd)
	cmd.Flags().StringVar(&key, "key", "", "Optional status key/slug for correlation and idempotency")
	cmd.Flags().StringVar(&ring, "ring", "auto", "Ring policy: auto, ring, no-ring")
	return cmd
}

// newNotifyCmd creates the `munsu notify` alias for `munsu report`.
func newNotifyCmd() *cobra.Command {
	reportCmd := newReportCmd()
	notifyCmd := &cobra.Command{
		Use:   "notify <state> <msg>",
		Short: "Alias for 'munsu report'",
		Long:  `'munsu notify' is an alias for 'munsu report'. See 'munsu report --help'.`,
		Args:  ExactArgs(2),
		RunE:  reportCmd.RunE,
	}
	notifyCmd.Flags().AddFlagSet(reportCmd.Flags())
	return notifyCmd
}

// resolveRingPolicy returns the effective ring decision.
// auto: ring unless AFK flag is present.
func resolveRingPolicy(ring, homeDir string) string {
	switch ring {
	case "ring":
		return "ring"
	case "no-ring":
		return "no-ring"
	default: // auto
		afkPath := filepath.Join(homeDir, "state", ".afk")
		if _, err := os.Stat(afkPath); err == nil {
			return "no-ring"
		}
		return "ring"
	}
}
