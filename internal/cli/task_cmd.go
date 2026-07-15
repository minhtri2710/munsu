package cli

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/home"
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
		RunE: func(cmd *cobra.Command, args []string) error {
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

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			if err := task.WriteMeta(homeDir, id, meta); err != nil {
				return err
			}
			fmt.Printf("task %s added\n", id)
			return nil
		},
	}
	addCmd.Flags().String("kind", "ship", "Task kind (ship|scout)")
	addCmd.Flags().String("repo", "", "Project repository name")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		Args:  NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stateFilter, _ := cmd.Flags().GetString("state")

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			entries, err := task.ListMeta(homeDir)
			if err != nil {
				return fmt.Errorf("listing tasks: %w", err)
			}

			if len(entries) == 0 {
				fmt.Println("no tasks found")
				return nil
			}

			fmt.Printf("%-20s %-8s %-16s %s\n", "ID", "KIND", "PROJECT", "STATUS")
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
				fmt.Printf("%-20s %-8s %-16s %s\n", e.ID, e.Kind, project, status)
			}
			return nil
		},
	}
	listCmd.Flags().String("state", "", "Filter by state (in-flight|queued|done)")

	showCmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show task details",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			full, _ := cmd.Flags().GetBool("full")

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			meta, err := task.ReadMeta(homeDir, id)
			if err != nil {
				return err
			}

			fmt.Printf("Task: %s\n", id)
			fmt.Printf("---\n")
			for k, v := range meta {
				fmt.Printf("%s: %s\n", k, v)
			}

			if full {
				statusLines, err := task.ReadStatus(homeDir, id)
				if err == nil && len(statusLines) > 0 {
					fmt.Printf("---\nStatus:\n")
					for _, line := range statusLines {
						fmt.Printf("  %s\n", line)
					}
				}
			}
			return nil
		},
	}
	showCmd.Flags().Bool("full", false, "Show full details including status")

	statusCmd := &cobra.Command{
		Use:   "status <id> <state> <message>",
		Short: "Append a status line to a task",
		Args:  ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			state := args[1]
			msg := args[2]
			line := fmt.Sprintf("%s: %s", state, msg)

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			if err := task.AppendStatus(homeDir, id, line); err != nil {
				return err
			}
			fmt.Printf("status appended: %s\n", line)
			return nil
		},
	}

	cmd.AddCommand(addCmd)
	cmd.AddCommand(listCmd)
	cmd.AddCommand(showCmd)
	cmd.AddCommand(statusCmd)
	return cmd
}
