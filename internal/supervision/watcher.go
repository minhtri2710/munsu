// Package supervision provides the event-driven watcher backbone.
package supervision

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/minhtri2710/munsu/internal/classify"
	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/soldierstate"
	"github.com/minhtri2710/munsu/internal/task"
)

const pollInterval = 5 * time.Second

// WakeReason describes an actionable watcher condition.
type WakeReason struct {
	Kind                 string // signal, stale, check, heartbeat
	TaskIDs              []string
	Message              string
	DemandDeepInspection bool // set after N consecutive stale polls for same task
}

// Run starts the persistent watcher daemon. It records actionable conditions in
// the durable wake queue and continues polling until SIGTERM or SIGINT.
func Run(homeDir string) (*WakeReason, error) {
	return run(homeDir, time.NewTicker, signalChannel())
}

func signalChannel() <-chan os.Signal {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	return sigCh
}

func run(homeDir string, newTicker func(time.Duration) *time.Ticker, sigCh <-chan os.Signal) (*WakeReason, error) {
	acquired, err := lifecycle.AcquireSession(homeDir)
	if err != nil {
		return nil, fmt.Errorf("watcher lock: %w", err)
	}
	if !acquired {
		return nil, fmt.Errorf("another watcher is already running")
	}
	defer lifecycle.ReleaseSession(homeDir)

	// Write watcher identity on start and clear it on exit.
	identity := NewIdentity(homeDir)
	if err := WriteIdentity(homeDir, identity); err != nil {
		return nil, fmt.Errorf("writing watcher identity: %w", err)
	}
	defer ClearIdentityIfMatches(homeDir, identity)

	lifecycle.WriteBeat(homeDir)
	ticker := newTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			return &WakeReason{Kind: "signal", Message: "watcher interrupted"}, nil
		case <-ticker.C:
			lifecycle.WriteBeat(homeDir)
			if _, err := runCycle(homeDir); err != nil {
				return nil, err
			}
		}
	}
}

// ArmBackground launches the watcher as a background process.
// If restart is true, signals any existing watcher first, using identity-based
// PID ownership validation to avoid signaling an unrelated process.
func ArmBackground(homeDir string, restart bool) error {
	if restart {
		if err := stopRunningWatcher(homeDir); err != nil {
			return err
		}
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding munsu binary: %w", err)
	}

	cmd := exec.Command(execPath, "watch")
	cmd.Dir = homeDir
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting watcher: %w", err)
	}

	fmt.Printf("Watcher armed (pid %d)\n", cmd.Process.Pid)
	return nil
}

// stopRunningWatcher signals the running watcher identified by beat + identity.
// Uses identity-based PID ownership validation to avoid signaling unrelated processes.
func stopRunningWatcher(homeDir string) error {
	_, pid, ok := lifecycle.ReadBeat(homeDir)
	if !ok || pid <= 0 {
		return nil // no watcher running
	}

	// Validate that this PID belongs to our watcher before signaling.
	if !ValidatePIDOwnership(homeDir, pid) {
		return fmt.Errorf("watcher pid %d ownership could not be verified; refusing to signal", pid)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		lifecycle.ClearBeat(homeDir)
		return nil
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("signaling watcher pid %d: %w", pid, err)
	}

	time.Sleep(500 * time.Millisecond)
	return nil
}

var (
	// staleStreaks tracks consecutive stale polls per task ID.
	// Persists across scanFleet calls within a process; protected for concurrent package use.
	staleStreaksMu sync.Mutex
	staleStreaks   = map[string]int{}

	// consecutiveStaleThreshold is the number of consecutive stale polls
	// before demanding deep inspection (3 polls * 5s = ~15s).
	consecutiveStaleThreshold = 3
)

// ScanFleet checks all live tasks for the first actionable condition.
func ScanFleet(homeDir string) *WakeReason {
	reasons := scanFleet(homeDir, false)
	if len(reasons) == 0 {
		return nil
	}
	return reasons[0]
}

func scanFleet(homeDir string, clearResolved bool) []*WakeReason {
	entries, err := os.ReadDir(filepath.Join(homeDir, "state"))
	if err != nil {
		return nil
	}

	var reasons []*WakeReason
	// Status-signal path: captain-relevant last lines (including Captain return-channel
	// files state/captain:<id>.status) wake General even when the pane is alive.
	seenStatus := map[string]bool{}
	for _, match := range classify.ScanGeneralRelevant(filepath.Join(homeDir, "state")) {
		seenStatus[match.TaskID] = true
		reasons = append(reasons, &WakeReason{
			Kind:    "signal",
			TaskIDs: []string{match.TaskID},
			Message: match.LastLine,
		})
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".meta") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".meta")
		if seenStatus[id] {
			// Status signal already actionable for this id; skip stale scan.
			continue
		}
		reason := scanTask(homeDir, id)
		if reason == nil {
			if clearResolved {
				clearWakeMarker(homeDir, id)
			}
			continue
		}
		reasons = append(reasons, reason)
	}
	// Clear status-only markers when last line is no longer captain-relevant
	// (e.g. Second wrote working: after a prior done:).
	if clearResolved {
		for id := range collectStatusIDs(filepath.Join(homeDir, "state")) {
			if seenStatus[id] {
				continue
			}
			hasReason := false
			for _, r := range reasons {
				if len(r.TaskIDs) > 0 && r.TaskIDs[0] == id {
					hasReason = true
					break
				}
			}
			if !hasReason {
				clearWakeMarker(homeDir, id)
			}
		}
	}
	return reasons
}

func collectStatusIDs(stateDir string) map[string]struct{} {
	out := map[string]struct{}{}
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".status") || strings.HasPrefix(name, ".") {
			continue
		}
		out[strings.TrimSuffix(name, ".status")] = struct{}{}
	}
	return out
}

func scanTask(homeDir, id string) *WakeReason {
	meta, err := task.ReadMeta(homeDir, id)
	if err != nil {
		return nil
	}
	windowID, hasWindow := meta["window"]
	if !hasWindow {
		return nil
	}

	taskBackend, _, err := session.BackendForTask(homeDir, meta)
	if err != nil {
		return nil
	}
	if !taskBackend.Alive(windowID) {
		if isStatusGeneralRelevant(homeDir, id) {
			return handleStale(id, fmt.Sprintf("pane %s is dead (general-relevant status)", windowID))
		}
		if isNoMistakesActive(homeDir, id) || isStatusPaused(homeDir, id) {
			resetStreak(id)
			return nil
		}
		return handleStale(id, fmt.Sprintf("pane %s is dead", windowID))
	}

	statusPath := filepath.Join(homeDir, "state", id+".status")
	if fi, err := os.Stat(statusPath); err == nil {
		age := time.Since(fi.ModTime())
		if age > lifecycle.StaleThreshold() {
			if isStatusGeneralRelevant(homeDir, id) {
				return handleStale(id, fmt.Sprintf("pane %s idle for %v (general-relevant status)", windowID, age.Round(time.Second)))
			}
			if isNoMistakesActive(homeDir, id) || isStatusPaused(homeDir, id) {
				resetStreak(id)
				return nil
			}
			return handleStale(id, fmt.Sprintf("pane %s idle for %v", windowID, age.Round(time.Second)))
		}
	}

	resetStreak(id)
	return nil
}

// RunCycle performs one durable scan/enqueue cycle with condition dedupe.
// It is the shared path used by the persistent daemon and `munsu watch run`.
func RunCycle(homeDir string) (bool, error) {
	return runCycle(homeDir)
}

func runCycle(homeDir string) (bool, error) {
	emitted := false
	for _, reason := range scanFleet(homeDir, true) {
		if len(reason.TaskIDs) == 0 {
			continue
		}
		id := reason.TaskIDs[0]
		fingerprint := wakeFingerprint(homeDir, reason)
		marker := wakeMarkerPath(homeDir, id)
		if data, err := os.ReadFile(marker); err == nil && string(data) == fingerprint {
			continue
		}
		if err := lifecycle.EnqueueWake(homeDir, reason.Kind, id, reason.Message); err != nil {
			return emitted, fmt.Errorf("enqueue watcher wake: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
			return emitted, err
		}
		if err := os.WriteFile(marker, []byte(fingerprint), 0644); err != nil {
			return emitted, err
		}
		emitted = true
	}
	return emitted, nil
}

func wakeFingerprint(homeDir string, reason *WakeReason) string {
	message := strings.TrimSuffix(reason.Message, "; demand-deep-inspection")
	status := ""
	if len(reason.TaskIDs) > 0 {
		if lines, err := task.ReadStatus(homeDir, reason.TaskIDs[0]); err == nil && len(lines) > 0 {
			status = lines[len(lines)-1]
		}
	}
	return reason.Kind + "\n" + message + "\n" + status
}

func wakeMarkerPath(homeDir, id string) string {
	safeID := strings.NewReplacer("/", "_", ":", "_", ".", "_").Replace(id)
	return filepath.Join(homeDir, "state", ".watcher-seen-"+safeID)
}

func clearWakeMarker(homeDir, id string) {
	_ = os.Remove(wakeMarkerPath(homeDir, id))
}

// handleStale creates a stale WakeReason with streak tracking.
// After consecutiveStaleThreshold consecutive stale polls for the same task,
// it marks the reason as demanding deep inspection.
func handleStale(id, msg string) *WakeReason {
	staleStreaksMu.Lock()
	staleStreaks[id]++
	count := staleStreaks[id]
	staleStreaksMu.Unlock()

	reason := &WakeReason{
		Kind:    "stale",
		TaskIDs: []string{id},
		Message: msg,
	}

	if count >= consecutiveStaleThreshold {
		reason.DemandDeepInspection = true
		reason.Message += "; demand-deep-inspection"
	}

	return reason
}

// resetStreak clears the stale streak counter for a task.
// Called when a task is provably working or its status changes.
func resetStreak(id string) {
	staleStreaksMu.Lock()
	delete(staleStreaks, id)
	staleStreaksMu.Unlock()
}

// isNoMistakesActive checks whether the task has an active no-mistakes
// run-step that indicates it is provably working. Tasks driving the
// no-mistakes pipeline (running, fixing, ci, fix_review, awaiting_approval)
// should not trigger stale wakes.
func isNoMistakesActive(homeDir, id string) bool {
	s, err := soldierstate.Read(homeDir, id)
	if err != nil {
		return false
	}
	return absorbStaleSignal(s)
}

// absorbStaleSignal returns true when the soldier state has an active
// no-mistakes run-step that should absorb a stale signal.
func absorbStaleSignal(s *soldierstate.State) bool {
	if s == nil {
		return false
	}
	switch s.NoMistakesRunStep {
	case "running", "fixing", "ci", "fix_review", "awaiting_approval":
		return true
	}
	return false
}

// isStatusPaused checks whether the task's last status line is a declared
// deliberate external-wait pause. Returns false if no status file exists.
func isStatusPaused(homeDir, id string) bool {
	lines, err := task.ReadStatus(homeDir, id)
	if err != nil || len(lines) == 0 {
		return false
	}
	return classify.IsPaused(lines[len(lines)-1])
}

// isStatusGeneralRelevant checks whether the task's last status line contains
// a general-relevant verb. Returns false if no status file exists.
func isStatusGeneralRelevant(homeDir, id string) bool {
	lines, err := task.ReadStatus(homeDir, id)
	if err != nil || len(lines) == 0 {
		return false
	}
	return classify.GeneralRelevant(lines[len(lines)-1])
}
