package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/brief"
	"github.com/minhtri2710/munsu/internal/captain"
	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/project"
	"github.com/minhtri2710/munsu/internal/scope"
	"github.com/minhtri2710/munsu/internal/soldierstate"
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
		force       bool
		reopen      bool
		backend     string
		harnessFlag string
		modelFlag   string
		effortFlag  string
		arm         bool
	)

	cmd := &cobra.Command{
		Use:   "spawn <id> [<project>]",
		Short: "Spawn a soldier agent (project inferred from cwd if omitted)",
		Long: `Spawn a soldier agent.

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
				Force:       force,
				Reopen:      reopen,
				Backend:     backend,
				HarnessFlag: harnessFlag,
				ModelFlag:   modelFlag,
				EffortFlag:  effortFlag,
				HomeDir:     homeOverride,
				Endpoints:   newSpawnSessionEndpoints(),
				Arm:         arm,
			})
			if err != nil {
				return err
			}

			if !arm {
				fmt.Fprintln(os.Stderr, "hint: ensure supervision with 'munsu watch ensure' or use 'munsu spawn --arm'")
			}
			return nil
		}),
	}
	cmd.Flags().StringVar(&kind, "kind", "ship", "Task kind (ship|scout)")
	cmd.Flags().StringVar(&mode, "mode", "", "Delivery mode (no-mistakes|direct-PR|local-only; empty=auto-detect)")
	cmd.Flags().BoolVar(&yolo, "yolo", false, "Skip pre-flight checks")
	cmd.Flags().BoolVar(&force, "force", false, "Bypass captain backlog authority checks")
	cmd.Flags().BoolVar(&reopen, "reopen", false, "Allow spawning a done/blocked task (reopen)")
	cmd.Flags().StringVar(&backend, "backend", "", "Session backend (tmux|herdr)")
	cmd.Flags().StringVar(&harnessFlag, "harness", "", "Override soldier harness (pi, agy, etc.)")
	cmd.Flags().StringVar(&modelFlag, "model", "", "Override model for the soldier harness")
	cmd.Flags().StringVar(&effortFlag, "effort", "", "Override effort/thinking level (harness-specific)")
	cmd.Flags().BoolVar(&arm, "arm", false, "Arm the watcher after spawn (warn-only on failure)")
	return cmd
}

func newSendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send <id> <line>",
		Short: "Send a line to a soldier/captain endpoint (captain uses mailbox Store/Receiver)",
		Args:  ExactArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]
			line := args[1]

			// BREAKING: uplink to the General (fleet top) fails closed — use munsu report.
			// Note: 'captain:<id>' task ids are legit downlink targets for the General
			// dispatching work, so they are NOT blocked here. A Captain reporting to its
			// General home uses 'munsu report', not send.
			if id == "general" {
				return fmt.Errorf("error: uplink use munsu report; send is downlink only")
			}

			// Gate refusal: no-mistakes gate agents must not drive fleet lifecycle.
			if err := scope.GateRefuseFromCWD(); err != nil {
				return fmt.Errorf("send refused: %w", err)
			}

			// Read meta to determine kind and resolve captain info.
			meta, err := task.ReadMeta(ctx.Home, id)
			if err != nil {
				return fmt.Errorf("reading task %s: %w", id, err)
			}

			isCaptain := meta["kind"] == "captain"

			if isCaptain {
				// Mailbox Store/Receiver flow for captain targets.
				smID := captain.CaptainIDFromTask(id, meta)
				captainHome := meta["home"]
				if captainHome == "" {
					return fmt.Errorf("captain %s has no home in meta", smID)
				}
				sm := captain.Info{ID: smID, Home: captainHome}

				result := captain.SendMailboxToCaptain(sm, ctx.Home, line, newSessionMailboxSender())
				if result.Err != nil {
					return fmt.Errorf("captain %s: %w", smID, result.Err)
				}

				// Pending retained until exact ack reconciles via converge.
				msg := fmt.Sprintf("sent to captain %s (message=%s, pending until ack)", smID, result.MessageID)
				if !result.Acknowledged {
					msg = fmt.Sprintf("sent to captain %s (message=%s, notification pending)", smID, result.MessageID)
				}

				return writeContract(cmd, contract.Response[contract.MessageResult]{
					SchemaVersion: contract.SchemaVersion,
					Kind:          "send",
					Status:        "success",
					Data:          contract.MessageResult{Message: msg},
				})
			}

			// Non-captain: mailbox-based send with busy-queueing for soldier tasks.
			// Derive sender identity from the captain/general home.
			senderIdentity, _, identErr := orchestrator.ReadHomeIdentity(ctx.Home)
			if identErr != nil {
				// Fallback to home basename.
				senderIdentity = filepath.Base(ctx.Home)
			}

			sendResult := captain.SendToSoldier(ctx.Home, id, senderIdentity, line, newSessionSoldierEndpoints())
			if sendResult.Err != nil {
				return fmt.Errorf("soldier %s: %w", id, sendResult.Err)
			}

			if sendResult.Queued {
				return writeContract(cmd, contract.Response[contract.MessageResult]{
					SchemaVersion: contract.SchemaVersion,
					Kind:          "send",
					Status:        "success",
					Data:          contract.MessageResult{Message: fmt.Sprintf("queued to %s (message=%s): soldier busy", id, sendResult.MessageID)},
				})
			}

			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "send",
				Status:        "success",
				Data:          contract.MessageResult{Message: fmt.Sprintf("sent to %s (message=%s)", id, sendResult.MessageID)},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

type BoundCapture interface {
	Capture(homeDir string, meta map[string]string, lines int) (string, error)
}

func newPeekCmd() *cobra.Command { return newPeekCmdWithCapture(sessionBoundCapture{}) }

func newPeekCmdWithCapture(capture BoundCapture) *cobra.Command {
	var lines int

	cmd := &cobra.Command{
		Use:   "peek <id>",
		Short: "Peek at soldier output",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]

			// Read meta to resolve window
			meta, err := task.ReadMeta(ctx.Home, id)
			if err != nil {
				return fmt.Errorf("reading task %s: %w", id, err)
			}
			if _, ok := meta["window"]; !ok {
				return fmt.Errorf("task %s has no window endpoint", id)
			}

			if capture == nil {
				return fmt.Errorf("task-bound capture is not configured")
			}
			out, err := capture.Capture(ctx.Home, meta, lines)
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

func newSoldierStateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "soldier-state <id>",
		Short: "Read soldier current state",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]
			if _, err := contractOutput(cmd); err != nil {
				return err
			}
			state, err := soldierstate.Read(ctx.Home, id)
			if err != nil {
				return operationError("internal", "Run `munsu soldier-state "+id+"` again", "Unable to read soldier state")
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
		Short: "Tear down a soldier",
		Long: `Tear down a soldier by its task ID.

Safety checks require a scout to have a report.md with no unresolved decision
holds before teardown proceeds. Use --force to skip all safety checks.

With --force:
  - Skips report.md and decision-hold checks
  - Removes data/<id>/ including report.md and brief.md
  - Use when the scout completed without a formal report or for cleanup
`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]

			// Gate refusal: no-mistakes gate agents must not drive fleet lifecycle.
			if err := scope.GateRefuseFromCWD(); err != nil {
				return fmt.Errorf("teardown refused: %w", err)
			}

			opts := teardown.Options{
				HomeDir: ctx.Home,
				ID:      id,
				Force:   force,
			}

			result, err := teardown.RunWithBackend(opts, newSessionBoundTeardown())
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

func newSoldierFlushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "soldier-flush <id>",
		Short: "Flush one pending command to a soldier",
		Long: `Read the captain's pending outbox for the given soldier and
send the oldest pending command via SubmitPrompt, but only if the soldier
is not busy. This is triggered by a review-ready event from the soldier.

Idempotent: calling when no pending exists is a no-op.
Calling when the soldier is busy returns with "still busy".
`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]

			// Derive sender identity from the captain/general home.
			senderIdentity, _, identErr := orchestrator.ReadHomeIdentity(ctx.Home)
			if identErr != nil {
				senderIdentity = filepath.Base(ctx.Home)
			}

			result := captain.FlushPendingSoldierCommands(ctx.Home, id, senderIdentity, newSessionSoldierEndpoints())
			if result.Err != nil {
				return fmt.Errorf("soldier %s flush: %w", id, result.Err)
			}

			if result.Queued {
				return writeContract(cmd, contract.Response[contract.MessageResult]{
					SchemaVersion: contract.SchemaVersion,
					Kind:          "soldier-flush",
					Status:        "success",
					Data:          contract.MessageResult{Message: fmt.Sprintf("%s: soldier still busy, pending retained", id)},
				})
			}

			if result.MessageID == "" {
				return writeContract(cmd, contract.Response[contract.MessageResult]{
					SchemaVersion: contract.SchemaVersion,
					Kind:          "soldier-flush",
					Status:        "success",
					Data:          contract.MessageResult{Message: fmt.Sprintf("%s: no pending commands", id)},
				})
			}

			if result.Sent {
				return writeContract(cmd, contract.Response[contract.MessageResult]{
					SchemaVersion: contract.SchemaVersion,
					Kind:          "soldier-flush",
					Status:        "success",
					Data:          contract.MessageResult{Message: fmt.Sprintf("%s: flushed pending command (message=%s)", id, result.MessageID)},
				})
			}

			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "soldier-flush",
				Status:        "success",
				Data:          contract.MessageResult{Message: fmt.Sprintf("%s: no action taken", id)},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}
