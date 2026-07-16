package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/crewstate"
	"github.com/minhtri2710/munsu/internal/project"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/waker"
	"github.com/minhtri2710/munsu/internal/worktree"
	"github.com/spf13/cobra"
)

func newCapabilitiesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capabilities",
		Short: "Show agent-facing orchestration capabilities",
		Args:  contractNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := contractOutput(cmd); err != nil {
				return err
			}
			return writeContract(cmd, contract.Response[contract.Capabilities]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "capabilities",
				Status:        "success",
				Data: contract.Capabilities{
					ContractVersion: contract.SchemaVersion,
					Commands: []string{
						"capabilities", "task observe", "fleet snapshot --version 2", "guard", "watch ensure",
						"watch run", "wake claim", "wake ack", "backend capabilities", "spawn",
					},
					OutputFormats: []string{contract.OutputTOON, contract.OutputJSON},
				},
				Help: []string{"Run `munsu task observe <task-id>` to inspect a task"},
			})
		},
	}
	configureContractCommand(cmd)
	return cmd
}

func newBackendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backend",
		Short: "Inspect session backend support",
	}
	capabilitiesCmd := &cobra.Command{
		Use:   "capabilities",
		Short: "Show supported operations for one session backend",
		Args:  contractNoArgs,
		RunE: withHome(func(cmd *cobra.Command, _ []string, ctx Ctx) error {
			backend, err := cmd.Flags().GetString("backend")
			if err != nil {
				return usageError("invalid_argument", "Run `munsu backend capabilities --help`", "Unable to read --backend")
			}
			if backend != "" && backend != "tmux" && backend != "herdr" {
				return usageError("unsupported_input", "Run `munsu backend capabilities --backend tmux` or `munsu backend capabilities --backend herdr`", fmt.Sprintf("Unsupported backend %q", backend))
			}
			if _, err := contractOutput(cmd); err != nil {
				return err
			}
			if backend == "" {
				_, backend, err = session.Resolve(ctx.Home, "")
				if err != nil {
					return operationError("dependency_unavailable", "Configure a supported session backend and rerun `munsu backend capabilities`", "No supported session backend is available")
				}
			}
			return writeContract(cmd, contract.Response[contract.BackendCapabilities]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "backend.capabilities",
				Status:        "success",
				Data: contract.BackendCapabilities{
					Backend:  backend,
					Features: []string{"create_session", "send_input", "pane_liveness"},
				},
			})
		}),
	}
	configureContractCommand(capabilitiesCmd)
	capabilitiesCmd.Flags().String("backend", "", "Backend name (tmux|herdr)")
	cmd.AddCommand(capabilitiesCmd)
	return cmd
}

func newTaskObserveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "observe <task-id>",
		Short: "Observe one task using the orchestration contract",
		Args:  contractArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			fields, err := contractFields(cmd, []string{"description", "branch", "pane_alive", "no_mistakes_step"})
			if err != nil {
				return err
			}
			full, _ := cmd.Flags().GetBool("full")
			if full {
				return usageError("unsupported_input", "Run `munsu task observe "+args[0]+"`", "--full is unavailable because task observation has no truncated fields")
			}
			if _, err := contractOutput(cmd); err != nil {
				return err
			}
			if _, err := task.ReadMeta(ctx.Home, args[0]); err != nil {
				return operationError("not_found", "Run `munsu task list` to find a task ID", fmt.Sprintf("Task %q was not found", args[0]))
			}
			state, err := crewstate.Read(ctx.Home, args[0])
			if err != nil {
				return operationError("internal", "Run `munsu task observe "+args[0]+"` again", "Unable to observe task state")
			}
			meta, _ := task.ReadMeta(ctx.Home, args[0])
			result := contract.TaskObserve{TaskID: state.TaskID, Status: state.Status}
			if fields["description"] {
				result.Description = state.Description
			}
			if fields["branch"] {
				result.Branch = branchFor(meta)
			}
			if fields["pane_alive"] {
				result.PaneAlive = &state.PaneAlive
			}
			if fields["no_mistakes_step"] {
				result.NoMistakesStep = state.NoMistakesRunStep
			}
			help := []string{"Run `munsu task observe " + args[0] + " --fields description,branch` for expanded fields"}
			return writeContract(cmd, contract.Response[contract.TaskObserve]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "task.observe",
				Status:        "success",
				Data:          result,
				Help:          help,
			})
		}),
	}
	configureContractCommand(cmd)
	cmd.Flags().String("fields", "", "Optional fields: description,branch,pane_alive,no_mistakes_step")
	cmd.Flags().Bool("full", false, "Include full truncated content when available")
	return cmd
}

func newContractGuardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guard",
		Short: "Report fleet guard conditions for agents",
		Args:  contractNoArgs,
		RunE: withHome(func(cmd *cobra.Command, _ []string, ctx Ctx) error {
			if _, err := contractOutput(cmd); err != nil {
				return err
			}
			conditions := waker.GuardWarnings(ctx.Home)
			projects, err := project.List(ctx.Home)
			if err == nil {
				for _, entry := range projects {
					projectDir, resolveErr := project.ResolveRepoPath(ctx.Home, entry.Name)
					if resolveErr != nil {
						continue
					}
					if guardErr := worktree.AssertNotTangled(projectDir, entry.Name); guardErr != nil {
						conditions = append(conditions, guardErr.Error())
					}
				}
			}
			state := "clear"
			if len(conditions) > 0 {
				state = "warning"
			} else {
				conditions = []string{"no blocked work", "watcher healthy"}
			}
			return writeContract(cmd, contract.Response[contract.Guard]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "guard",
				Status:        "success",
				Data:          contract.Guard{State: state, Conditions: conditions},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func contractFields(cmd *cobra.Command, allowed []string) (map[string]bool, error) {
	requested, err := cmd.Flags().GetString("fields")
	if err != nil {
		return nil, usageError("invalid_argument", fmt.Sprintf("Run `%s --help`", commandPath(cmd)), "Unable to read --fields")
	}
	fields := make(map[string]bool)
	if requested == "" {
		return fields, nil
	}
	for _, field := range strings.Split(requested, ",") {
		field = strings.TrimSpace(field)
		if field == "" || !contains(allowed, field) {
			return nil, usageError("unsupported_input", fmt.Sprintf("Run `%s --help`", commandPath(cmd)), fmt.Sprintf("Unsupported field %q", field))
		}
		fields[field] = true
	}
	return fields, nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func branchFor(meta map[string]string) string {
	if branch := meta["branch"]; branch != "" {
		return branch
	}
	worktreePath := meta["worktree"]
	if worktreePath == "" {
		return ""
	}
	if strings.HasSuffix(worktreePath, string(filepath.Separator)) {
		worktreePath = strings.TrimSuffix(worktreePath, string(filepath.Separator))
	}
	return filepath.Base(worktreePath)
}
