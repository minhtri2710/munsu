package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/spf13/cobra"
)

var recoverBriefHandoffs = fleet.RecoverTaskHandoffs
var writeBriefArtifact = func(auth *taskauthority.Canonical, id string, write func() error) error {
	return auth.WriteTaskDataArtifactByID(id, write)
}

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

			// One canonical aggregate read serves mode resolution, the
			// existence gate, and the scout contract below. A canonical task
			// that fails to read is fatal: brief never falls back to an
			// alternate mode source on a failed read (fail closed).
			auth, err := ctx.TaskAuthority()
			if err != nil {
				return err
			}
			taskIDValue, err := domain.NewTaskID(id)
			if err != nil {
				return err
			}
			var agg taskauthority.Aggregate
			canonicalExists := false
			if a, getErr := auth.Get(taskIDValue); getErr == nil {
				agg = a
				canonicalExists = true
			} else if !errors.Is(getErr, taskauthority.ErrNotFound) {
				return getErr
			}

			// yolo stays registry-owned.
			projYolo := false
			if _, y, err := fleet.Mode(ctx.Home, repo); err == nil {
				projYolo = y
			}

			// Delivery mode is the canonical DeliveryContract's mode whenever
			// this home's canonical record carries one (recorded once at first
			// spawn, thereafter READ — taskauthority.DeliveryContract). Only
			// when the owning home records no contract is the mode resolved
			// from the typed project/base surface. An explicit --mode that
			// contradicts a recorded contract fails closed rather than
			// silently re-scaffolding under a different mode than the one the
			// task delivers under. The contract owns the mode, but the project
			// snapshot still gates existence and well-formedness (fail closed
			// on an unknown project or malformed base/overlay).
			var resolvedMode string
			if canonicalExists && agg.DeliveryContract != nil {
				if modeFlag != "" {
					if err := fleet.ValidateDeliveryMode(modeFlag); err != nil {
						return err
					}
				}
				resolvedMode = agg.DeliveryContract.Mode
				if modeFlag != "" && modeFlag != resolvedMode {
					return fmt.Errorf("--mode %q contradicts task %q's recorded delivery contract (%q): brief reads the contract and never re-scaffolds it; re-record the mode with 'munsu spawn %s --mode %s'", modeFlag, id, resolvedMode, id, modeFlag)
				}
				if err := fleet.ValidateProjectSnapshot(ctx.Home, repo); err != nil {
					return err
				}
			} else {
				resolvedMode, err = fleet.ResolveDeliveryModeFromProject(ctx.Home, repo, modeFlag)
				if err != nil {
					return err
				}
			}

			// Require existing canonical task or legacy task meta unless --force.
			if !force && !canonicalExists {
				if _, err := mhome.ReadMeta(ctx.Home, id); err != nil {
					return fmt.Errorf("task %q not found: create it with 'munsu task add %s ...' or use --force", id, id)
				}
			}

			scoutScope, scoutBudget := "", int64(0)
			var scoutGeneration taskauthority.Generation
			if scout {
				if !canonicalExists {
					return fmt.Errorf("reading scout contract: %w", taskauthority.ErrNotFound)
				}
				if agg.Definition.Kind != "scout" {
					return fmt.Errorf("task %q is not a scout", id)
				}
				scoutScope = agg.Definition.ScoutScope
				scoutBudget = agg.Definition.ScoutRuntimeBudgetSecs
				scoutGeneration = agg.Generation
			}
			opts := fleet.ScaffoldOptions{
				HomeDir: ctx.Home, ID: id, Repo: repo, Scout: scout,
				Mode: resolvedMode, Yolo: projYolo,
				ScoutScope: scoutScope, ScoutRuntimeBudgetSecs: scoutBudget,
				Generation: scoutGeneration,
			}

			if err := recoverBriefHandoffs(ctx.Home); err != nil {
				return err
			}
			if err := writeBriefArtifact(auth, id, func() error { return fleet.Scaffold(opts) }); err != nil {
				return err
			}

			kind := "ship"
			if scout {
				kind = "scout"
			}

			var b strings.Builder
			b.WriteString(fmt.Sprintf("Brief scaffolded at %s\n", fleet.Path(ctx.Home, id)))
			b.WriteString(fmt.Sprintf("  id:    %s\n", id))
			b.WriteString(fmt.Sprintf("  repo:  %s\n", repo))
			b.WriteString(fmt.Sprintf("  kind:  %s\n", kind))
			if resolvedMode != "" {
				b.WriteString(fmt.Sprintf("  mode:  %s\n", resolvedMode))
			}
			if projYolo {
				b.WriteString("  yolo:  true\n")
			}

			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "brief",
				Status:        "success",
				Data:          MessageResult{Message: strings.TrimSpace(b.String())},
			})
		}),
	}
	configureContractCommand(cmd)

	cmd.Flags().BoolVar(&scout, "scout", false, "Generate a scout brief instead of ship brief")
	cmd.Flags().BoolVar(&force, "force", false, "Scaffold brief without requiring existing task meta")
	cmd.Flags().StringVar(&modeFlag, "mode", "", "Delivery mode override (no-mistakes|direct-PR|local-only)")

	return cmd
}

func sessionRuntimeIdentityContract(id *bootstrap.RuntimeIdentity) *RuntimeIdentityContract {
	if id == nil {
		return nil
	}
	out := &RuntimeIdentityContract{
		ProtocolVersion: id.ProtocolVersion,
		RunningExecutable: ExecutableIdentityContract{
			Path:   id.RunningExecutable.Path,
			Digest: id.RunningExecutable.Digest,
			Error:  id.RunningExecutable.Error,
		},
		PATHExecutable: ExecutableIdentityContract{
			Path:   id.PATHExecutable.Path,
			Digest: id.PATHExecutable.Digest,
			Error:  id.PATHExecutable.Error,
		},
		Build: BuildProvenanceContract{
			CLIVersion:    id.Build.CLIVersion,
			ModulePath:    id.Build.ModulePath,
			ModuleVersion: id.Build.ModuleVersion,
			VCSRevision:   id.Build.VCSRevision,
			VCSTime:       id.Build.VCSTime,
			VCSModified:   id.Build.VCSModified,
			Available:     id.Build.Available,
		},
		Skew: sessionSkewContract(id),
	}
	for _, checkout := range id.SourceCheckouts {
		out.SourceCheckouts = append(out.SourceCheckouts, sourceCheckoutContract(checkout))
	}
	if id.Watcher != nil {
		watcher := watcherRuntimeContract(*id.Watcher)
		out.Watcher = &watcher
	}
	for _, captain := range id.Captains {
		capContract := CaptainRuntimeContract{ID: captain.ID, Home: captain.Home}
		if captain.SourceCheckout != nil {
			checkout := sourceCheckoutContract(*captain.SourceCheckout)
			capContract.SourceCheckout = &checkout
		}
		if captain.Watcher != nil {
			watcher := watcherRuntimeContract(*captain.Watcher)
			capContract.Watcher = &watcher
		}
		out.Captains = append(out.Captains, capContract)
	}
	for _, integration := range id.Integrations {
		out.Integrations = append(out.Integrations, IntegrationRuntimeContract{
			Harness:        integration.Harness,
			Scope:          string(integration.Scope),
			State:          integration.State,
			Version:        integration.Version,
			ManifestPath:   integration.ManifestPath,
			ManifestSchema: integration.ManifestSchema,
			ContentDigest:  integration.ContentDigest,
			Drifted:        integration.Drifted,
			Message:        integration.Message,
			Remediation:    integration.Remediation,
		})
	}
	return out
}

func sourceCheckoutContract(checkout bootstrap.SourceCheckoutIdentity) SourceCheckoutContract {
	return SourceCheckoutContract{
		Path:     checkout.Path,
		Revision: checkout.Revision,
		Dirty:    checkout.Dirty,
		Error:    checkout.Error,
	}
}

func watcherRuntimeContract(watcher bootstrap.WatcherRuntimeIdentity) WatcherRuntimeContract {
	return WatcherRuntimeContract{
		Component:        watcher.Component,
		Home:             watcher.Home,
		Executable:       watcher.Executable,
		ExecutableDigest: watcher.ExecutableDigest,
		BuildVersion:     watcher.BuildVersion,
		ProtocolVersion:  watcher.ProtocolVersion,
		CommitSHA:        watcher.CommitSHA,
		Running:          watcher.Running,
	}
}

func sessionSkewContract(id *bootstrap.RuntimeIdentity) []RuntimeSkewContract {
	if id == nil || len(id.Skew) == 0 {
		return nil
	}
	out := make([]RuntimeSkewContract, 0, len(id.Skew))
	for _, finding := range id.Skew {
		out = append(out, RuntimeSkewContract{
			Classification: string(finding.Classification),
			Component:      finding.Component,
			Detail:         finding.Detail,
			Remediation:    finding.Remediation,
		})
	}
	return out
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
			if output == OutputJSON {
				w = io.Discard
			}

			// --recover flag or MUNSU_SESSION_RECOVER env opts in captain relaunch.
			wantRecover := recover
			if _, ok := os.LookupEnv("MUNSU_SESSION_RECOVER"); ok {
				wantRecover = true
			}

			result, err := bootstrap.RunSessionStartWithWatcher(w, ctx.Home, func(home string) bootstrap.WatchEnsureResult {
				r := ensureWatcher(home, false)
				return bootstrap.WatchEnsureResult{State: r.Data.State}
			}, func(home string, doRecover bool) bootstrap.CaptainLivenessResult {
				return captainLivenessForSession(home, doRecover && wantRecover)
			}, taskDataDirReclaimer(ctx.Home))
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

			return writeContract(cmd, Response[SessionStart]{
				SchemaVersion: SchemaVersion,
				Kind:          "session.start",
				Status:        "success",
				Data: SessionStart{
					Lock:            lockState,
					Watcher:         watcherState,
					BootstrapOK:     result.Bootstrap != nil,
					FleetSyncOK:     result.FleetSync != nil,
					RuntimeIdentity: sessionRuntimeIdentityContract(result.RuntimeIdentity),
					Message:         "Session started. Lock: " + lockState + ". Watcher: " + watcherState + ".",
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
			locked := orchestrator.IsSessionLocked(ctx.Home)
			var installTools []string
			if len(args) > 1 && args[0] == "install" {
				installTools = args[1:]
			}
			result, err := bootstrap.Run(ctx.Home, !locked, installTools, taskDataDirReclaimer(ctx.Home))
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

			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "bootstrap",
				Status:        "success",
				Data:          MessageResult{Message: strings.TrimSpace(b.String())},
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
			retirementPort := fleetRetirementPort{compose: func(h string) (*taskauthority.Canonical, error) { return ctx.TaskAuthorityFor(h) }}
			reason, err := orchestrator.RunWithProbeSenderAndEvents(ctx.Home, runtimeTaskEndpointProbe(), newSessionMailboxSender(), watcherHooks(), retirementPort, fleetCheckValidationPort{}, runtimeTaskStatePort{}, runtimeObservationEventPort())
			if err != nil {
				return err
			}
			message := "watcher stopped"
			if reason != nil {
				message = fmt.Sprintf("stopped: %s — %s", reason.Kind, reason.Message)
			}
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "watch",
				Status:        "success",
				Data:          MessageResult{Message: message},
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
	cmd.AddCommand(newWatchStatusCmd())

	return cmd
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
	cls := fleet.Classify(homeDir)
	if cls.Err != nil || cls.Identity != fleet.Primary {
		if err := checkPendingRelayObligations(homeDir); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			exitWithCode(2)
			return nil
		}
		return nil
	}

	// Check fleet state for in-flight tasks
	inFlight, guardErr := guardInFlight(homeDir)
	if guardErr != nil {
		fmt.Fprintln(os.Stderr, "cannot read authoritative fleet state:", guardErr)
		exitWithCode(2)
		return nil
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
	status := orchestrator.ReadBeatStatus(homeDir, time.Now())

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
	cls := fleet.Classify(homeDir)
	if cls.Err != nil || cls.Identity != fleet.Primary {
		if err := checkPendingRelayObligations(homeDir); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			exitWithCode(2)
			return nil
		}
		return nil
	}

	// Check fleet state for in-flight tasks
	inFlight, guardErr := guardInFlight(homeDir)
	if guardErr != nil {
		fmt.Fprintln(os.Stderr, "cannot read authoritative fleet state:", guardErr)
		exitWithCode(2)
		return nil
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
	status := orchestrator.ReadBeatStatus(homeDir, time.Now())

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
	cls := fleet.Classify(homeDir)
	if cls.Err != nil || cls.Identity != fleet.Primary {
		if err := checkPendingRelayObligations(homeDir); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			exitWithCode(2)
			return nil
		}
		return nil
	}

	// Check fleet state for in-flight tasks
	inFlight, guardErr := guardInFlight(homeDir)
	if guardErr != nil {
		fmt.Fprintln(os.Stderr, "cannot read authoritative fleet state:", guardErr)
		exitWithCode(2)
		return nil
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
	status := orchestrator.ReadBeatStatus(homeDir, time.Now())

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
	cls := fleet.Classify(homeDir)
	if cls.Err != nil || cls.Identity != fleet.Primary {
		allowJSON, _ := json.Marshal(map[string]interface{}{
			"decision": "allow",
		})
		fmt.Fprintln(os.Stdout, string(allowJSON))
		return nil
	}

	// Check fleet state for in-flight tasks
	inFlight, guardErr := guardInFlight(homeDir)
	if guardErr != nil {
		fmt.Fprintln(os.Stderr, "cannot read authoritative fleet state:", guardErr)
		exitWithCode(2)
		return nil
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
	status := orchestrator.ReadBeatStatus(homeDir, time.Now())

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
		receipts, err := orchestrator.ListPendingReceipts(h)
		if err != nil {
			return fmt.Errorf("obligation gate fail-closed: reading terminal receipts from %s: %w", h, err)
		}
		for _, r := range receipts {
			has, err := orchestrator.MaterialReportExists(h, r.TaskID)
			if err != nil {
				return fmt.Errorf("obligation gate fail-closed: checking material report for task %s in %s: %w", r.TaskID, h, err)
			}
			if has {
				return fmt.Errorf("material relay pending: task %s has un-acked terminal receipt (state=%s) in %s; re-run 'munsu report %s <msg>' for that task to close the handoff, or use --force", r.TaskID, r.State, h, r.State)
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
			var d orchestrator.Daemon
			return d.Start(ctx.Home)
		}),
	}
	cmd.AddCommand(newAfkDrainCmd())
	cmd.AddCommand(newAfkReturnCmd())
	return cmd
}

func newAfkDrainCmd() *cobra.Command {
	var consumer string
	var leaseSeconds int
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

			report, err := orchestrator.DrainCycle(orchestrator.DrainCycleOptions{
				HomeDir:      ctx.Home,
				Consumer:     consumer,
				LeaseSeconds: leaseSeconds,
				Limit:        limit,
				PeekFleet:    !noPeek,
				FleetSnapshot: func(homeDir string) ([]orchestrator.FleetTaskSnapshot, error) {
					snap, err := fleet.Snapshot(homeDir, snapshotDeps())
					if err != nil {
						return nil, err
					}
					var list []orchestrator.FleetTaskSnapshot
					for _, t := range snap.Tasks {
						list = append(list, orchestrator.FleetTaskSnapshot{
							ID:         t.ID,
							Kind:       t.Kind,
							LastStatus: t.LastStatus,
							Window:     t.Window,
						})
					}
					return list, nil
				},
			})
			if err != nil {
				return operationError("internal", "Run `munsu afk drain --consumer "+consumer+"` again", err.Error())
			}

			if output == OutputJSON {
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
				return writeContract(cmd, Response[DrainCycle]{
					SchemaVersion: SchemaVersion,
					Kind:          "orchestrator.drain",
					Status:        "success",
					Data: DrainCycle{
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
	cmd.Flags().IntVar(&leaseSeconds, "lease-seconds", 60, "Lease duration in seconds")
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
			report, err := orchestrator.Return(ctx.Home)
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
			if !orchestrator.IsClean(ctx.Home) {
				return fmt.Errorf("actionable AFK state remains — run 'munsu afk return' to reconcile")
			}
			return nil
		}),
	})
	return cmd
}
