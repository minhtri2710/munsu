package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/captain"
	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/spf13/cobra"
)

// newWatchEnsureCmd creates the `munsu watch ensure` command.
// It returns started|attached|healthy|failed with identity, lease id, heartbeat age.
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
var startWatcherProcess = defaultStartWatcherProcess

var watcherBeaconTimeout = 3 * time.Second

func defaultStartWatcherProcess(homeDir string) (int, error) {
	execPath, err := os.Executable()
	if err != nil {
		return 0, err
	}

	cmd := exec.Command(execPath, "watch", "--home", homeDir)
	cmd.Dir = homeDir
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Env = append(os.Environ(), "MUNSU_HOME="+homeDir)
	configureWatchProcess(cmd)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	return cmd.Process.Pid, nil
}

// ensureWatcher checks the watcher state and starts one if needed.
func ensureWatcher(homeDir string, restart bool) contract.Response[contract.WatchEnsure] {
	beatStatus := lifecycle.ReadBeatStatus(homeDir, time.Now())

	// If restart requested, signal existing watcher using identity validation
	if restart && beatStatus.Exists {
		_, pid, ok := lifecycle.ReadBeat(homeDir)
		if ok && pid > 0 && orchestrator.ValidatePIDOwnership(homeDir, pid) {
			proc, err := os.FindProcess(pid)
			if err == nil {
				_ = signalWatchProcess(proc)
				time.Sleep(500 * time.Millisecond)
			}
		}
		beatStatus = lifecycle.ReadBeatStatus(homeDir, time.Now())
	}

	// A fresh beat is healthy only when its PID has validated ownership.
	if beatStatus.Exists && !beatStatus.Stale {
		_, pid, ok := lifecycle.ReadBeat(homeDir)
		if ok && pid > 0 && orchestrator.ValidatePIDOwnership(homeDir, pid) {
			return contract.Response[contract.WatchEnsure]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "watch.ensure",
				Status:        "success",
				Data: contract.WatchEnsure{
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
		return contract.Response[contract.WatchEnsure]{
			SchemaVersion: contract.SchemaVersion,
			Kind:          "watch.ensure",
			Status:        "success",
			Data: contract.WatchEnsure{
				WatchID: "",
				State:   "failed",
				Noop:    false,
			},
		}
	}

	// Poll until beat + identity ownership land, or timeout. A single short sleep
	// races the child process and caused false "started"/NEVER STARTED reports.
	afterStatus, validated := waitForWatcherBeacon(homeDir, pid, watcherBeaconTimeout)

	state := "started"
	if validated {
		state = "healthy"
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

	leaseInfo := &contract.WatchLeaseInfo{
		Identity:    identityStr,
		Heartbeat:   heartbeatAge,
		HeartbeatOK: validated && !afterStatus.Stale,
	}

	watchID := fmt.Sprintf("watch-%d", pid)
	if id != nil {
		watchID = fmt.Sprintf("watch-%d-v%s", id.PID, id.BuildVersion)
	}

	return contract.Response[contract.WatchEnsure]{
		SchemaVersion: contract.SchemaVersion,
		Kind:          "watch.ensure",
		Status:        "success",
		Data: contract.WatchEnsure{
			WatchID:  watchID,
			State:    state,
			Interval: "5s",
			Lease:    leaseInfo,
			Noop:     false,
		},
	}
}

// waitForWatcherBeacon polls until the watcher beat exists and identity ownership
// validates for pid, or until timeout. Returns the last beat status and whether
// ownership was validated.
func waitForWatcherBeacon(homeDir string, pid int, timeout time.Duration) (lifecycle.BeatStatus, bool) {
	deadline := time.Now().Add(timeout)
	var status lifecycle.BeatStatus
	for {
		status = lifecycle.ReadBeatStatus(homeDir, time.Now())
		if status.Exists && !status.Stale && orchestrator.ValidatePIDOwnership(homeDir, pid) {
			return status, true
		}
		if time.Now().After(deadline) {
			return status, status.Exists && orchestrator.ValidatePIDOwnership(homeDir, pid)
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
	_, pid, ok := lifecycle.ReadBeat(homeDir)
	if ok && pid > 0 {
		return fmt.Sprintf("watch-%d", pid)
	}
	return "watch-unknown"
}

// watcherLeaseInfo builds WatchLeaseInfo from the current beat and identity.
func watcherLeaseInfo(homeDir string) *contract.WatchLeaseInfo {
	beatStatus := lifecycle.ReadBeatStatus(homeDir, time.Now())
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

	return &contract.WatchLeaseInfo{
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
			emitted, err := orchestrator.RunCycleWithProbeAndSender(ctx.Home, runtimeTaskEndpointProbe(), newSessionMailboxSender(), captain.NewWatcherHooks(newSessionUplinkTransport(), newSessionActivationTransport()), fleetRetirementPort{})
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

			return writeContract(cmd, contract.Response[contract.WatchRun]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "watch.run",
				Status:        "success",
				Data: contract.WatchRun{
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

// countQueuedWakes returns the number of entries in the wake queue file.
func countQueuedWakes(homeDir string) int {
	data, err := os.ReadFile(lifecycle.QueuePath(homeDir))
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return 0
	}
	return len(lines)
}

// newWatchStopCmd creates the `munsu watch stop` command.
// It reads the watcher PID from the beat file, sends SIGTERM, and reports stopped.
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

// stopWatcher reads the watcher PID from the beat file, validates ownership
// when identity is available, sends SIGTERM, waits briefly, and reports the
// result. Idempotent: no running watcher is a no-op success.
//
// Ownership must be proven from the identity file; beat-only state is ambiguous.
func stopWatcher(homeDir string) contract.Response[contract.WatchStop] {
	_, pid, ok := lifecycle.ReadBeat(homeDir)

	// No watcher running — report already-stopped
	if !ok || pid <= 0 {
		return contract.Response[contract.WatchStop]{
			SchemaVersion: contract.SchemaVersion,
			Kind:          "watch.stop",
			Status:        "success",
			Data: contract.WatchStop{
				WatchID: "",
				PID:     0,
				State:   "already-stopped",
			},
		}
	}

	watchID := fmt.Sprintf("watch-%d", pid)

	if !orchestrator.ValidatePIDOwnership(homeDir, pid) {
		return contract.Response[contract.WatchStop]{
			SchemaVersion: contract.SchemaVersion,
			Kind:          "watch.stop",
			Status:        "success",
			Data: contract.WatchStop{
				WatchID: watchID,
				PID:     pid,
				State:   "identity-mismatch",
			},
		}
	}

	// Find and signal the process
	proc, err := os.FindProcess(pid)
	if err == nil {
		_ = signalWatchProcess(proc)
		time.Sleep(500 * time.Millisecond)
	}

	// Check if process is still alive
	alive := false
	if proc != nil {
		if processIsAlive(proc) {
			alive = true
		}
	}

	state := "stopped"
	if alive {
		state = "unresponsive"
	}

	lifecycle.ClearBeat(homeDir)
	orchestrator.ClearIdentity(homeDir)
	return contract.Response[contract.WatchStop]{
		SchemaVersion: contract.SchemaVersion,
		Kind:          "watch.stop",
		Status:        "success",
		Data: contract.WatchStop{
			WatchID: watchID,
			PID:     pid,
			State:   state,
		},
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
func evaluateWatcherStatus(homeDir string) contract.Response[contract.WatchStatus] {
	beatStatus := lifecycle.ReadBeatStatus(homeDir, time.Now())

	watchID := identifyWatcher(homeDir)
	var identity string
	var pid int

	if id := orchestrator.ReadIdentity(homeDir); id != nil {
		identity = orchestrator.IdentitySummary(id)
		pid = id.PID
	} else if _, beatPID, ok := lifecycle.ReadBeat(homeDir); ok {
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
			materialAge = time.Duration(oldest).Round(time.Second).String()
			// Aged material wakes make the guard unhealthy.
			if oldest > int64(5*time.Minute) && guardState == "healthy" {
				guardState = "unhealthy"
			}
		}
	}

	var diagnostics []string
	if !beatStatus.Exists {
		diagnostics = append(diagnostics, "Watcher never started — run 'munsu watch ensure'")
	} else if beatStatus.Stale {
		diagnostics = append(diagnostics, "Watcher beat stale — run 'munsu watch ensure --restart'")
	}
	if queuedWakes > 0 {
		diagnostics = append(diagnostics, fmt.Sprintf("Queued wakes: %d — drain with 'munsu wake-drain'", queuedWakes))
		if materialAge != "" {
			diagnostics = append(diagnostics, fmt.Sprintf("Oldest material wake age: %s", materialAge))
		}
	}

	lease := watcherLeaseInfo(homeDir)

	return contract.Response[contract.WatchStatus]{
		SchemaVersion: contract.SchemaVersion,
		Kind:          "watch.status",
		Status:        "success",
		Data: contract.WatchStatus{
			WatchID:     watchID,
			State:       state,
			BeatAge:     beatAge,
			PID:         pid,
			Identity:    identity,
			QueuedWakes: queuedWakes,
			MaterialAge: materialAge,
			GuardState:  guardState,
			Lease:       lease,
			Diagnostics: diagnostics,
		},
	}
}

// oldestMaterialWakeAge returns the age in seconds of the oldest material wake
// (wake with done/failed/needs-decision/blocked payload). Returns 0 if no
// material wakes are found.
func oldestMaterialWakeAge(homeDir string) int64 {
	data, err := os.ReadFile(lifecycle.QueuePath(homeDir))
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return 0
	}
	now := time.Now().Unix()
	var oldest int64
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 5 {
			continue
		}
		payload := parts[4]
		// Check for material states: done, failed, needs-decision, blocked
		if strings.HasPrefix(payload, "done:") || strings.HasPrefix(payload, "failed:") ||
			strings.HasPrefix(payload, "needs-decision:") || strings.HasPrefix(payload, "blocked:") ||
			strings.Contains(payload, "done:") || strings.Contains(payload, "failed:") ||
			strings.Contains(payload, "needs-decision:") || strings.Contains(payload, "blocked:") {
			var epoch int64
			if _, err := fmt.Sscanf(parts[0], "%d", &epoch); err == nil && epoch > 0 {
				age := now - epoch
				if age > oldest {
					oldest = age
				}
			}
		}
	}
	return oldest
}
