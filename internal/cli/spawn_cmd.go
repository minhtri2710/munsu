package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
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
				p, err := fleet.ResolveFromCwd(ctx.Home)
				if err != nil {
					return fmt.Errorf("no project argument and cannot infer from cwd: %w\n  Pass the project name: munsu spawn %s <project>\n  Or register this repo: munsu project add <name> <path>", err, id)
				}
				projectName = p.Name
				fmt.Fprintf(os.Stderr, "info: inferred project %q from cwd\n", projectName)
			}

			// Resolve project mode from registry
			projectMode, _, projErr := fleet.Mode(ctx.Home, projectName)
			if projErr != nil {
				projectMode = "" // registry not set or not found — will use other fallbacks
			}

			// Compose the Task Authority over the exact home the Runner will
			// use: construction is side-effect free and the Authority is passed
			// into fleet.Args for the worktree binding cutover (Task 4.1).
			taskAuthority, err := ctx.TaskAuthority()
			if err != nil {
				return fmt.Errorf("composing task authority for spawn: %w", err)
			}

			_, err = fleet.Spawn(fleet.Args{
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
				HomeDir:     ctx.Home,
				Endpoints:   newSpawnSessionEndpoints(),
				Arm:         arm,
				Authority:   taskAuthority,
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
	cmd.Flags().BoolVar(&force, "force", false, "Bypass captain task authority checks")
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
			if err := fleet.GateRefuseFromCWD(); err != nil {
				return fmt.Errorf("send refused: %w", err)
			}

			// Read meta to determine kind and resolve captain info.
			meta, err := home.ReadMeta(ctx.Home, id)
			if err != nil {
				return fmt.Errorf("reading task %s: %w", id, err)
			}

			isCaptain := meta["kind"] == "captain"

			if isCaptain {
				// Mailbox Store/Receiver flow for captain targets.
				smID := fleet.CaptainIDFromTask(id, meta)
				captainHome := meta["home"]
				if captainHome == "" {
					return fmt.Errorf("captain %s has no home in meta", smID)
				}
				sm := fleet.Info{ID: smID, Home: captainHome}
				if err := ensureCaptainReady(ctx.Home, sm, newSessionProbeEndpoint(), recoverCaptainEndpoint); err != nil {
					return fmt.Errorf("captain %s: %w", smID, err)
				}

				result := fleet.SendMailboxToCaptain(sm, ctx.Home, line, newSessionMailboxSender())
				if result.Err != nil {
					return fmt.Errorf("captain %s: %w", smID, result.Err)
				}

				// Pending retained until exact ack reconciles via converge.
				msg := fmt.Sprintf("sent to captain %s (message=%s, pending until ack)", smID, result.MessageID)
				if !result.Acknowledged {
					msg = fmt.Sprintf("sent to captain %s (message=%s, notification pending)", smID, result.MessageID)
				}

				return writeContract(cmd, Response[MessageResult]{
					SchemaVersion: SchemaVersion,
					Kind:          "send",
					Status:        "success",
					Data:          MessageResult{Message: msg},
				})
			}

			// Non-captain: mailbox-based send with busy-queueing for soldier tasks.
			// Derive sender identity from the captain/general home.
			senderIdentity, _, identErr := orchestrator.ReadHomeIdentity(ctx.Home)
			if identErr != nil {
				// Fallback to home basename.
				senderIdentity = filepath.Base(ctx.Home)
			}

			sendResult := fleet.SendToSoldier(ctx.Home, id, senderIdentity, line, newSessionSoldierEndpoints())
			if sendResult.Err != nil {
				return fmt.Errorf("soldier %s: %w", id, sendResult.Err)
			}

			if sendResult.Queued {
				return writeContract(cmd, Response[MessageResult]{
					SchemaVersion: SchemaVersion,
					Kind:          "send",
					Status:        "success",
					Data:          MessageResult{Message: fmt.Sprintf("queued to %s (message=%s): soldier busy", id, sendResult.MessageID)},
				})
			}

			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "send",
				Status:        "success",
				Data:          MessageResult{Message: fmt.Sprintf("sent to %s (message=%s)", id, sendResult.MessageID)},
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
			meta, err := home.ReadMeta(ctx.Home, id)
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
			state, err := fleet.ReadWithProbe(ctx.Home, id, runtimeTaskEndpointProbe())
			if err != nil {
				if errors.Is(err, taskauthority.ErrNotFound) {
					if _, metaErr := home.ReadMeta(ctx.Home, id); metaErr == nil {
						return operationError("invalid_state", "Run `munsu task reconcile "+id+"` or observe it after canonical Task truth is established",
							fmt.Sprintf("Task %q in home %q has no canonical Task Authority record; observation refuses the legacy projection", id, ctx.Home))
					}
					return operationError("not_found", "Run `munsu task list` to find a task ID",
						fmt.Sprintf("Task %q was not found in home %q", id, ctx.Home))
				}
				return operationError("invalid_state", "Run `munsu task reconcile "+id+"` or observe it again after Task truth is readable",
					fmt.Sprintf("Unable to read authoritative Task truth for task %q in home %q: %v", id, ctx.Home, err))
			}
			return writeContract(cmd, Response[TaskObserve]{
				SchemaVersion: SchemaVersion,
				Kind:          "task.observe",
				Status:        "success",
				Data: TaskObserve{
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

			auth, err := ctx.TaskAuthority()
			if err != nil {
				return err
			}
			tid, err := domain.NewTaskID(id)
			if err != nil {
				return err
			}
			// Canonical preflight: the kind is an authoritative TaskDefinition
			// field; the projection can never be the preflight source or
			// override the canonical record.
			agg, err := auth.Get(tid)
			if err != nil {
				return fmt.Errorf("promote %s: %w", id, err)
			}
			if agg.Definition.Kind != "scout" {
				return fmt.Errorf("task %s has kind=%q, can only promote kind=scout", id, agg.Definition.Kind)
			}

			// Preflight: require the current generation's report to exist
			if !fleet.ReportExists(ctx.Home, id, agg.Generation) {
				return fmt.Errorf("no report found for scout task %s: write the generation %s report at %s before promoting", id, agg.Generation, fleet.ReportPath(ctx.Home, id, agg.Generation))
			}

			// The promotion is a named canonical operation: it flips the
			// generation-bound kind scout → ship with the exact generation/
			// revision precondition and the phase prerequisite (done or
			// resolved) enforced inside one atomic transaction. A stale
			// .status projection can never authorize or block the transition.
			req := taskauthority.CanonicalPromoteRequest{
				HomeID:       auth.HomeID(),
				TaskID:       tid,
				Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
				CurrentKind:  "scout",
				TargetKind:   "ship",
				Reason:       "cli promote",
			}
			op, err := newCanonicalOperation("promote", req)
			if err != nil {
				return err
			}
			if _, err := auth.Promote(op, req); err != nil {
				return err
			}

			// .meta kind is a post-commit projection (ADR-0007 §7): derive the
			// authoritative fields from the canonical aggregate after the
			// receipt. A projection failure is a typed partial error and never
			// rolls back the committed promotion.
			after, err := auth.Get(tid)
			if err != nil {
				return &LifecyclePartialError{TaskID: id, State: string(agg.Phase), Cause: err}
			}
			if perr := projectTaskMeta(ctx.Home, after, nil); perr != nil {
				return &LifecyclePartialError{TaskID: id, State: string(agg.Phase), Cause: perr}
			}

			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "promote",
				Status:        "success",
				Data:          MessageResult{Message: fmt.Sprintf("Task %s promoted from scout to ship", id)},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func newTeardownCmd() *cobra.Command {
	var force bool
	var targetGen uint

	cmd := &cobra.Command{
		Use:   "teardown <id>",
		Short: "Tear down a soldier",
		Long: `Tear down a soldier by its task ID.

Safety checks require a scout to have its own generation's report
(report-g<generation>.md) with no unresolved decision holds before teardown
proceeds. Use --force to skip all safety checks.

With --force:
  - Skips report and decision-hold checks
  - Use when the scout completed without a formal report
  - data/<id>/ is treated exactly as it is without --force: --force never
    deletes more than a plain teardown does. A scout report is born
    generation-named, so teardown retains it in place and no later
    generation inherits it as its own evidence.

--generation binds this invocation to the exact generation it intends to
retire (captured when the teardown request was issued). A delayed retry that
observes the task reopened to a newer generation fails closed instead of
implicitly retiring the newer generation; a fresh teardown of a reopened
generation must pass its explicit generation here.
`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]

			// Gate refusal: no-mistakes gate agents must not drive fleet lifecycle.
			if err := fleet.GateRefuseFromCWD(); err != nil {
				return fmt.Errorf("teardown refused: %w", err)
			}

			opts := fleet.Options{
				HomeDir: ctx.Home,
				ID:      id,
				Force:   force,
			}
			if targetGen > 0 {
				g := taskauthority.Generation(targetGen)
				opts.ExpectedGeneration = &g
			}

			// Resolve the task home (cross-home retirement: a handed-off task
			// lives in a captain home) and compose the Task Authority over it
			// (Task 7.7). The authoritative retirement transition commits
			// through the Authority; the fleet teardown performs the saga-side
			// cleanup after the durable receipt.
			taskHome, _, err := fleet.ResolveTaskHome(ctx.Home, id)
			if err != nil {
				return fmt.Errorf("teardown %s: %w", id, err)
			}
			auth, err := ctx.TaskAuthorityFor(taskHome)
			if err != nil {
				return fmt.Errorf("teardown %s: composing task authority: %w", id, err)
			}
			opts.HomeDir = taskHome

			result, err := fleet.RetireTask(opts, newSessionBoundTeardown(), orchestratorRetirementJournals{}, auth)
			if err != nil {
				return err
			}

			var b strings.Builder
			b.WriteString(fmt.Sprintf("Teardown %s completed:\n", id))
			for _, step := range result.Steps {
				b.WriteString(fmt.Sprintf("  - %s\n", step))
			}
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "teardown",
				Status:        "success",
				Data:          MessageResult{Message: strings.TrimSpace(b.String())},
			})
		}),
	}
	configureContractCommand(cmd)

	cmd.Flags().BoolVar(&force, "force", false, "Skip safety checks")
	cmd.Flags().UintVar(&targetGen, "generation", 0, "Bind this teardown to the exact generation it intends to retire; a delayed retry never retires a newer generation")

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

			result := fleet.FlushPendingSoldierCommands(ctx.Home, id, senderIdentity, newSessionSoldierEndpoints())
			if result.Err != nil {
				return fmt.Errorf("soldier %s flush: %w", id, result.Err)
			}

			if result.Queued {
				return writeContract(cmd, Response[MessageResult]{
					SchemaVersion: SchemaVersion,
					Kind:          "soldier-flush",
					Status:        "success",
					Data:          MessageResult{Message: fmt.Sprintf("%s: soldier still busy, pending retained", id)},
				})
			}

			if result.MessageID == "" {
				return writeContract(cmd, Response[MessageResult]{
					SchemaVersion: SchemaVersion,
					Kind:          "soldier-flush",
					Status:        "success",
					Data:          MessageResult{Message: fmt.Sprintf("%s: no pending commands", id)},
				})
			}

			if result.Sent {
				return writeContract(cmd, Response[MessageResult]{
					SchemaVersion: SchemaVersion,
					Kind:          "soldier-flush",
					Status:        "success",
					Data:          MessageResult{Message: fmt.Sprintf("%s: flushed pending command (message=%s)", id, result.MessageID)},
				})
			}

			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "soldier-flush",
				Status:        "success",
				Data:          MessageResult{Message: fmt.Sprintf("%s: no action taken", id)},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}
