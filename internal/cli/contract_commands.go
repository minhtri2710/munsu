package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
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
			return writeContract(cmd, Response[Capabilities]{
				SchemaVersion: SchemaVersion,
				Kind:          "capabilities",
				Status:        "success",
				Data: Capabilities{
					ContractVersion: SchemaVersion,
					Commands: []string{
						"capabilities", "task observe", "fleet snapshot --version 2", "guard", "watch ensure",
						"watch run", "wake claim", "wake ack", "wake drain", "event append", "backend capabilities", "spawn",
						"integrate install", "integrate repair", "integrate status", "afk drain",
					},
					OutputFormats: []string{OutputTOON, OutputJSON},
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
			backendName, err := cmd.Flags().GetString("backend")
			if err != nil {
				return usageError("invalid_argument", "Run `munsu backend capabilities --help`", "Unable to read --backend")
			}
			if backendName != "" && backendName != "tmux" && backendName != "herdr" {
				return usageError("unsupported_input", "Run `munsu backend capabilities --backend tmux` or `munsu backend capabilities --backend herdr`", fmt.Sprintf("Unsupported backend %q", backendName))
			}
			if _, err := contractOutput(cmd); err != nil {
				return err
			}
			if backendName == "" {
				_, backendName, err = backend.Resolve(ctx.Home, "")
				if err != nil {
					return operationError("dependency_unavailable", "Configure a supported session backend and rerun `munsu backend capabilities`", "No supported session backend is available")
				}
			}
			return writeContract(cmd, Response[BackendCapabilities]{
				SchemaVersion: SchemaVersion,
				Kind:          "backend.capabilities",
				Status:        "success",
				Data: BackendCapabilities{
					Backend:  backendName,
					Features: []string{"create_session", "send_input", "pane_liveness"},
				},
				Help: []string{"Run `munsu task observe <task-id>` to inspect a task"},
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
			if _, err := home.ReadMeta(ctx.Home, args[0]); err != nil {
				return operationError("not_found", "Run `munsu task list` to find a task ID", fmt.Sprintf("Task %q was not found", args[0]))
			}
			state, err := fleet.ReadWithProbe(ctx.Home, args[0], runtimeTaskEndpointProbe())
			if err != nil {
				return operationError("internal", "Run `munsu task observe "+args[0]+"` again", "Unable to observe task state")
			}
			meta, _ := home.ReadMeta(ctx.Home, args[0])
			if meta["kind"] == "captain" {
				status := fleet.CaptainStatus(ctx.Home, fleet.CaptainIDFromTask(args[0], meta), meta["home"])
				state.PaneAlive = status == "alive"
				if summary := fleet.SummarizeCaptainHome(meta["home"]); summary.Valid {
					state.Status = summary.State
				}
			}
			result := TaskObserve{TaskID: state.TaskID, Status: state.Status, PaneAlive: &state.PaneAlive}
			if fields["description"] {
				result.Description = state.Description
			}
			if fields["branch"] {
				result.Branch = branchFor(meta)
			}
			if fields["no_mistakes_step"] {
				result.NoMistakesStep = state.NoMistakesRunStep
			}
			help := []string{"Run `munsu task observe " + args[0] + " --fields description,branch` for expanded fields"}
			return writeContract(cmd, Response[TaskObserve]{
				SchemaVersion: SchemaVersion,
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
	var harnessFlag string
	cmd := &cobra.Command{
		Use:   "guard",
		Short: "Report fleet guard conditions, or act as a harness Stop-hook guard (--harness)",
		Args:  contractNoArgs,
		RunE: withHome(func(cmd *cobra.Command, _ []string, ctx Ctx) error {
			// Harness Stop-hook guard: --harness routes to the harness-specific guard
			// (adapter hooks call `munsu guard --harness <X>`). Omit --harness for the
			// contract guard report below.
			switch harnessFlag {
			case "agy":
				return runGuardAgy(ctx.Home)
			case "claude":
				return runGuardClaude(ctx.Home)
			case "grok":
				return runGuardGrok(ctx.Home)
			case "codex", "opencode":
				return runGuardCodexLike(ctx.Home)
			}
			if _, err := contractOutput(cmd); err != nil {
				return err
			}

			// Count in-flight tasks for unified evaluation
			inFlight := 0
			snap, snapErr := fleet.Snapshot(ctx.Home)
			if snapErr == nil && snap != nil {
				for _, ts := range snap.Tasks {
					if ts.Kind == "ship" || ts.Kind == "scout" {
						inFlight++
					}
				}
			}

			// Use shared guard evaluation (same as middleware)
			result := orchestrator.EvaluateGuard(ctx.Home, inFlight, time.Now())
			beatStatus := result.BeatStatus

			// Build structured conditions with stable codes
			var allConditions []orchestrator.ConditionInfo
			if !beatStatus.Exists {
				allConditions = append(allConditions, orchestrator.ConditionInfo{
					Code:    orchestrator.ConditionWatcherAbsent,
					Message: "WATCHER NEVER STARTED - no liveness beacon",
				})
			} else if beatStatus.Stale {
				allConditions = append(allConditions, orchestrator.ConditionInfo{
					Code: orchestrator.ConditionWatcherStale,
					Message: fmt.Sprintf(
						"WATCHER BEACON STALE - last beat %v ago (grace %v)",
						beatStatus.Age.Round(time.Second), orchestrator.StaleThreshold()),
				})
			}
			allConditions = append(allConditions, result.Conditions...)

			var violations []GuardViolation
			for _, c := range allConditions {
				v := GuardViolation{
					Code:      string(c.Code),
					Condition: c.Message,
					Evidence:  []string{"munsu guard", "state/.wake-queue"},
				}
				violations = append(violations, v)
			}

			// Check project tangles
			projects, err := fleet.List(ctx.Home)
			if err == nil {
				for _, entry := range projects {
					projectDir, resolveErr := fleet.ResolveRepoPath(ctx.Home, entry.Name)
					if resolveErr != nil {
						continue
					}
					if guardErr := backend.AssertNotTangled(projectDir, entry.Name); guardErr != nil {
						v := GuardViolation{
							Condition: guardErr.Error(),
							Evidence:  []string{"git worktree list", "project: " + entry.Name},
						}
						violations = append(violations, v)
					}
				}
			}

			// Determine state
			state := "healthy"
			if !beatStatus.Exists || beatStatus.Stale {
				state = "unhealthy"
			} else if len(violations) > 0 {
				state = "indeterminate"
			}

			// Build contextual help based on guard state
			var guardHelp []string
			switch state {
			case "unhealthy":
				guardHelp = []string{
					"Run `munsu watch ensure` to start or restart the watcher",
					"Run `munsu fleet snapshot --version 2` to inspect fleet state",
				}
			case "indeterminate":
				guardHelp = []string{
					"Review and resolve the listed violations above",
					"Run `munsu fleet snapshot --version 2` to inspect fleet state",
				}
			default:
				guardHelp = []string{"Run `munsu fleet snapshot --version 2` to inspect fleet state"}
			}

			// Merge condition messages for backward compat
			var backwardCompatConditions []string
			for _, c := range allConditions {
				backwardCompatConditions = append(backwardCompatConditions, c.Message)
			}

			return writeContract(cmd, Response[Guard]{
				SchemaVersion: SchemaVersion,
				Kind:          "guard",
				Status:        "success",
				Data: Guard{
					State:      state,
					Violations: violations,
					Conditions: backwardCompatConditions,
				},
				Help: guardHelp,
			})
		}),
	}
	configureContractCommand(cmd)
	cmd.Flags().StringVar(&harnessFlag, "harness", "", "Stop-hook guard mode: agy (stdout decision JSON + exit 0), claude/codex/opencode (exit 2 + stderr), grok (passive Stop hook). Omit for the contract guard report.")
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
