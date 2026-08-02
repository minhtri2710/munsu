package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/taskauthorityfs"
	"github.com/spf13/cobra"
)

func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Manage task lifecycle",
	}

	addCmd := &cobra.Command{
		Use:   "add <id> <description>",
		Short: "Add a new task to the backlog",
		Args:  ExactArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if result := fleet.CheckOperation(fleet.OpTaskMutation, ctx.Home); !result.IsCompatible() {
				return fmt.Errorf("task mutation compatibility check failed: %s", result.FormatErrors())
			}
			id := args[0]
			desc := args[1]
			kind, _ := cmd.Flags().GetString("kind")
			repo, _ := cmd.Flags().GetString("repo")

			project := ""
			if repo != "" {
				project = repo // --repo maps directly to the project name
			}

			auth, err := ctx.TaskAuthority()
			if err != nil {
				return err
			}
			owner, actor := resolveTaskActor(ctx.Home)
			if _, err := auth.Create(taskauthority.CreateRequest{
				OperationID: newTaskAuthorityOperationID("task-add"),
				Actor:       actor,
				TaskID:      id,
				Owner:       owner,
				Description: desc,
				Kind:        kind,
				Project:     project,
				Reason:      "cli task add",
			}); err != nil {
				return err
			}
			// .meta is a post-commit projection (ADR-0007 §7): the authoritative
			// fields are derived from the canonical aggregate by the projection
			// layer and the runtime-only repo field is preserved. The direct
			// home.WriteMeta reach-through is gone (Task 7.8); a projection
			// failure must not roll back the authoritative Task Generation.
			store, err := taskauthorityfs.NewStore(ctx.Home)
			if err != nil {
				return err
			}
			runtimeFields := map[string]string{}
			if repo != "" {
				runtimeFields["repo"] = repo
			}
			if _, err := store.ProjectTaskAdd(id, runtimeFields); err != nil {
				return &LifecyclePartialError{TaskID: id, State: "queued", Cause: err}
			}
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "task.add",
				Status:        "success",
				Data:          MessageResult{Message: fmt.Sprintf("task %s added", id)},
			})
		}),
	}
	configureContractCommand(addCmd)
	addCmd.Flags().String("kind", "ship", "Task kind (ship|scout)")
	addCmd.Flags().String("repo", "", "Project repository name")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		Args:  NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			stateFilter, _ := cmd.Flags().GetString("state")

			auth, err := ctx.TaskAuthority()
			if err != nil {
				return err
			}
			aggregates, err := auth.List()
			if err != nil {
				return fmt.Errorf("listing tasks: %w", err)
			}

			if len(aggregates) == 0 {
				return writeContract(cmd, Response[EmptyResult]{
					SchemaVersion: SchemaVersion,
					Kind:          "task.list",
					Status:        "success",
					Data:          EmptyResult{Count: 0, Context: "no tasks found"},
				})
			}

			var taskEntries []TaskEntry
			for _, agg := range aggregates {
				status := string(agg.Phase)
				if stateFilter != "" && !strings.Contains(status, stateFilter) {
					continue
				}
				project := agg.Definition.Project
				if project == "" {
					project = "-"
				}
				taskEntries = append(taskEntries, TaskEntry{
					ID:      agg.TaskID,
					Kind:    agg.Definition.Kind,
					Project: project,
					Status:  status,
				})
			}

			return writeContract(cmd, Response[[]TaskEntry]{
				SchemaVersion: SchemaVersion,
				Kind:          "task.list",
				Status:        "success",
				Data:          taskEntries,
				Help:          []string{fmt.Sprintf("Total: %d task(s)", len(taskEntries))},
			})
		}),
	}
	configureContractCommand(listCmd)
	listCmd.Flags().String("state", "", "Filter by state (in-flight|queued|done)")

	showCmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show task details",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if err := fleet.RecoverTaskHandoffs(ctx.Home); err != nil {
				return err
			}
			id := args[0]
			full, _ := cmd.Flags().GetBool("full")

			resolvedID, err := resolveCurrentTaskID(ctx.Home, id)
			if err != nil {
				if ambiguous, ok := err.(*home.AmbiguousTaskIDError); ok {
					return operationError("ambiguous_task_id", strings.Join(ambiguous.CorrectionCommands("munsu task show"), "; "), fmt.Sprintf("Task ID %q is ambiguous", id))
				}
				return err
			}
			id = resolvedID

			auth, err := ctx.TaskAuthority()
			if err != nil {
				return err
			}
			agg, err := auth.Get(id)
			hasAggregate := err == nil
			if err != nil && !errors.Is(err, taskauthority.ErrNotFound) {
				return err
			}
			meta, metaErr := home.ReadMeta(ctx.Home, id)
			if metaErr != nil && !hasAggregate {
				return metaErr
			}

			var b strings.Builder
			b.WriteString(fmt.Sprintf("Task: %s\n---\n", id))
			if hasAggregate {
				b.WriteString(fmt.Sprintf("generation: %s\n", agg.Generation))
				b.WriteString(fmt.Sprintf("owner: %s\n", agg.Definition.Owner))
				if agg.Definition.Description != "" {
					b.WriteString(fmt.Sprintf("description: %s\n", agg.Definition.Description))
				}
				if agg.Definition.Kind != "" {
					b.WriteString(fmt.Sprintf("kind: %s\n", agg.Definition.Kind))
				}
				if agg.Definition.Project != "" {
					b.WriteString(fmt.Sprintf("project: %s\n", agg.Definition.Project))
				}
				b.WriteString(fmt.Sprintf("state: %s\n", agg.Phase))
			}
			for k, v := range meta {
				if authoritativeMetaField(k) {
					continue
				}
				b.WriteString(fmt.Sprintf("%s: %s\n", k, v))
			}

			if full {
				statusLines, err := home.ReadStatus(ctx.Home, id)
				if err == nil && len(statusLines) > 0 {
					b.WriteString("---\nStatus:\n")
					for _, line := range statusLines {
						b.WriteString(fmt.Sprintf("  %s\n", line))
					}
				}
			}

			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "task.show",
				Status:        "success",
				Data:          MessageResult{Message: strings.TrimSpace(b.String())},
			})
		}),
	}
	configureContractCommand(showCmd)
	showCmd.Flags().Bool("full", false, "Show full details including status")

	statusCmd := &cobra.Command{
		Use:   "status <id> <state> <message>",
		Short: "Append an audit-only status line to a task",
		Long: `Append a status line to the task .status projection and typed event
log. This is audit input only: it never changes the authoritative task
phase. Authoritative transitions are named operations owned by the parent
rank (munsu backlog start|done|block|unblock|reopen).`,
		Args: ExactArgs(3),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if result := fleet.CheckOperation(fleet.OpTaskMutation, ctx.Home); !result.IsCompatible() {
				return fmt.Errorf("task mutation compatibility check failed: %s", result.FormatErrors())
			}
			id := args[0]
			state := args[1]
			msg := args[2]
			line := fmt.Sprintf("%s: %s", state, msg)

			// Audit-only: appending a status line never mutates the authoritative
			// Task Aggregate phase (Task 3.4). Transitions are named operations.
			if err := home.AppendStatus(ctx.Home, id, line); err != nil {
				return fmt.Errorf("appending status line: %w", err)
			}

			// Compatibility translator: also write as typed event
			rec, _ := orchestrator.FromTaskStatus(ctx.Home, id, line)
			_ = orchestrator.AppendWithID(ctx.Home, rec.ID, rec.Type, rec.Producer, rec.Key, rec.Payload)

			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "task.status",
				Status:        "success",
				Data:          MessageResult{Message: fmt.Sprintf("status appended (audit-only; phase unchanged): %s", line)},
			})
		}),
	}
	configureContractCommand(statusCmd)

	reconcileCmd := &cobra.Command{
		Use:   "reconcile [id]",
		Short: "Reconcile .meta and .status projections from canonical Task Authority records",
		Long: `Reconcile the .meta and .status projections from canonical Task
Authority records. .meta authoritative fields are rewritten from the current
Task Generation (runtime-only projection fields are preserved); .status lines
are derived from the typed audit history and appended when missing.

Reconciliation is one-directional: it never changes the authoritative task
revision or generation, is idempotent, and reports a typed partial outcome
when a projection cannot be repaired. Without an id it reconciles every
current task.`,
		Args: MaximumNArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			store, err := taskauthorityfs.NewStore(ctx.Home)
			if err != nil {
				return err
			}
			var outcomes []taskauthorityfs.TaskProjection
			if len(args) == 1 {
				out, err := store.ReconcileTaskProjections(args[0])
				if err != nil {
					return err
				}
				outcomes = []taskauthorityfs.TaskProjection{out}
			} else {
				outcomes, err = store.ReconcileProjections()
				if err != nil {
					return err
				}
			}
			rows := make([]TaskProjectionRow, 0, len(outcomes))
			var failed []taskauthorityfs.TaskProjection
			for _, out := range outcomes {
				rows = append(rows, TaskProjectionRow{
					TaskID:     out.TaskID,
					Generation: out.Generation.String(),
					Revision:   uint64(out.Revision),
					Meta:       string(out.Meta),
					Status:     string(out.Status),
				})
				if out.Err != "" {
					failed = append(failed, out)
				}
			}
			if len(failed) > 0 {
				return &ProjectionPartialError{Failed: failed}
			}
			return writeContract(cmd, Response[[]TaskProjectionRow]{
				SchemaVersion: SchemaVersion,
				Kind:          "task.reconcile",
				Status:        "success",
				Data:          rows,
				Help:          []string{fmt.Sprintf("Reconciled %d task(s); projection reconciliation never changes authoritative revision or generation", len(rows))},
			})
		}),
	}
	configureContractCommand(reconcileCmd)

	cmd.AddCommand(addCmd)
	cmd.AddCommand(listCmd)
	cmd.AddCommand(showCmd)
	cmd.AddCommand(statusCmd)
	cmd.AddCommand(reconcileCmd)
	cmd.AddCommand(newTaskObserveCmd())
	return cmd
}
