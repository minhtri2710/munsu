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
	"github.com/minhtri2710/munsu/internal/mailbox"
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
	DemandDeepInspection bool // set after stale-by-idle-seconds threshold for same task
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
	acquired, err := lifecycle.AcquireWatch(homeDir)
	if err != nil {
		return nil, fmt.Errorf("watcher lock: %w", err)
	}
	if !acquired {
		return nil, fmt.Errorf("another watcher is already running")
	}
	defer lifecycle.ReleaseWatch(homeDir)

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

// startWatcherProcess is an unexported seam for tests. Production uses
// defaultStartWatcher. Tests can substitute it to verify the identity
// clearance contract without spawning a real daemon.
var startWatcherProcess = defaultStartWatcher

func defaultStartWatcher(homeDir string) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding munsu binary: %w", err)
	}

	cmd := exec.Command(execPath, "watch", "--home", homeDir)
	cmd.Dir = homeDir
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Env = append(os.Environ(), "MUNSU_HOME="+homeDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting watcher: %w", err)
	}

	fmt.Printf("Watcher armed (pid %d)\n", cmd.Process.Pid)
	return nil
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

	// Clear stale identity before launching the new watcher so that
	// handshake polling never reads stale state (e.g. old CommitSHA)
	// before the subprocess writes its own identity.
	ClearIdentity(homeDir)

	return startWatcherProcess(homeDir)
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

// Stop signals the running watcher for the given home and clears its beat.
// Uses identity-based PID ownership validation to avoid signaling unrelated processes.
// Idempotent: returns nil when no watcher is running.
func Stop(homeDir string) error {
	return stopRunningWatcher(homeDir)
}

var (
	// staleFirstSeen tracks the first time a task was continuously stale.
	// Persists across scanFleet calls within a process; protected for concurrent package use.
	staleFirstSeenMu sync.Mutex
	staleFirstSeen   = map[string]time.Time{}

	// staleByIdleThreshold is the wall-clock duration a task must be continuously stale
	// before demanding deep inspection (~15 seconds, matching firstmate's idle-seconds timer).
	staleByIdleThreshold = 15 * time.Second

	// pauseResurfaceThreshold is the max duration a paused task is absorbed
	// before it surfaces as stale. After this threshold, a paused task
	// triggers a stale wake so the general can reassess it.
	pauseResurfaceThreshold = 5 * time.Minute
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
	// Captains are idle-by-default. Parent
	// supervision uses captain-relevant status signals, not pane-idle stale.
	if meta["kind"] == "captain" {
		resetStreak(id)
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
	paneAlive := taskBackend.Alive(windowID)
	if !paneAlive {
		if isStatusGeneralRelevant(homeDir, id) {
			// Signal path already covers general-relevant; if we still reach
			// here (race), surface once with a stable message.
			return handleStale(id, fmt.Sprintf("pane %s is dead (general-relevant status)", windowID))
		}
		if shouldAbsorbStale(homeDir, id, false) {
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
				return handleStale(id, fmt.Sprintf("pane %s idle beyond threshold (general-relevant status)", windowID))
			}
			if shouldAbsorbStale(homeDir, id, true) {
				resetStreak(id)
				return nil
			}
			// Stable message (no wall-clock age) so wake fingerprints dedupe.
			return handleStale(id, fmt.Sprintf("pane %s idle beyond threshold", windowID))
		}
	}

	resetStreak(id)
	return nil
}

// TerminalReconcileHook is a recovery-only hook for terminal receipt retry.
// It is called ONCE when the watcher starts, not every cycle.
// Set by the captain package during init.
var TerminalReconcileHook func(homeDir string) error

// recoveryDone tracks whether the one-shot recovery has completed.
var recoveryDone bool

// RunCycle performs one durable scan/enqueue cycle with condition dedupe.
// It is the shared path used by the persistent daemon and `munsu watch run`.
func RunCycle(homeDir string) (bool, error) {
	return runCycle(homeDir)
}

// runRecovery executes the one-shot recovery on watcher startup.
// It retries pending inbox envelopes once with fingerprint dedup,
// runs the legacy terminal reconcile hook (if any) once,
// then completes any pending poll retirements.
func runRecovery(homeDir string) {
	if recoveryDone {
		return
	}
	recoveryDone = true

	// Recovery step 1: retry pending inbox envelopes via mailbox recovery.
	attempts, err := mailbox.RecoverAllInboxes(homeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mailbox recovery error: %v\n", err)
	} else {
		for _, a := range attempts {
			if a.Err != nil {
				fmt.Fprintf(os.Stderr, "mailbox recovery: %s: %v\n", a.MessageID, a.Err)
			} else if a.Delivered {
				fmt.Fprintf(os.Stderr, "mailbox recovery: %s: delivered\n", a.MessageID)
			}
		}
	}

	// Recovery step 2: run legacy terminal reconcile hook once.
	// This catches any remaining turnend receipts that were not migrated
	// to the new mailbox format.
	if TerminalReconcileHook != nil {
		if err := TerminalReconcileHook(homeDir); err != nil {
			fmt.Fprintf(os.Stderr, "terminal reconcile recovery: %v\n", err)
		}
	}
}

func runCycle(homeDir string) (bool, error) {
	// Snapshot recovery state before the call — prevents double invocation
	// of TerminalReconcileHook on cycle 1 (recovery handles startup).
	recoveryWasDone := recoveryDone

	// Run one-shot recovery on first cycle.
	runRecovery(homeDir)

	// Retirement recovery: scan pending records every cycle.
	// This handles crashes during the merged-PR retirement sequence.
	// Runs before check discovery so recovered tasks produce wake signals.
	resolved, recErrs := RecoverAllPendingRetirements(homeDir)
	if resolved > 0 || len(recErrs) > 0 {
		if resolved > 0 {
			fmt.Fprintf(os.Stderr, "poll retirement recovery: %d resolved\n", resolved)
		}
		for _, re := range recErrs {
			fmt.Fprintf(os.Stderr, "poll retirement recovery error (preserving artifacts): %v\n", re)
		}
	}

	// Per-cycle terminal receipt reconciliation (cycles after startup only;
	// startup recovery already handles the first cycle via runRecovery).
	// Catches receipts that arrive after the one-shot startup recovery
	// (e.g., soldier finishes mid-cycle). Uses the same
	// TerminalReconcileHook as startup recovery — the underlying
	// ReconcileTerminalReceipts is idempotent (skips already-acked
	// receipts), so calling it every cycle is safe and produces
	// exactly-once relay.
	// On error, the error is logged and the cycle continues (bounded
	// failure) — partial failure must not falsely ack/close obligations.
	if recoveryWasDone && TerminalReconcileHook != nil {
		if err := TerminalReconcileHook(homeDir); err != nil {
			fmt.Fprintf(os.Stderr, "terminal reconcile cycle: %v\n", err)
		}
	}

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

	// Discover and emit check plugin wakes (per-task .check files + global checks).
	// These cover PR merge polls and custom checks registered under state/checks/.
	checks, err := DiscoverAllChecks(homeDir)
	if err != nil {
		return emitted, err
	}
	for _, plugin := range checks {
		// Validate the check artifact before surfacing
		if err := ValidateCheck(plugin.Path); err != nil {
			continue
		}
		// Migrate-or-refuse: skip if stale
		if migrated, err := MigrateOrRefuseStale(plugin.Path); err != nil {
			if !migrated {
				os.Remove(plugin.Path)
			}
			continue
		}

		checkID := plugin.Label
		msg := fmt.Sprintf("check ready: %s", plugin.Label)
		if plugin.Kind == CheckPerTask {
			// Include PR URL in message if available
			if meta, err := task.ReadMeta(homeDir, plugin.Label); err == nil {
				if prURL, ok := meta["pr_url"]; ok && prURL != "" {
					msg = fmt.Sprintf("PR poll ready for task %s: %s", plugin.Label, prURL)
				}
			}

			// Attempt crash-safe retirement for merged polls.
			// Uses ValidateCheckWithLstat for symlink rejection.
			// On success, the poll is removed and a durable status line
			// is published. The check wake is NOT emitted — the status
			// scan will surface it as a signal wake on the next cycle.
			if err := ValidateCheckWithLstat(plugin.Path); err == nil {
				if retireErr := RetireMergedPoll(homeDir, plugin.Label, plugin.Path); retireErr == nil {
					// Poll retired successfully. Skip wake emission;
					// the status signal path will surface the publication.
					continue
				}
				// Retirement failed for a non-crash reason:
				// open/unmerged/closed PR, provider error, or digest mismatch.
				// Fall through to normal check wake emission.
			}
		}

		fingerprint := "check\n" + msg
		marker := wakeMarkerPath(homeDir, "check:"+checkID)
		if data, err := os.ReadFile(marker); err == nil && string(data) == fingerprint {
			continue
		}
		if err := lifecycle.EnqueueWake(homeDir, "check", checkID, msg); err != nil {
			return emitted, fmt.Errorf("enqueue check wake: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
			return emitted, err
		}
		if err := os.WriteFile(marker, []byte(fingerprint), 0644); err != nil {
			return emitted, err
		}
		emitted = true
	}

	// Mailbox inbox recovery is handled in runRecovery, called once at
	// startup. No per-cycle routing of pending mailbox envelopes or
	// diagnostics. General never requires parent-home. Pending mailbox
	// counts are visible through health checks and status queries, not
	// watcher diagnostic wakes.
	// Per-cycle terminal receipt reconciliation runs above (see comment),
	// sharing the same TerminalReconcileHook with startup recovery.
	// The watcher is recovery-only for pending envelope delivery.
	// Normal rank-aware communication goes directly via mailbox.SendReport.

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

// handleStale creates a stale WakeReason with idle-seconds tracking.
// After the task has been stale for staleByIdleThreshold (wall-clock time,
// not poll count), it marks the reason as demanding deep inspection.
func handleStale(id, msg string) *WakeReason {
	staleFirstSeenMu.Lock()
	first, exists := staleFirstSeen[id]
	if !exists {
		staleFirstSeen[id] = time.Now()
		first = staleFirstSeen[id]
	}
	staleSince := time.Since(first)
	staleFirstSeenMu.Unlock()

	reason := &WakeReason{
		Kind:    "stale",
		TaskIDs: []string{id},
		Message: msg,
	}

	if staleSince >= staleByIdleThreshold {
		reason.DemandDeepInspection = true
		reason.Message += "; demand-deep-inspection"
	}

	return reason
}

// resetStreak clears the stale-first-seen timestamp for a task.
// Called when a task is provably working or its status changes.
func resetStreak(id string) {
	staleFirstSeenMu.Lock()
	delete(staleFirstSeen, id)
	staleFirstSeenMu.Unlock()
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

// shouldAbsorbStale reports whether a stale condition is benign.
// paneAlive gates status-only "working" absorb: a dead pane with a leftover
// working: line is still actionable; an alive idle pane with working: is healthy.
// A paused task beyond the resurface threshold is NOT absorbed — it surfaces as stale.
func shouldAbsorbStale(homeDir, id string, paneAlive bool) bool {
	if isNoMistakesActive(homeDir, id) {
		return true
	}
	// Check pause status: absorb only if within the resurface threshold.
	if isStatusPaused(homeDir, id) {
		return !isPausedBeyondResurface(homeDir, id)
	}
	if !paneAlive {
		return false
	}
	switch classify.AbsorbClass(id, filepath.Join(homeDir, "state")) {
	case classify.Working, classify.Paused:
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

// isPausedBeyondResurface reports whether a paused task has been paused
// longer than the resurface threshold and should surface as stale.
// Uses the status file's modification time as a proxy for pause duration.
func isPausedBeyondResurface(homeDir, id string) bool {
	statusPath := filepath.Join(homeDir, "state", id+".status")
	fi, err := os.Stat(statusPath)
	if err != nil {
		return false
	}
	return time.Since(fi.ModTime()) > pauseResurfaceThreshold
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
