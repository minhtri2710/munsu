package cli

import (
	"fmt"
	"time"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/spf13/cobra"
)

// newWatchEnsureCmd creates the `munsu watch ensure` command.
// It returns attached|healthy|failed with identity, lease id, heartbeat age.
func newWatchEnsureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ensure",
		Short: "Ensure the watcher is running (idempotent)",
		Args:  contractNoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			restart, _ := cmd.Flags().GetBool("restart")

			if _, err := contractOutput(cmd); err != nil {
				return err
			}

			result := ensureWatcher(ctx.Home, restart)
			return writeContract(cmd, result)
		}),
	}
	configureContractCommand(cmd)
	cmd.Flags().Bool("restart", false, "Restart watcher if running")
	return cmd
}

// startWatcherProcess is a test seam for the detached watcher launch.
var startWatcherProcess = orchestrator.StartWatcher

var watcherBeaconTimeout = 3 * time.Second

// watcherEvictAfter is the beat age past which a holder is treated as stalled
// and sent TERM. It is twice the 300s stale threshold, and the stale threshold
// already covers 60 poll intervals: a live watcher's beat lags by one cycle,
// whose longest bounded step is the 5 minute worktree command timeout
// (backendWorktreeCommandTimeout), so 10 minutes leaves a slow cycle on a
// loaded host unevicted. No measured beat-lag record exists in the tree; the
// bound is derived from those timeouts, not observed.
var watcherEvictAfter = 2 * orchestrator.StaleThreshold()

// ensureWatcher checks the watcher state and starts one if needed.
func ensureWatcher(homeDir string, restart bool) Response[WatchEnsure] {
	beatStatus := orchestrator.ReadBeatStatus(homeDir, time.Now())

	// A refused stop falls through: a watcher still alive keeps its beat and
	// identity, so the ownership check below attaches to it, and one whose
	// ownership is unproven is never signalled. StopWatcher targets only the
	// pid the beat names, after the identity file proves it.
	stalled := beatStatus.Exists && beatStatus.Age > watcherEvictAfter
	if (restart && beatStatus.Exists) || stalled {
		_, _ = orchestrator.StopWatcher(homeDir)
		beatStatus = orchestrator.ReadBeatStatus(homeDir, time.Now())
	}

	// A fresh beat is healthy only when its PID has validated ownership.
	if beatStatus.Exists && !beatStatus.Stale {
		_, pid, ok := orchestrator.ReadBeat(homeDir)
		if ok && pid > 0 && orchestrator.ValidatePIDOwnership(homeDir, pid) {
			return Response[WatchEnsure]{
				SchemaVersion: SchemaVersion,
				Kind:          "watch.ensure",
				Status:        "success",
				Data: WatchEnsure{
					WatchID:  identifyWatcher(homeDir),
					State:    "attached",
					Interval: "5s",
					Lease:    watcherLeaseInfo(homeDir),
					Noop:     true,
				},
			}
		}
	}

	// Start the watcher.
	pid, err := startWatcherProcess(homeDir)
	if err != nil {
		return Response[WatchEnsure]{
			SchemaVersion: SchemaVersion,
			Kind:          "watch.ensure",
			Status:        "error",
			Data: WatchEnsure{
				WatchID: "",
				State:   "failed",
				Noop:    false,
			},
		}
	}

	// Poll until beat + identity ownership land, or timeout. A single short sleep
	// races the child process and caused false "started"/NEVER STARTED reports.
	afterStatus, validated := waitForWatcherBeacon(homeDir, pid, watcherBeaconTimeout)

	if !validated {
		return Response[WatchEnsure]{
			SchemaVersion: SchemaVersion,
			Kind:          "watch.ensure",
			Status:        "error",
			Data:          WatchEnsure{State: "failed"},
			Help:          []string{fmt.Sprintf("watcher pid %d never wrote a beat with proven ownership within %s; a stalled holder may still own the watch lock", pid, watcherBeaconTimeout)},
		}
	}

	heartbeatAge := ""
	if afterStatus.Exists {
		heartbeatAge = afterStatus.Age.Round(time.Second).String()
	}

	// Read identity for identity-aware lease info
	id := orchestrator.ReadIdentity(homeDir)
	identityStr := fmt.Sprintf("pid:%d", pid)
	if id != nil {
		identityStr = fmt.Sprintf("pid:%d version=%s proto=%d", id.PID, id.BuildVersion, id.ProtocolVersion)
	}

	leaseInfo := &WatchLeaseInfo{
		Identity:    identityStr,
		Heartbeat:   heartbeatAge,
		HeartbeatOK: true,
	}

	watchID := fmt.Sprintf("watch-%d", pid)
	if id != nil {
		watchID = fmt.Sprintf("watch-%d-v%s", id.PID, id.BuildVersion)
	}

	return Response[WatchEnsure]{
		SchemaVersion: SchemaVersion,
		Kind:          "watch.ensure",
		Status:        "success",
		Data: WatchEnsure{
			WatchID:  watchID,
			State:    "healthy",
			Interval: "5s",
			Lease:    leaseInfo,
			Noop:     false,
		},
	}
}

// waitForWatcherBeacon polls until the watcher beat exists and identity ownership
// validates for pid, or until timeout. Returns the last beat status and whether
// ownership was validated.
func waitForWatcherBeacon(homeDir string, pid int, timeout time.Duration) (orchestrator.BeatStatus, bool) {
	deadline := time.Now().Add(timeout)
	var status orchestrator.BeatStatus
	for {
		status = orchestrator.ReadBeatStatus(homeDir, time.Now())
		if status.Exists && !status.Stale && orchestrator.ValidatePIDOwnership(homeDir, pid) {
			return status, true
		}
		if time.Now().After(deadline) {
			return status, false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// identifyWatcher generates a watcher identity string. Prefers the persisted
// identity file over PID-only inference.
func identifyWatcher(homeDir string) string {
	if id := orchestrator.ReadIdentity(homeDir); id != nil {
		return fmt.Sprintf("watch-%d-v%s", id.PID, id.BuildVersion)
	}
	_, pid, ok := orchestrator.ReadBeat(homeDir)
	if ok && pid > 0 {
		return fmt.Sprintf("watch-%d", pid)
	}
	return "watch-unknown"
}

// watcherLeaseInfo builds WatchLeaseInfo from the current beat and identity.
func watcherLeaseInfo(homeDir string) *WatchLeaseInfo {
	beatStatus := orchestrator.ReadBeatStatus(homeDir, time.Now())
	heartbeatAge := ""
	if beatStatus.Exists {
		heartbeatAge = beatStatus.Age.Round(time.Second).String()
	}

	identityStr := ""
	validated := false
	if id := orchestrator.ReadIdentity(homeDir); id != nil {
		identityStr = fmt.Sprintf("pid:%d version=%s proto=%d", id.PID, id.BuildVersion, id.ProtocolVersion)
		validated = orchestrator.ValidatePIDOwnership(homeDir, id.PID)
	}

	return &WatchLeaseInfo{
		Identity:    identityStr,
		Heartbeat:   heartbeatAge,
		HeartbeatOK: validated && beatStatus.Exists && !beatStatus.Stale,
	}
}

// newWatchRunCmd creates the `munsu watch run` command.
// It runs one poll cycle of the watcher and emits normalized output.
func newWatchRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run one watcher poll cycle with contract output",
		Args:  contractNoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if _, err := contractOutput(cmd); err != nil {
				return err
			}

			wakesBefore := countQueuedWakes(ctx.Home)

			// One diagnostic cycle: a delivery recovery gate failure fails the
			// command before the cycle runs.
			if err := orchestrator.DeliveryRecoveryGate(ctx.Home, fleetDeliveryRecoveryPort{}, cmd.ErrOrStderr()); err != nil {
				return err
			}

			retirementPort := fleetRetirementPort{compose: func(h string) (*taskauthority.Canonical, error) { return ctx.TaskAuthorityFor(h) }}
			emitted, err := orchestrator.RunCycleWithProbeAndSender(ctx.Home, runtimeTaskEndpointProbe(), newSessionMailboxSender(), watcherHooks(), retirementPort, fleetReviewVerdictPort{}, fleetCheckValidationPort{}, runtimeTaskStatePort{})
			if err != nil {
				return err
			}
			wakesAfter := countQueuedWakes(ctx.Home)
			wakesEmitted := 0
			if emitted {
				wakesEmitted = wakesAfter - wakesBefore
				if wakesEmitted < 0 {
					wakesEmitted = 0
				}
			}

			return writeContract(cmd, Response[WatchRun]{
				SchemaVersion: SchemaVersion,
				Kind:          "watch.run",
				Status:        "success",
				Data: WatchRun{
					WatchID:        identifyWatcher(ctx.Home),
					State:          "completed",
					WakesScanned:   wakesBefore,
					WakesEmitted:   wakesEmitted,
					EventsObserved: 1,
				},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

// fleetDeliveryRecoveryPort resumes pending delivery journals through Fleet.
type fleetDeliveryRecoveryPort struct{}

func (fleetDeliveryRecoveryPort) RecoverDeliveries(homeDir string) error {
	return fleet.RecoverDeliveryJournals(homeDir)
}

// countQueuedWakes returns the number of decodable wake records in the queue,
// process-event wakes included.
func countQueuedWakes(homeDir string) int {
	records, _ := home.PeekWakes(homeDir)
	return len(records)
}

// newWatchStopCmd creates the `munsu watch stop` command.
// It stops the watcher named by beat + identity and reports what it observed.
func newWatchStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the watcher (idempotent)",
		Args:  contractNoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if _, err := contractOutput(cmd); err != nil {
				return err
			}

			result := stopWatcher(ctx.Home)
			return writeContract(cmd, result)
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

// stopWatcher shapes orchestrator.StopWatcher as the watch.stop contract.
// An ownership refusal is a successful no-op; a refused signal is an error.
func stopWatcher(homeDir string) Response[WatchStop] {
	stop, err := orchestrator.StopWatcher(homeDir)
	status := "success"
	var help []string
	if err != nil && stop.State != orchestrator.StopIdentityMismatch {
		status = "error"
		help = []string{err.Error()}
	}
	watchID := ""
	if stop.PID > 0 {
		watchID = fmt.Sprintf("watch-%d", stop.PID)
	}
	return Response[WatchStop]{
		SchemaVersion: SchemaVersion,
		Kind:          "watch.stop",
		Status:        status,
		Data: WatchStop{
			WatchID: watchID,
			PID:     stop.PID,
			State:   string(stop.State),
		},
		Help: help,
	}
}

// newWatchStatusCmd creates the `munsu watch status` command.
// It returns a bounded watcher health/status reading that never enters
// foreground daemon mode — pure stateless read of beat, identity, and wake state.
func newWatchStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Bounded watcher health status (never enters daemon mode)",
		Args:  contractNoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if _, err := contractOutput(cmd); err != nil {
				return err
			}

			result := evaluateWatcherStatus(ctx.Home)
			return writeContract(cmd, result)
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

// evaluateWatcherStatus builds a bounded watcher status from beat, identity,
// and wake queue state. Never enters daemon mode; pure stateless read.
func evaluateWatcherStatus(homeDir string) Response[WatchStatus] {
	beatStatus := orchestrator.ReadBeatStatus(homeDir, time.Now())

	watchID := identifyWatcher(homeDir)
	var identity string
	var pid int

	if id := orchestrator.ReadIdentity(homeDir); id != nil {
		identity = orchestrator.IdentitySummary(id)
		pid = id.PID
	} else if _, beatPID, ok := orchestrator.ReadBeat(homeDir); ok {
		pid = beatPID
	}

	beatAge := ""
	state := "healthy"
	guardState := "healthy"
	if beatStatus.Exists {
		beatAge = beatStatus.Age.Round(time.Second).String()
		if beatStatus.Stale {
			state = "stale"
			guardState = "unhealthy"
		}
	} else {
		state = "absent"
		guardState = "unhealthy"
	}

	queuedWakes := countQueuedWakes(homeDir)

	// Detect oldest material wake age.
	materialAge := ""
	if queuedWakes > 0 {
		if oldest := oldestMaterialWakeAge(homeDir); oldest > 0 {
			oldestDuration := time.Duration(oldest) * time.Second
			materialAge = oldestDuration.Round(time.Second).String()
			// Aged material wakes make the guard unhealthy.
			if oldestDuration > orchestrator.MaterialWakeAgeThreshold && guardState == "healthy" {
				guardState = "unhealthy"
			}
		}
	}

	// Claimed wakes are in neither queue; an unreadable lease directory is
	// unknown, never none.
	claimedWakes, claimedErr := orchestrator.ListClaimedWakes(homeDir, time.Now())
	var claimedExpired, claimedStalled int
	for _, c := range claimedWakes {
		switch c.State {
		case home.ClaimExpired:
			claimedExpired++
		case home.ClaimStalled:
			claimedStalled++
			// A stalled material wake makes the guard unhealthy, as an aged queued one does.
			if c.Material && guardState == "healthy" {
				guardState = "unhealthy"
			}
		}
	}
	if claimedErr != nil && guardState == "healthy" {
		guardState = "indeterminate"
	}

	var diagnostics []string
	if !beatStatus.Exists {
		diagnostics = append(diagnostics, "Watcher never started — run 'munsu watch ensure'")
	} else if beatStatus.Stale {
		diagnostics = append(diagnostics, "Watcher beat stale — run 'munsu watch ensure --restart'")
	}
	if queuedWakes > 0 {
		diagnostics = append(diagnostics, fmt.Sprintf("Queued wakes: %d — claim with 'munsu wake claim'", queuedWakes))
		if materialAge != "" {
			diagnostics = append(diagnostics, fmt.Sprintf("Oldest material wake age: %s", materialAge))
		}
	}

	if claimedErr != nil {
		diagnostics = append(diagnostics, fmt.Sprintf("Claimed wakes: unknown — %v", claimedErr))
	} else if len(claimedWakes) > 0 {
		diagnostics = append(diagnostics, fmt.Sprintf("Claimed wakes: %d held (%d expired, %d stalled)", len(claimedWakes), claimedExpired, claimedStalled))
	}

	lease := watcherLeaseInfo(homeDir)

	return Response[WatchStatus]{
		SchemaVersion: SchemaVersion,
		Kind:          "watch.status",
		Status:        "success",
		Data: WatchStatus{
			WatchID:        watchID,
			State:          state,
			BeatAge:        beatAge,
			PID:            pid,
			Identity:       identity,
			QueuedWakes:    queuedWakes,
			ClaimedWakes:   len(claimedWakes),
			ClaimedStalled: claimedStalled,
			MaterialAge:    materialAge,
			GuardState:     guardState,
			Lease:          lease,
			Diagnostics:    diagnostics,
		},
	}
}

// oldestMaterialWakeAge returns the age in seconds of the oldest material wake
// (wake with done/failed/needs-decision/blocked payload). Returns 0 if no
// material wakes are found.
func oldestMaterialWakeAge(homeDir string) int64 {
	epoch, ok := orchestrator.OldestMaterialWake(homeDir)
	if !ok {
		return 0
	}
	return max(time.Now().Unix()-epoch, 0)
}
