package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
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
			state := args[0]
			msg := args[1]

			// Validate state
			if !domain.IsValidStatusState(state) {
				return usageError("invalid_argument",
					fmt.Sprintf("Valid states: %s", strings.Join(domain.ValidStatusStates, ", ")),
					fmt.Sprintf("Invalid status state %q", state))
			}

			// Resolve role and identity from env
			role := os.Getenv("MUNSU_ROLE")
			taskID := os.Getenv("MUNSU_TASK_ID")
			homeDir := ctx.Home

			if taskID == "" {
				return operationError("invalid_environment",
					"Run inside a munsu-managed task (MUNSU_TASK_ID must be set)",
					"MUNSU_TASK_ID is not set")
			}

			parentHome := os.Getenv("MUNSU_PARENT_STATUS")
			// A soldier's launch names its task kind; reading it from the
			// canonical record would open the dispatcher's home.
			taskKind := os.Getenv("MUNSU_TASK_KIND")

			// A review soldier's outcome is its verdict file, never a status report.
			if role == "soldier" && taskKind == taskauthority.KindReview {
				return operationError("review_task",
					"A review task reports through its verdict file, not munsu report",
					fmt.Sprintf("report: task %s is a review task", taskID))
			}

			// Determine target home based on role
			targetHome := homeDir // default: local
			switch role {
			case "captain":
				if parentHome == "" {
					return fmt.Errorf("report: MUNSU_PARENT_STATUS not set for captain role")
				}
				targetHome = parentHome
			}

			// Ship tasks cannot use "resolved" as delivery completion.
			// Only "done" is the valid terminal delivery state for ship tasks.
			if state == "resolved" {
				kind := taskKind
				if role != "soldier" {
					var err error
					if kind, err = canonicalTaskKind(targetHome, taskID); err != nil {
						return fmt.Errorf("report: cannot read task %s kind to check 'resolved': %w", taskID, err)
					}
				}
				if kind == taskauthority.KindShip {
					return fmt.Errorf("report: ship task %s cannot use 'resolved' as delivery completion; use 'done' instead", taskID)
				}
			}

			// Delivery truth is never captured or committed through the terminal
			// report path: terminal reports and retirement consume canonical
			// delivery authorization/outcome truth, and delivery execution runs
			// exclusively through the Fleet journaled Deliver operation.

			// The report is the one line. A soldier's or captain's line carries
			// the report mark, so it derives a report wake and holds the task's
			// teardown until its parent settles it; settling a scout's done
			// report completes the scout.
			if msg == "" {
				return fmt.Errorf("report: message is required")
			}
			// A captain's material report is refused from a home whose captain
			// marker, or whose parent's identity, cannot be read, as it always
			// was. Both homes are the captain's to read.
			if role == "captain" && domain.IsMaterialVerb(state) {
				if _, err := home.ValidateCaptainProvenance(homeDir); err != nil {
					return fmt.Errorf("report: deriving sender identity: %w", err)
				}
				if _, _, err := home.ReadHomeIdentity(parentHome); err != nil {
					return fmt.Errorf("report: deriving receiver identity: %w", err)
				}
			}
			if key == "" {
				key = "default"
			}
			// The key is checked where it always was: on every report but a
			// soldier's or captain's material one, a scout's done excepted. One
			// rule for every report would refuse keys this path accepts, or
			// accept keys it refuses; that change waits for the Human's words.
			material := domain.IsMaterialVerb(state) && (role == "soldier" || role == "captain")
			if !material || (role == "soldier" && state == "done" && taskKind == taskauthority.KindScout) {
				if !validReportKey(key) {
					return fmt.Errorf("report: invalid terminal key %q: must be a non-empty slug of [A-Za-z0-9_-]", key)
				}
			}
			line := domain.ReportStatusLine(state, msg, key)
			if role != "soldier" && role != "captain" {
				line = domain.ReportLineText(line)
			}
			if _, err := orchestrator.AppendReportStatus(targetHome, taskID, line); err != nil {
				return fmt.Errorf("report: %w", err)
			}

			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "report",
				Status:        "success",
				Data: MessageResult{
					Message:         fmt.Sprintf("reported %s: %s (role=%s, task=%s)", state, msg, role, taskID),
					WatcherIdentity: identifyWatcher(homeDir),
				},
			})
		}),
	}

	configureContractCommand(cmd)
	cmd.Flags().StringVar(&key, "key", "", "Optional status key/slug for correlation and idempotency")
	return cmd
}

func validReportKey(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_' || c == '-') {
			return false
		}
	}
	return s != ""
}

// canonicalTaskKind reads the task's kind from its canonical record. A task
// with no canonical record has no kind (""); an unreadable record is an error.
func canonicalTaskKind(homeDir, taskID string) (string, error) {
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return "", nil
	}
	auth, err := taskAuthorityForRead(homeDir)
	if err != nil {
		return "", err
	}
	agg, err := auth.Get(tid)
	if errors.Is(err, taskauthority.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return agg.Definition.Kind, nil
}
