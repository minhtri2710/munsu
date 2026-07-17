package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/minhtri2710/munsu/internal/brief"
	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/crewstate"
	"github.com/minhtri2710/munsu/internal/project"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/spawn"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/teardown"
	"github.com/spf13/cobra"
)

func newSpawnCmd() *cobra.Command {
	var (
		kind        string
		mode        string
		yolo        bool
		backend     string
		harnessFlag string
		arm         bool
	)

	cmd := &cobra.Command{
		Use:   "spawn <id> [<project>]",
		Short: "Spawn a crewmate agent (project inferred from cwd if omitted)",
		Long: `Spawn a crewmate agent.

Project can be omitted when the current working directory is inside a git
repository that matches a registered project or can be ad-hoc inferred.

Precedence: explicit project arg > registry match on cwd path > adhoc git
remote/name heuristics.

When inference fails, pass the project name explicitly or run 'munsu project add'.`,
		Args: cobra.MatchAll(MinimumNArgs(1), cobra.MaximumNArgs(2)),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]

			// Resolve project name: explicit arg, or infer from cwd
			var projectName string
			if len(args) >= 2 {
				projectName = args[1]
			} else {
				p, err := project.ResolveFromCwd(ctx.Home)
				if err != nil {
					return fmt.Errorf("no project argument and cannot infer from cwd: %w\n  Pass the project name: munsu spawn %s <project>\n  Or register this repo: munsu project add <name> <path>", err, id)
				}
				projectName = p.Name
				fmt.Fprintf(os.Stderr, "info: inferred project %q from cwd\n", projectName)
			}

			// Resolve project mode from registry
			projectMode, _, projErr := project.Mode(ctx.Home, projectName)
			if projErr != nil {
				projectMode = "" // registry not set or not found — will use other fallbacks
			}

			_, err := spawn.Run(spawn.Args{
				ID:          id,
				ProjectName: projectName,
				Kind:        kind,
				Mode:        mode,        // raw flag value; resolution happens inside Run
				ProjectMode: projectMode, // raw project mode; resolution happens inside Run
				Yolo:        yolo,
				Backend:     backend,
				HarnessFlag: harnessFlag,
				HomeDir:     homeOverride,
				Arm:         arm,
			})
			if err != nil {
				return err
			}

			if !arm {
				fmt.Fprintln(os.Stderr, "hint: arm the watcher with 'munsu watch-arm' or 'munsu spawn --arm' to auto-detect completion")
			}
			return nil
		}),
	}
	cmd.Flags().StringVar(&kind, "kind", "ship", "Task kind (ship|scout)")
	cmd.Flags().StringVar(&mode, "mode", "", "Delivery mode (no-mistakes|direct-PR|local-only; empty=auto-detect)")
	cmd.Flags().BoolVar(&yolo, "yolo", false, "Skip pre-flight checks")
	cmd.Flags().StringVar(&backend, "backend", "", "Session backend (tmux|herdr)")
	cmd.Flags().StringVar(&harnessFlag, "harness", "", "Override crewmate harness (pi, agy, etc.)")
	cmd.Flags().BoolVar(&arm, "arm", false, "Arm the watcher after spawn (warn-only on failure)")

	return cmd
}

func newSendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send <id> <line>",
		Short: "Send a line to a crewmate endpoint",
		Args:  ExactArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]
			line := args[1]

			// Read meta to resolve window
			meta, err := task.ReadMeta(ctx.Home, id)
			if err != nil {
				return fmt.Errorf("reading task %s: %w", id, err)
			}
			windowID, ok := meta["window"]
			if !ok {
				return fmt.Errorf("task %s has no window endpoint", id)
			}

			bk, _, err := session.BackendForTask(ctx.Home, meta)
			if err != nil {
				return err
			}
			if err := bk.SendKeys(windowID, line); err != nil {
				return fmt.Errorf("sending to %s: %w", id, err)
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "send",
				Status:        "success",
				Data:          contract.MessageResult{Message: fmt.Sprintf("sent to %s: %s", id, line)},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func newPeekCmd() *cobra.Command {
	var lines int

	cmd := &cobra.Command{
		Use:   "peek <id>",
		Short: "Peek at crewmate output",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]

			// Read meta to resolve window
			meta, err := task.ReadMeta(ctx.Home, id)
			if err != nil {
				return fmt.Errorf("reading task %s: %w", id, err)
			}
			windowID, ok := meta["window"]
			if !ok {
				return fmt.Errorf("task %s has no window endpoint", id)
			}

			bk, _, err := session.BackendForTask(ctx.Home, meta)
			if err != nil {
				return err
			}
			out, err := bk.Capture(windowID, lines)
			if err != nil {
				return fmt.Errorf("capturing from %s: %w", id, err)
			}

			// Print with a header showing lines count
			count := strings.Count(out, "\n")
			if out != "" && out[len(out)-1] == '\n' && count > 0 {
				// tmux output often ends with newline; don't overcount
				count--
			}
			if count == 0 && out != "" {
				count = 1
			}
			fmt.Printf("--- %s (captured %d lines) ---\n", id, count)
			fmt.Print(out)
			if out != "" && out[len(out)-1] != '\n' {
				fmt.Println()
			}
			return nil
		}),
	}

	cmd.Flags().IntVar(&lines, "lines", 40, "Number of lines to capture")

	return cmd
}

func newCrewStateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "crew-state <id>",
		Short: "Read crewmate current state",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]
			if _, err := contractOutput(cmd); err != nil {
				return err
			}
			state, err := crewstate.Read(ctx.Home, id)
			if err != nil {
				return operationError("internal", "Run `munsu crew-state "+id+"` again", "Unable to read crew state")
			}
			return writeContract(cmd, contract.Response[contract.TaskObserve]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "task.observe",
				Status:        "success",
				Data: contract.TaskObserve{
					TaskID:              state.TaskID,
					Status:              state.Status,
					Description:         state.Description,
					PaneAlive:           &state.PaneAlive,
					NoMistakesStep:      state.NoMistakesRunStep,
					StatusLines:         state.StatusLines,
					StatusLogSuperseded: state.StatusLogSuperseded,
				},
				Help: []string{"Run `munsu task observe " + id + " --fields description,branch` for expanded fields"},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func newPromoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "promote <id>",
		Short: "Promote a scout task to ship",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]

			// Preflight: verify task meta exists with kind=scout
			meta, err := task.ReadMeta(ctx.Home, id)
			if err != nil {
				return fmt.Errorf("reading meta for %s: %w", id, err)
			}
			if meta["kind"] != "scout" {
				return fmt.Errorf("task %s has kind=%q, can only promote kind=scout", id, meta["kind"])
			}

			// Preflight: require report.md to exist
			if !brief.ReportExists(ctx.Home, id) {
				return fmt.Errorf("no report found for scout task %s: write report at %s before promoting", id, brief.ReportPath(ctx.Home, id))
			}

			// Preflight: require last status to be done or resolved
			if statusLines, err := task.ReadStatus(ctx.Home, id); err == nil && len(statusLines) > 0 {
				lastLine := statusLines[len(statusLines)-1]
				lastStatus, _ := task.ParseStatusKey(lastLine)
				if !strings.HasPrefix(lastStatus, "done") && !strings.HasPrefix(lastStatus, "resolved") {
					return fmt.Errorf("task %s has last status %q, need 'done' or 'resolved' before promote", id, lastStatus)
				}
			} else {
				return fmt.Errorf("task %s has no status: report done or resolved before promoting", id)
			}

			if err := task.PromoteMeta(ctx.Home, id); err != nil {
				return fmt.Errorf("promote %s: %w", id, err)
			}

			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "promote",
				Status:        "success",
				Data:          contract.MessageResult{Message: fmt.Sprintf("Task %s promoted from scout to ship", id)},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func newTeardownCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "teardown <id>",
		Short: "Tear down a crewmate",
		Long: `Tear down a crewmate by its task ID.

Safety checks require a scout to have a report.md with no unresolved decision
holds before teardown proceeds. Use --force to skip all safety checks.

With --force:
  - Skips report.md and decision-hold checks
  - Removes data/<id>/ including report.md and brief.md
  - Use when the scout completed without a formal report or for cleanup
`,
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]

			opts := teardown.Options{
				HomeDir: ctx.Home,
				ID:      id,
				Force:   force,
			}

			result, err := teardown.Run(opts)
			if err != nil {
				return err
			}

			var b strings.Builder
			b.WriteString(fmt.Sprintf("Teardown %s completed:\n", id))
			for _, step := range result.Steps {
				b.WriteString(fmt.Sprintf("  - %s\n", step))
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "teardown",
				Status:        "success",
				Data:          contract.MessageResult{Message: strings.TrimSpace(b.String())},
			})
		}),
	}
	configureContractCommand(cmd)

	cmd.Flags().BoolVar(&force, "force", false, "Skip safety checks")

	return cmd
}
