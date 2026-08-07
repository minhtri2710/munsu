package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/spf13/cobra"
)

func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Manage task lifecycle",
	}

	addCmd := &cobra.Command{
		Use:   "add <id> <description>",
		Short: "Add a new task",
		Args:  ExactArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
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
			tid, err := domain.NewTaskID(id)
			if err != nil {
				return err
			}
			req := taskauthority.CanonicalCreateRequest{
				HomeID:      auth.HomeID(),
				TaskID:      tid,
				Owner:       resolveTaskOwner(ctx.Home),
				Description: desc,
				Kind:        kind,
				Project:     domain.ProjectID{},
				Reason:      "cli task add",
			}
			if project != "" {
				pid, err := domain.NewProjectID(project)
				if err != nil {
					return err
				}
				req.Project = pid
			}
			op, err := newCanonicalOperation("task-add", req)
			if err != nil {
				return err
			}
			if _, err := auth.Create(op, req); err != nil {
				return err
			}
			// .meta and .status are post-commit projections (ADR-0007 §7):
			// the authoritative fields are derived from the canonical
			// aggregate; a projection failure must not roll back the
			// authoritative Task Generation.
			agg, err := auth.Get(tid)
			if err != nil {
				return &LifecyclePartialError{TaskID: id, State: "queued", Cause: err}
			}
			runtimeFields := map[string]string{}
			if repo != "" {
				runtimeFields["repo"] = repo
			}
			if perr := projectTaskMeta(ctx.Home, agg, runtimeFields); perr != nil {
				return &LifecyclePartialError{TaskID: id, State: "queued", Cause: perr}
			}
			if perr := home.AppendStatus(ctx.Home, id, "queued: cli task add"); perr != nil {
				return &LifecyclePartialError{TaskID: id, State: "queued", Cause: perr}
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
			tid, err := domain.NewTaskID(id)
			if err != nil {
				return err
			}
			agg, err := auth.Get(tid)
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
rank (munsu task start|done|block|unblock|reopen).`,
		Args: ExactArgs(3),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
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

	cmd.AddCommand(addCmd)
	cmd.AddCommand(listCmd)
	cmd.AddCommand(showCmd)
	cmd.AddCommand(statusCmd)
	cmd.AddCommand(newTaskStartCmd())
	cmd.AddCommand(newTaskDoneCmd())
	cmd.AddCommand(newTaskBlockCmd())
	cmd.AddCommand(newTaskUnblockCmd())
	cmd.AddCommand(newTaskReopenCmd())
	cmd.AddCommand(newTaskRetryCmd())
	cmd.AddCommand(newTaskObserveCmd())
	return cmd
}

func newTaskStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <id>",
		Short: "Start a task (mark in-flight)",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			// Supervision gate: start fails closed when the watcher lease is
			// degraded, before any Task Authority call (Task 4.3, ADR-0007 §8).
			if err := fleet.CheckSupervisionForDispatch(ctx.Home, home.DispatchActionStart); err != nil {
				return err
			}
			return runTaskLifecycleTransition(ctx, "start", "working", args, func(auth *taskauthority.Canonical, tid domain.TaskID, agg taskauthority.Aggregate) error {
				req := taskauthority.CanonicalStartRequest{
					HomeID:       auth.HomeID(),
					TaskID:       tid,
					Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
					Reason:       "task: start",
				}
				op, err := newCanonicalOperation("task-start", req)
				if err != nil {
					return err
				}
				_, err = auth.Start(op, req)
				return err
			})
		}),
	}
}

func newTaskDoneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a task as done",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return runTaskLifecycleTransition(ctx, "done", "done", args, func(auth *taskauthority.Canonical, tid domain.TaskID, agg taskauthority.Aggregate) error {
				req := taskauthority.CanonicalCompleteRequest{
					HomeID:       auth.HomeID(),
					TaskID:       tid,
					Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
					To:           taskauthority.PhaseDone,
					Reason:       "task: done",
				}
				op, err := newCanonicalOperation("task-done", req)
				if err != nil {
					return err
				}
				_, err = auth.Complete(op, req)
				return err
			})
		}),
	}
}

func newTaskBlockCmd() *cobra.Command {
	var by string
	cmd := &cobra.Command{
		Use:   "block <id> [--by <dependency-id>]",
		Short: "Block a task",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			detail := "task: blocked"
			if by != "" {
				detail += " by " + by
			}
			return runTaskLifecycleTransition(ctx, "block", "blocked", args, func(auth *taskauthority.Canonical, tid domain.TaskID, agg taskauthority.Aggregate) error {
				req := taskauthority.CanonicalBlockRequest{
					HomeID:       auth.HomeID(),
					TaskID:       tid,
					Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
					Detail:       detail,
					Reason:       "task: block",
				}
				op, err := newCanonicalOperation("task-block", req)
				if err != nil {
					return err
				}
				_, err = auth.Block(op, req)
				return err
			})
		}),
	}
	cmd.Flags().StringVar(&by, "by", "", "Dependency that blocks this task")
	return cmd
}

func newTaskUnblockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unblock <id>",
		Short: "Unblock a blocked task",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return runTaskLifecycleTransition(ctx, "unblock", "queued", args, func(auth *taskauthority.Canonical, tid domain.TaskID, agg taskauthority.Aggregate) error {
				req := taskauthority.CanonicalUnblockRequest{
					HomeID:       auth.HomeID(),
					TaskID:       tid,
					Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
					Reason:       "task: unblock",
				}
				op, err := newCanonicalOperation("task-unblock", req)
				if err != nil {
					return err
				}
				_, err = auth.Unblock(op, req)
				return err
			})
		}),
	}
}

func newTaskReopenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reopen <id>",
		Short: "Reopen a terminal task as a new generation",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return runTaskLifecycleTransition(ctx, "reopen", "queued", args, func(auth *taskauthority.Canonical, tid domain.TaskID, agg taskauthority.Aggregate) error {
				req := taskauthority.CanonicalReopenRequest{
					HomeID:       auth.HomeID(),
					TaskID:       tid,
					Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
					Reason:       "task: reopen",
				}
				op, err := newCanonicalOperation("task-reopen", req)
				if err != nil {
					return err
				}
				_, err = auth.Reopen(op, req)
				return err
			})
		}),
	}
}

func newTaskRetryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retry <id>",
		Short: "Supersede a failed/terminal generation as a new queued generation",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return runTaskLifecycleTransition(ctx, "retry", "queued", args, func(auth *taskauthority.Canonical, tid domain.TaskID, agg taskauthority.Aggregate) error {
				// Retry is the canonical Reopen operation: a terminal generation
				// is superseded as a fresh queued Generation at Revision one and
				// the prior generation is preserved as historical state. Live
				// generations are refused so a retry never claims running work.
				req := taskauthority.CanonicalReopenRequest{
					HomeID:       auth.HomeID(),
					TaskID:       tid,
					Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
					Reason:       "task: retry",
				}
				op, err := newCanonicalOperation("task-retry", req)
				if err != nil {
					return err
				}
				_, err = auth.Reopen(op, req)
				return err
			})
		}),
	}
}

// runTaskLifecycleTransition runs one authoritative Task Authority lifecycle
// transition and appends the .status post-commit projection (ADR-0007 §7): a
// projection failure must not roll back the authoritative transition; the
// projection can be retried independently and the authoritative operation is
// never replayed.
func runTaskLifecycleTransition(ctx Ctx, verb, projectionState string, args []string, op func(auth *taskauthority.Canonical, tid domain.TaskID, agg taskauthority.Aggregate) error) error {
	taskID := args[0]
	auth, err := ctx.TaskAuthority()
	if err != nil {
		return err
	}
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return err
	}
	agg, err := auth.Get(tid)
	if err != nil {
		return err
	}
	if err := op(auth, tid, agg); err != nil {
		return err
	}
	if perr := home.AppendStatus(ctx.Home, taskID, projectionState+": cli task "+verb); perr != nil {
		return &LifecyclePartialError{TaskID: taskID, State: projectionState, Cause: perr}
	}
	return nil
}

// projectTaskMeta overlays the canonical aggregate fields onto the task .meta
// projection, preserving runtime-only projection fields. Empty canonical
// values remove stale keys. It is the post-commit projection write (ADR-0007
// §7): the authoritative Task Generation is never written here.
func projectTaskMeta(homeDir string, agg taskauthority.Aggregate, runtime map[string]string) error {
	existing, err := home.ReadMeta(homeDir, agg.TaskID)
	if err != nil {
		existing = map[string]string{}
	}
	derived := make(map[string]string, len(existing)+len(runtime)+6)
	for k, v := range existing {
		derived[k] = v
	}
	for k, v := range runtime {
		derived[k] = v
	}
	put := func(k, v string) {
		if v == "" {
			delete(derived, k)
			return
		}
		derived[k] = v
	}
	put("owner", agg.Definition.Owner)
	put("description", agg.Definition.Description)
	put("kind", agg.Definition.Kind)
	put("project", agg.Definition.Project)
	put("generation", agg.Generation.String())
	put("state", string(agg.Phase))
	return home.WriteMeta(homeDir, agg.TaskID, derived)
}
