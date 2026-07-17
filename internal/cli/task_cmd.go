package cli

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/event"
	"github.com/minhtri2710/munsu/internal/task"
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

			if err := task.WriteMeta(ctx.Home, id, meta); err != nil {
				return err
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "task.add",
				Status:        "success",
				Data:          contract.MessageResult{Message: fmt.Sprintf("task %s added", id)},
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

			entries, err := task.ListMeta(ctx.Home)
			if err != nil {
				return fmt.Errorf("listing tasks: %w", err)
			}

			if len(entries) == 0 {
				return writeContract(cmd, contract.Response[contract.EmptyResult]{
					SchemaVersion: contract.SchemaVersion,
					Kind:          "task.list",
					Status:        "success",
					Data:          contract.EmptyResult{Count: 0, Context: "no tasks found"},
				})
			}

			var taskEntries []contract.TaskEntry
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
				taskEntries = append(taskEntries, contract.TaskEntry{
					ID:      e.ID,
					Kind:    e.Kind,
					Project: project,
					Status:  status,
				})
			}

			return writeContract(cmd, contract.Response[[]contract.TaskEntry]{
				SchemaVersion: contract.SchemaVersion,
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

			meta, err := task.ReadMeta(ctx.Home, id)
			if err != nil {
				return err
			}

			var b strings.Builder
			b.WriteString(fmt.Sprintf("Task: %s\n---\n", id))
			for k, v := range meta {
				b.WriteString(fmt.Sprintf("%s: %s\n", k, v))
			}

			if full {
				statusLines, err := task.ReadStatus(ctx.Home, id)
				if err == nil && len(statusLines) > 0 {
					b.WriteString("---\nStatus:\n")
					for _, line := range statusLines {
						b.WriteString(fmt.Sprintf("  %s\n", line))
					}
				}
			}

			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "task.show",
				Status:        "success",
				Data:          contract.MessageResult{Message: strings.TrimSpace(b.String())},
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

			if err := task.AppendStatus(ctx.Home, id, line); err != nil {
				return err
			}

			// Compatibility translator: also write as typed event
			rec, _ := event.FromTaskStatus(ctx.Home, id, line)
			_ = event.AppendWithID(ctx.Home, rec.ID, rec.Type, rec.Producer, rec.Key, rec.Payload)

			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "task.status",
				Status:        "success",
				Data:          contract.MessageResult{Message: fmt.Sprintf("status appended: %s", line)},
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
