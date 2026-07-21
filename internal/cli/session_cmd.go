package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/afk"
	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/brief"
	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/project"
	"github.com/minhtri2710/munsu/internal/scope"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/spawn"
	"github.com/minhtri2710/munsu/internal/supervision"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/turnend"
	"github.com/minhtri2710/munsu/internal/waker"
	"github.com/spf13/cobra"
)


func newBriefCmd() *cobra.Command {
	var scout bool
	var force bool
	var modeFlag string

	cmd := &cobra.Command{
		Use:   "brief <id> <repo>",
		Short: "Scaffold a task brief",
		Args:  ExactArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]
			repo := args[1]

			// Resolve delivery mode using full auto-detection chain
			projectMode := ""
			projYolo := false
			if m, y, err := project.Mode(ctx.Home, repo); err == nil {
				projectMode = m
				projYolo = y
			}

			resolvedMode, err := spawn.ResolveDeliveryMode(ctx.Home, modeFlag, projectMode)
			if err != nil {
				return err
			}

			// Require existing task meta unless --force
			if !force {
				if _, err := task.ReadMeta(ctx.Home, id); err != nil {
					return fmt.Errorf("task %q not found: create it with 'munsu task add %s ...' or use --force", id, id)
				}
			}
			opts := brief.ScaffoldOptions{
				HomeDir: ctx.Home,
				ID:      id,
				Repo:    repo,
				Scout:   scout,
				Mode:    resolvedMode,
				Yolo:    projYolo,
			}

			if err := brief.Scaffold(opts); err != nil {
				return err
			}

			kind := "ship"
			if scout {
				kind = "scout"
			}

			var b strings.Builder
			b.WriteString(fmt.Sprintf("Brief scaffolded at %s\n", brief.Path(ctx.Home, id)))
			b.WriteString(fmt.Sprintf("  id:    %s\n", id))
			b.WriteString(fmt.Sprintf("  repo:  %s\n", repo))
			b.WriteString(fmt.Sprintf("  kind:  %s\n", kind))
			if resolvedMode != "" {
				b.WriteString(fmt.Sprintf("  mode:  %s\n", resolvedMode))
			}
			if projYolo {
				b.WriteString("  yolo:  true\n")
			}

			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "brief",
				Status:        "success",
				Data:          contract.MessageResult{Message: strings.TrimSpace(b.String())},
			})
		}),
	}
	configureContractCommand(cmd)

	cmd.Flags().BoolVar(&scout, "scout", false, "Generate a scout brief instead of ship brief")
	cmd.Flags().BoolVar(&force, "force", false, "Scaffold brief without requiring existing task meta")
	cmd.Flags().StringVar(&modeFlag, "mode", "", "Delivery mode override (no-mistakes|direct-PR|local-only)")

	return cmd
}

func newSessionStartCmd() *cobra.Command {
	var recover bool
	cmd := &cobra.Command{
		Use:   "session-start",
		Short: "Lock, bootstrap, ensure watcher for in-flight work, and print the session-start digest",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			output, err := contractOutput(cmd)
			if err != nil {
				return err
			}

			// Discard verbose output when JSON contract is requested.
			var w io.Writer = cmd.OutOrStdout()
			if output == contract.OutputJSON {
				w = io.Discard
			}

			// --recover flag or MUNSU_SESSION_RECOVER env opts in captain relaunch.
			wantRecover := recover
			if _, ok := os.LookupEnv("MUNSU_SESSION_RECOVER"); ok {
				wantRecover = true
			}

			result, err := session.RunSessionStartWithWatcher(w, ctx.Home, func(home string) session.WatchEnsureResult {
				r := ensureWatcher(home, false)
				return session.WatchEnsureResult{State: r.Data.State}
			}, func(home string, doRecover bool) session.CaptainLivenessResult {
				return captainLivenessForSession(home, doRecover && wantRecover)
			})
			if err != nil {
				return err
			}

			// Build structured data for contract output.
			lockState := "acquired"
			if !result.LockAcquired {
				lockState = "refused (read-only)"
			}
			watcherState := result.Watcher.State
			if watcherState == "" {
				watcherState = "unknown"
			}

			return writeContract(cmd, contract.Response[contract.SessionStart]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "session.start",
				Status:        "success",
				Data: contract.SessionStart{
					Lock:        lockState,
					Watcher:     watcherState,
					BootstrapOK: result.Bootstrap != nil,
					FleetSyncOK: result.FleetSync != nil,
					Message:     "Session started. Lock: " + lockState + ". Watcher: " + watcherState + ".",
				},
			})
		}),
	}
	configureContractCommand(cmd)
	cmd.Flags().BoolVar(&recover, "recover", false, "Relaunch launched-but-dead captain endpoints detected during the liveness probe")
	return cmd
}

func newBootstrapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap [install <tools>...]",
		Short: "Detect toolchain and run setup sweeps",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			locked := lifecycle.IsSessionLocked(ctx.Home)
			var installTools []string
			if len(args) > 1 && args[0] == "install" {
				installTools = args[1:]
			}
			result, err := bootstrap.Run(ctx.Home, !locked, installTools)
			if err != nil {
				return err
			}

			var b strings.Builder
			for _, d := range result.Tools {
				b.WriteString(d.String() + "\n")
			}
			if result.Auth != nil {
				b.WriteString(result.Auth.String() + "\n")
			}
			for _, c := range result.Configs {
				b.WriteString(c.String() + "\n")
			}
			if result.GC != nil {
				b.WriteString(result.GC.String() + "\n")
			}

			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "bootstrap",
				Status:        "success",
				Data:          contract.MessageResult{Message: strings.TrimSpace(b.String())},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func newWatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Run the persistent watcher daemon",
		Long:  `Run the persistent watcher daemon. Actionable conditions are durably queued while the watcher keeps polling until SIGTERM or SIGINT. Use 'munsu watch run' for one diagnostic cycle. Singleton-safe (home-scoped lock).`,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			reason, err := supervision.Run(ctx.Home)
			if err != nil {
				return err
			}
			message := "watcher stopped"
			if reason != nil {
				message = fmt.Sprintf("stopped: %s — %s", reason.Kind, reason.Message)
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "watch",
				Status:        "success",
				Data:          contract.MessageResult{Message: message},
			})
		}),
	}
	configureContractCommand(cmd)

	// Add subcommands
	ensureCmd := newWatchEnsureCmd()
	ensureCmd.Use = "ensure"
	runCmd := newWatchRunCmd()
	runCmd.Use = "run"
	cmd.AddCommand(ensureCmd)
	cmd.AddCommand(runCmd)
	cmd.AddCommand(newWatchStopCmd())

	return cmd
}

func newWatchArmCmd() *cobra.Command {
	var restart bool
	cmd := &cobra.Command{
		Use:   "watch-arm",
		Short: "Arm the watcher (deprecated: use 'munsu watch ensure')",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			result := ensureWatcher(ctx.Home, restart)
			if result.Status != "success" {
				return fmt.Errorf("watch-arm failed: %s", result.Data.State)
			}
			// Print deprecation warning to stderr
			_, _ = fmt.Fprintf(os.Stderr, "WARNING: 'munsu watch-arm' is deprecated. Use 'munsu watch ensure' instead.\n")
			fmt.Printf("Watcher armed (state=%s)\n", result.Data.State)
			return nil
		}),
	}
	cmd.Flags().BoolVar(&restart, "restart", false, "Restart existing watcher before arming")
	return cmd
}

func newWakeDrainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "wake-drain",
		Short: "Drain queued wakes",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			records, err := waker.Drain(ctx.Home)
			if err != nil {
				return err
			}
			waker.PrintRecords(records)
			return nil
		}),
	}
}

// runGuardClaude implements the Claude Stop hook guard.
// It reads stdin JSON for stop_hook_active (true → exit 0 loop guard)
// and checks fleet state + watcher health for blind-turn detection.
func runGuardClaude(homeDir string) error {
	// Read stdin JSON for loop guard
	stopHookActive := false
	data, err := io.ReadAll(os.Stdin)
	if err == nil {
		var payload map[string]interface{}
		if json.Unmarshal([]byte(strings.TrimSpace(string(data))), &payload) == nil {
			if active, ok := payload["stop_hook_active"].(bool); ok && active {
				stopHookActive = true
			}
		}
	}

	// Loop guard: stop_hook_active means Claude has already been forced
	// to continue one turn. Allow the stop by exiting 0.
	if stopHookActive {
		if err := checkPendingRelayObligations(homeDir); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			exitWithCode(2)
			return nil
		}
		return nil
	}

	// Check scope: only guard primary checkouts
	cls := scope.Classify(homeDir)
	if cls.Err != nil || cls.Identity != scope.Primary {
		if err := checkPendingRelayObligations(homeDir); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			exitWithCode(2)
			return nil
		}
		return nil
	}

	// Check fleet state for in-flight tasks
	inFlight := 0
	snap, snapErr := fleet.Snapshot(homeDir)
	if snapErr == nil {
		for _, ts := range snap.Tasks {
			if ts.Kind == "ship" || ts.Kind == "scout" {
				inFlight++
			}
		}
	}

	// No in-flight work → safe to end turn
	if inFlight == 0 {
		if err := checkPendingRelayObligations(homeDir); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			exitWithCode(2)
			return nil
		}
		return nil
	}

	// Check watcher liveness
	status := lifecycle.ReadBeatStatus(homeDir, time.Now())

	// If watcher is healthy and not stale, allow the stop
	if status.Exists && !status.Stale {
		if err := checkPendingRelayObligations(homeDir); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			exitWithCode(2)
			return nil
		}
		return nil
	}

	// Blind turn: in-flight work + unhealthy watcher → block the stop
	reason := "TURN WOULD END BLIND: "
	if !status.Exists {
		reason += "watcher never started"
	} else {
		reason += fmt.Sprintf("watcher beat stale by %v", status.Age.Round(time.Second))
	}
	reason += fmt.Sprintf(" with %d in-flight task(s)", inFlight)

	// Claude Stop hook block: exit 2 + stderr reason
	fmt.Fprintln(os.Stderr, reason)
	exitWithCode(2)
	return nil // unreachable
}

// runGuardGrok implements the Grok Stop hook guard.
// Grok Stop hooks are passive: exit 2 does not block the turn, but we
// still detect blind-turn conditions and warn.
// runGuardCodexLike implements the Codex Stop hook guard.
// Codex uses the same deny shape as Claude: exit 2 + stderr reason.
func runGuardCodexLike(homeDir string) error {
	// Read stdin JSON for loop guard
	stopHookActive := false
	data, err := io.ReadAll(os.Stdin)
	if err == nil {
		var payload map[string]interface{}
		if json.Unmarshal([]byte(strings.TrimSpace(string(data))), &payload) == nil {
			if active, ok := payload["stop_hook_active"].(bool); ok && active {
				stopHookActive = true
			}
		}
	}

	// Loop guard: stop_hook_active means Codex has already been forced
	// to continue one turn. Allow the stop by exiting 0.
	if stopHookActive {
		if err := checkPendingRelayObligations(homeDir); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			exitWithCode(2)
			return nil
		}
		return nil
	}

	// Check scope: only guard primary checkouts
	cls := scope.Classify(homeDir)
	if cls.Err != nil || cls.Identity != scope.Primary {
		if err := checkPendingRelayObligations(homeDir); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			exitWithCode(2)
			return nil
		}
		return nil
	}

	// Check fleet state for in-flight tasks
	inFlight := 0
	snap, snapErr := fleet.Snapshot(homeDir)
	if snapErr == nil {
		for _, ts := range snap.Tasks {
			if ts.Kind == "ship" || ts.Kind == "scout" {
				inFlight++
			}
		}
	}

	// No in-flight work → safe to end turn
	if inFlight == 0 {
		if err := checkPendingRelayObligations(homeDir); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			exitWithCode(2)
			return nil
		}
		return nil
	}

	// Check watcher liveness
	status := lifecycle.ReadBeatStatus(homeDir, time.Now())

	// If watcher is healthy and not stale, allow the stop
	if status.Exists && !status.Stale {
		if err := checkPendingRelayObligations(homeDir); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			exitWithCode(2)
			return nil
		}
		return nil
	}

	// Blind turn: in-flight work + unhealthy watcher → block the stop
	reason := "TURN WOULD END BLIND: "
	if !status.Exists {
		reason += "watcher never started"
	} else {
		reason += fmt.Sprintf("watcher beat stale by %v", status.Age.Round(time.Second))
	}
	reason += fmt.Sprintf(" with %d in-flight task(s)", inFlight)

	// Codex Stop hook block: exit 2 + stderr reason
	fmt.Fprintln(os.Stderr, reason)
	exitWithCode(2)
	return nil // unreachable
}

func runGuardGrok(homeDir string) error {
	// Read stdin JSON for loop guard
	stopHookActive := false
	data, err := io.ReadAll(os.Stdin)
	if err == nil {
		var payload map[string]interface{}
		if json.Unmarshal([]byte(strings.TrimSpace(string(data))), &payload) == nil {
			if active, ok := payload["stop_hook_active"].(bool); ok && active {
				stopHookActive = true
			}
		}
	}

	// Loop guard: stop_hook_active means Grok has already been forced
	// to continue one turn. Allow the stop by exiting 0.
	if stopHookActive {
		if err := checkPendingRelayObligations(homeDir); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			exitWithCode(2)
			return nil
		}
		return nil
	}

	// Check scope: only guard primary checkouts
	cls := scope.Classify(homeDir)
	if cls.Err != nil || cls.Identity != scope.Primary {
		if err := checkPendingRelayObligations(homeDir); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			exitWithCode(2)
			return nil
		}
		return nil
	}

	// Check fleet state for in-flight tasks
	inFlight := 0
	snap, snapErr := fleet.Snapshot(homeDir)
	if snapErr == nil {
		for _, ts := range snap.Tasks {
			if ts.Kind == "ship" || ts.Kind == "scout" {
				inFlight++
			}
		}
	}

	// No in-flight work -> safe to end turn
	if inFlight == 0 {
		if err := checkPendingRelayObligations(homeDir); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			exitWithCode(2)
			return nil
		}
		return nil
	}

	// Check watcher liveness
	status := lifecycle.ReadBeatStatus(homeDir, time.Now())

	// If watcher is healthy and not stale, allow the stop
	if status.Exists && !status.Stale {
		if err := checkPendingRelayObligations(homeDir); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			exitWithCode(2)
			return nil
		}
		return nil
	}

	// Blind turn: in-flight work + unhealthy watcher -> block the stop
	reason := "TURN WOULD END BLIND: "
	if !status.Exists {
		reason += "watcher never started"
	} else {
		reason += fmt.Sprintf("watcher beat stale by %v", status.Age.Round(time.Second))
	}
	reason += fmt.Sprintf(" with %d in-flight task(s)", inFlight)

	// Grok Stop hook: exit 2 + stderr reason (Grok Stop hooks are passive
	// but we still signal so munsu can detect the condition)
	fmt.Fprintln(os.Stderr, reason)
	exitWithCode(2)
	return nil // unreachable
}

// runGuardAgy implements the agy Stop hook guard.
// agy Stop hooks are active: stdout decision JSON gates the turn end.
// - fullyIdle=true: allow stop with {"decision":"allow"}
// - pending relay obligations: continue with {"decision":"continue","reason":"..."}
// - Blind turn: continue with {"decision":"continue","reason":"..."}
// - Healthy: allow stop with {"decision":"allow"}
// All paths exit 0 because agy gates on the stdout decision field, NOT exit code.
func runGuardAgy(homeDir string) error {
	// Read stdin JSON for fullyIdle
	fullyIdle := false
	data, err := io.ReadAll(os.Stdin)
	if err == nil {
		var payload map[string]interface{}
		if json.Unmarshal([]byte(strings.TrimSpace(string(data))), &payload) == nil {
			if idle, ok := payload["fullyIdle"].(bool); ok && idle {
				fullyIdle = true
			}
		}
	}

	// Obligation gate: check before any allow decision
	if err := checkPendingRelayObligations(homeDir); err != nil {
		continueJSON, _ := json.Marshal(map[string]interface{}{
			"decision": "continue",
			"reason":   err.Error(),
		})
		fmt.Fprintln(os.Stdout, string(continueJSON))
		return nil
	}

	// fullyIdle means the agent says it's completely finished — allow the stop
	if fullyIdle {
		allowJSON, _ := json.Marshal(map[string]interface{}{
			"decision": "allow",
		})
		fmt.Fprintln(os.Stdout, string(allowJSON))
		return nil
	}

	// Check scope: only guard primary checkouts
	cls := scope.Classify(homeDir)
	if cls.Err != nil || cls.Identity != scope.Primary {
		allowJSON, _ := json.Marshal(map[string]interface{}{
			"decision": "allow",
		})
		fmt.Fprintln(os.Stdout, string(allowJSON))
		return nil
	}

	// Check fleet state for in-flight tasks
	inFlight := 0
	snap, snapErr := fleet.Snapshot(homeDir)
	if snapErr == nil {
		for _, ts := range snap.Tasks {
			if ts.Kind == "ship" || ts.Kind == "scout" {
				inFlight++
			}
		}
	}

	// No in-flight work -> safe to end turn
	if inFlight == 0 {
		allowJSON, _ := json.Marshal(map[string]interface{}{
			"decision": "allow",
		})
		fmt.Fprintln(os.Stdout, string(allowJSON))
		return nil
	}

	// Check watcher liveness
	status := lifecycle.ReadBeatStatus(homeDir, time.Now())

	// If watcher is healthy and not stale, allow the stop
	if status.Exists && !status.Stale {
		allowJSON, _ := json.Marshal(map[string]interface{}{
			"decision": "allow",
		})
		fmt.Fprintln(os.Stdout, string(allowJSON))
		return nil
	}

	// Blind turn: in-flight work + unhealthy watcher -> continue the turn
	reason := "TURN WOULD END BLIND: "
	if !status.Exists {
		reason += "watcher never started"
	} else {
		reason += fmt.Sprintf("watcher beat stale by %v", status.Age.Round(time.Second))
	}
	reason += fmt.Sprintf(" with %d in-flight task(s)", inFlight)

	continueJSON, _ := json.Marshal(map[string]interface{}{
		"decision": "continue",
		"reason":   reason,
	})
	fmt.Fprintln(os.Stdout, string(continueJSON))
	return nil
}

// checkPendingRelayObligations checks for un-acked terminal receipts with material
// status. It is BLOCKING: returns an error if material relay is pending. Fail-closed
// on read errors. Checks both homeDir and MUNSU_PARENT_STATUS (captain home).
func checkPendingRelayObligations(homeDir string) error {
	// Collect homes to check: own home + parent home (for soldiers, receipts
	// live in captain-owned state under MUNSU_PARENT_STATUS).
	homes := []string{homeDir}
	if parentHome := os.Getenv("MUNSU_PARENT_STATUS"); parentHome != "" && parentHome != homeDir {
		homes = append(homes, parentHome)
	}

	for _, h := range homes {
		receipts, err := turnend.ListPendingReceipts(h)
		if err != nil {
			return fmt.Errorf("obligation gate fail-closed: reading terminal receipts from %s: %w", h, err)
		}
		for _, r := range receipts {
			has, err := turnend.MaterialReportExists(h, r.TaskID)
			if err != nil {
				return fmt.Errorf("obligation gate fail-closed: checking material report for task %s in %s: %w", r.TaskID, h, err)
			}
			if has {
				return fmt.Errorf("material relay pending: task %s has un-acked terminal receipt (state=%s) in %s; run 'munsu turnend obligations' or use --force", r.TaskID, r.State, h)
			}
		}
	}

	return nil
}

func newAfkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "afk",
		Short: "Enter away-mode supervision",
		Long: `Start the away-mode sub-supervisor daemon.

The daemon sets the AFK consent flag, acquires an identity lock,
and runs one wake-triage cycle. It then blocks until SIGTERM/SIGINT.
The flag and lock are cleaned up on stop.

Subcommands:
  drain      One General drain cycle: claim wakes, peek fleet, surface actionable
  return     Ordered AFK daemon shutdown with digest drain
  return check  Check if actionable AFK state remains`,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			var d afk.Daemon
			return d.Start(ctx.Home)
		}),
	}
	cmd.AddCommand(newAfkDrainCmd())
	cmd.AddCommand(newAfkReturnCmd())
	return cmd
}

func newAfkDrainCmd() *cobra.Command {
	var consumer string
	var leaseCaptains int
	var limit int
	var noPeek bool
	cmd := &cobra.Command{
		Use:   "drain",
		Short: "One General drain cycle: claim wakes, peek fleet, surface actionable",
		Long: `Claim signal wakes under a lease, classify each as actionable or routine,
and optionally peek the fleet for in-flight phase. Prints only actionable
wakes plus guidance so the General can steer without reading child chat.

Routines are counted, not enumerated, to reduce wake rot.
Ack claimed wakes after steering: munsu wake ack <lease-id> <event-id...>.`,
		Args: contractNoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if consumer == "" {
				return usageError("invalid_argument", "Run `munsu afk drain --consumer <id>`", "--consumer is required")
			}

			output, err := contractOutput(cmd)
			if err != nil {
				return err
			}

			report, err := afk.DrainCycle(afk.DrainCycleOptions{
				HomeDir:       ctx.Home,
				Consumer:      consumer,
				LeaseCaptains: leaseCaptains,
				Limit:         limit,
				PeekFleet:     !noPeek,
			})
			if err != nil {
				return operationError("internal", "Run `munsu afk drain --consumer "+consumer+"` again", err.Error())
			}

			if output == contract.OutputJSON {
				var actionable []string
				for _, w := range report.Actionable {
					actionable = append(actionable, fmt.Sprintf("[%s] %s: %s", w.EventID, w.Key, w.Payload))
				}
				state := "clean"
				if report.HasActionable() {
					state = "actionable"
				}
				var inFlight, dead int
				if report.FleetPeek != nil {
					inFlight = report.FleetPeek.InFlight
					dead = report.FleetPeek.Dead
				}
				return writeContract(cmd, contract.Response[contract.DrainCycle]{
					SchemaVersion: contract.SchemaVersion,
					Kind:          "afk.drain",
					Status:        "success",
					Data: contract.DrainCycle{
						ClaimID:      report.LeaseID,
						Consumer:     report.Consumer,
						Actionable:   actionable,
						RoutineCount: report.RoutineCount,
						Reclaimed:    report.Reclaimed,
						InFlight:     inFlight,
						Dead:         dead,
						Guidance:     report.Guidance,
						State:        state,
					},
				})
			}

			cmd.Println(report.String())
			return nil
		}),
	}
	configureContractCommand(cmd)
	cmd.Flags().StringVar(&consumer, "consumer", "", "Consumer identifier (required)")
	cmd.Flags().IntVar(&leaseCaptains, "lease-captains", 60, "Lease duration in seconds")
	cmd.Flags().IntVar(&limit, "limit", 10, "Maximum wakes to claim")
	cmd.Flags().BoolVar(&noPeek, "no-peek", false, "Skip the fleet peek")
	return cmd
}

func newAfkReturnCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "return",
		Short: "Perform ordered AFK daemon shutdown",
		Long: `Stop the AFK daemon, drain the durable digest queue,
and print a summary of escalations, wedge alarms, and blocked items.

Check exit code via 'munsu afk return check' — returns 0 when
no actionable AFK state remains.`,
		Args: cobra.NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			report, err := afk.Return(ctx.Home)
			if err != nil {
				return err
			}
			cmd.Println(report.String())
			return nil
		}),
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "check",
		Short: "Check if any actionable AFK state remains",
		Long: `Re-read the durable digest and exit 0 if clean,
non-zero if actionable items remain.`,
		Args: cobra.NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if !afk.IsClean(ctx.Home) {
				return fmt.Errorf("actionable AFK state remains — run 'munsu afk return' to reconcile")
			}
			return nil
		}),
	})
	return cmd
}
