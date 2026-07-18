package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/supervision"
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

// ensureWatcher checks the watcher state and starts one if needed.
func ensureWatcher(homeDir string, restart bool) contract.Response[contract.WatchEnsure] {
	beatStatus := lifecycle.ReadBeatStatus(homeDir, time.Now())

	// If restart requested, signal existing watcher
	if restart && beatStatus.Exists {
		_, pid, ok := lifecycle.ReadBeat(homeDir)
		if ok && pid > 0 {
			proc, err := os.FindProcess(pid)
			if err == nil {
				proc.Signal(syscall.SIGTERM)
				time.Sleep(500 * time.Millisecond)
			}
		}
		beatStatus = lifecycle.ReadBeatStatus(homeDir, time.Now())
	}

	// If watcher is running and healthy, return attached
	if beatStatus.Exists && !beatStatus.Stale {
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

	// Start the watcher
	execPath, err := os.Executable()
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

	cmd := exec.Command(execPath, "watch")
	cmd.Dir = homeDir
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
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

	pid := cmd.Process.Pid
	// Wait briefly for the watcher to write its beat
	time.Sleep(200 * time.Millisecond)
	afterStatus := lifecycle.ReadBeatStatus(homeDir, time.Now())

	state := "started"
	if afterStatus.Exists {
		state = "healthy"
	}

	heartbeatAge := ""
	if afterStatus.Exists {
		heartbeatAge = afterStatus.Age.Round(time.Second).String()
	}

	leaseInfo := &contract.WatchLeaseInfo{
		Identity:    fmt.Sprintf("pid:%d", pid),
		Heartbeat:   heartbeatAge,
		HeartbeatOK: afterStatus.Exists && !afterStatus.Stale,
	}

	return contract.Response[contract.WatchEnsure]{
		SchemaVersion: contract.SchemaVersion,
		Kind:          "watch.ensure",
		Status:        "success",
		Data: contract.WatchEnsure{
			WatchID:  fmt.Sprintf("watch-%d", pid),
			State:    state,
			Interval: "5s",
			Lease:    leaseInfo,
			Noop:     false,
		},
	}
}

// identifyWatcher generates a watcher identity from the beat file.
func identifyWatcher(homeDir string) string {
	_, pid, ok := lifecycle.ReadBeat(homeDir)
	if ok && pid > 0 {
		return fmt.Sprintf("watch-%d", pid)
	}
	return "watch-unknown"
}

// watcherLeaseInfo builds WatchLeaseInfo from the current beat.
func watcherLeaseInfo(homeDir string) *contract.WatchLeaseInfo {
	_, pid, ok := lifecycle.ReadBeat(homeDir)
	if !ok {
		return nil
	}
	beatStatus := lifecycle.ReadBeatStatus(homeDir, time.Now())
	heartbeatAge := beatStatus.Age.Round(time.Second).String()

	return &contract.WatchLeaseInfo{
		Identity:    fmt.Sprintf("pid:%d", pid),
		Heartbeat:   heartbeatAge,
		HeartbeatOK: !beatStatus.Stale,
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
			emitted, err := supervision.RunCycle(ctx.Home)
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

// stopWatcher reads the watcher PID from the beat file, sends SIGTERM,
// waits briefly, and reports the result. Idempotent: no running watcher
// is a no-op success.
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

	// Find and signal the process
	proc, err := os.FindProcess(pid)
	if err == nil {
		proc.Signal(syscall.SIGTERM)
		time.Sleep(500 * time.Millisecond)
	}

	// Check if process is still alive
	alive := false
	if proc != nil {
		if err := proc.Signal(syscall.Signal(0)); err == nil {
			alive = true
		}
	}

	state := "stopped"
	if alive {
		state = "unresponsive"
	}

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
