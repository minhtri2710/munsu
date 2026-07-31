package cli

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
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
			id := args[0]
			desc := args[1]
			kind, _ := cmd.Flags().GetString("kind")
			repo, _ := cmd.Flags().GetString("repo")

			meta := map[string]string{
				"description": desc,
				"kind":        kind,
			}
			if repo != "" {
				meta["repo"] = repo
				meta["project"] = repo // --repo maps directly to the project name
			}

			project := meta["project"]
			agg, err := home.CreateTaskAggregate(ctx.Home, id, "", desc, kind, project)
			if err != nil {
				return err
			}
			if err := home.WriteMeta(ctx.Home, id, meta); err != nil {
				_ = home.DeleteTaskAggregate(ctx.Home, id, agg.Generation)
				return err
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

			entries, err := home.ListMeta(ctx.Home)
			if err != nil {
				return fmt.Errorf("listing tasks: %w", err)
			}

			if len(entries) == 0 {
				return writeContract(cmd, Response[EmptyResult]{
					SchemaVersion: SchemaVersion,
					Kind:          "task.list",
					Status:        "success",
					Data:          EmptyResult{Count: 0, Context: "no tasks found"},
				})
			}

			var taskEntries []TaskEntry
			for _, e := range entries {
				if stateFilter != "" && !strings.Contains(e.LastStatus, stateFilter) {
					continue
				}
				project := e.Project
				if project == "" {
					project = "-"
				}
				status := e.LastStatus
				if status == "" {
					status = "registered"
				}
				taskEntries = append(taskEntries, TaskEntry{
					ID:      e.ID,
					Kind:    e.Kind,
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
			id := args[0]
			full, _ := cmd.Flags().GetBool("full")

			resolvedID, err := home.ResolveCurrentTaskID(ctx.Home, id)
			if err != nil {
				if ambiguous, ok := err.(*home.AmbiguousTaskIDError); ok {
					return operationError("ambiguous_task_id", strings.Join(ambiguous.CorrectionCommands("munsu task show"), "; "), fmt.Sprintf("Task ID %q is ambiguous", id))
				}
				return err
			}
			id = resolvedID

			agg, hasAggregate, err := home.ReadCurrentTaskAggregate(ctx.Home, id)
			if err != nil {
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
				b.WriteString(fmt.Sprintf("owner: %s\n", agg.Owner))
				if agg.Definition != "" {
					b.WriteString(fmt.Sprintf("description: %s\n", agg.Definition))
				}
				if agg.State != "" {
					b.WriteString(fmt.Sprintf("state: %s\n", agg.State))
				}
			}
			for k, v := range meta {
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
		Short: "Append a status line to a task",
		Args:  ExactArgs(3),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]
			state := args[1]
			msg := args[2]
			line := fmt.Sprintf("%s: %s", state, msg)

			if _, _, err := home.UpdateCurrentTaskAggregateState(ctx.Home, id, state, msg); err != nil {
				return err
			}
			if err := home.AppendStatus(ctx.Home, id, line); err != nil {
				return fmt.Errorf("authoritative task state committed; status projection failed: %w", err)
			}

			// Compatibility translator: also write as typed event
			rec, _ := orchestrator.FromTaskStatus(ctx.Home, id, line)
			_ = orchestrator.AppendWithID(ctx.Home, rec.ID, rec.Type, rec.Producer, rec.Key, rec.Payload)

			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "task.status",
				Status:        "success",
				Data:          MessageResult{Message: fmt.Sprintf("status appended: %s", line)},
			})
		}),
	}
	configureContractCommand(statusCmd)

	cmd.AddCommand(addCmd)
	cmd.AddCommand(listCmd)
	cmd.AddCommand(showCmd)
	cmd.AddCommand(statusCmd)
	cmd.AddCommand(newTaskObserveCmd())
	return cmd
}
