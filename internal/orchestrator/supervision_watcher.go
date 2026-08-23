// Package supervision provides the event-driven watcher backbone.
package orchestrator

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

const watcherPollInterval = 5 * time.Second

const (
	// watcherStopWait bounds how long stopRunningWatcher observes a signalled
	// watcher, and watcherStopPoll is how often it looks. See waitForWatcherExit
	// for why the bound expiring is not a failure.
	watcherStopWait = 2 * time.Second
	watcherStopPoll = 20 * time.Millisecond
)

// WakeReason describes an actionable watcher condition.
type WakeReason struct {
	Kind                 string // signal, stale, check, heartbeat
	TaskIDs              []string
	Message              string
	DemandDeepInspection bool // set after stale-by-idle-seconds threshold for same task
}

// Run starts the persistent watcher daemon. It records actionable conditions in
// the durable wake queue and continues polling until SIGTERM or SIGINT.
type TaskEndpointProbe interface {
	Probe(homeDir string, meta map[string]string) (bool, error)
}

type WatcherHooks interface {
	Reconcile(homeDir string, startup bool) error
	Activate(homeDir string)
}

type ObservedTaskState struct {
	Status, Description, NoMistakesRunStep string
	PaneAlive                              bool
}
type TaskStatePort interface {
	ReadTaskState(homeDir, taskID string) (*ObservedTaskState, error)
}

// RetirementPort retires the poll artifact of a task whose PR has merged.
// RetireMergedPoll owns validation of the artifact it is given: it must apply
// the check-validation rule to checkPath before it queries, records or mutates
// anything, and report a refusal as domain.ErrCheckValidationRefused. That is
// why the watcher does not validate again on its behalf — the guard nearest
// the destructive act is the only one that can close the window, and a copy in
// the caller would be a second disposition to keep in step for no gain.
type RetirementPort interface {
	RecoverPendingRetirements(homeDir string) (int, []error)
	RetireMergedPoll(homeDir, taskID, checkPath string) error
}

// CheckValidationPort validates a check artifact before the watcher surfaces it.
// Retirement validates its own artifact at the destructive boundary through
// RetirementPort; this port is separate because the watcher must still validate
// checks that will never be retired. Supplying an implementation asserts the
// capability exists, so a returned error can only mean "this artifact is
// invalid" — skip it and keep scanning. The capability being absent is a
// different failure and is expressed differently: a nil check validation port
// fails the whole cycle, loudly, instead of quietly emitting no checks at all.
type CheckValidationPort interface {
	ValidateCheck(path string) error
}

// RunWithProbeSenderAndEvents starts the persistent watcher and also runs the
// bounded native observation event lane (BEO-17/P1b): a validated event hint
// triggers an immediate re-probe cycle; every other outcome keeps the polling
// ticker as the cadence authority (the watcher is never silent). A nil observation
// event port keeps the watcher on pure polling; a nil check-validation port is refused as a
// fatal cycle capability error.
func RunWithProbeSenderAndEvents(homeDir string, probe TaskEndpointProbe, sender BoundSender, hooks WatcherHooks, retirement RetirementPort, checks CheckValidationPort, states TaskStatePort, events ObservationEventPort) (*WakeReason, error) {
	return run(homeDir, time.NewTicker, signalChannel(), probe, sender, hooks, retirement, checks, states, events)
}

func signalChannel() <-chan os.Signal {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	return sigCh
}

func run(homeDir string, newTicker func(time.Duration) *time.Ticker, sigCh <-chan os.Signal, probe TaskEndpointProbe, sender BoundSender, hooks WatcherHooks, retirement RetirementPort, checks CheckValidationPort, states TaskStatePort, events ObservationEventPort) (*WakeReason, error) {
	acquired, err := AcquireWatch(homeDir)
	if err != nil {
		return nil, fmt.Errorf("watcher lock: %w", err)
	}
	if !acquired {
		return nil, fmt.Errorf("another watcher is already running")
	}
	defer ReleaseWatch(homeDir)

	// Claim the watcher lease — unique per home.
	pid := os.Getpid()
	claimed, err := home.ClaimWatcherLease(homeDir, pid)
	if err != nil {
		return nil, fmt.Errorf("claiming watcher lease: %w", err)
	}
	if !claimed {
		return nil, fmt.Errorf("watcher lease already held by another process")
	}
	defer home.ReleaseWatcherLeaseIfMatches(homeDir, pid)

	// Write watcher identity on start and clear it on exit.
	identity := NewIdentity(homeDir)
	if err := WriteIdentity(homeDir, identity); err != nil {
		return nil, fmt.Errorf("writing watcher identity: %w", err)
	}
	defer ClearIdentityIfMatches(homeDir, identity)

	WriteBeat(homeDir)
	ticker := newTicker(watcherPollInterval)
	defer ticker.Stop()

	// Optional native event lane (BEO-17/P1b): bounded event wait feeding
	// validated wake hints; every other outcome is absorbed by the lane so
	// the polling ticker remains the cadence authority and the watcher is
	// never silent. A closed lane (no event surface, degraded reader) keeps
	// pure polling.
	var eventPulses <-chan eventPulse
	if events != nil {
		stopLane := make(chan struct{})
		defer close(stopLane)
		eventPulses = startEventLane(homeDir, NewEventWaiter(events), stopLane)
	}

	for {
		select {
		case <-sigCh:
			return &WakeReason{Kind: "signal", Message: "watcher interrupted"}, nil
		case <-ticker.C:
			WriteBeat(homeDir)
			obs := newCycleObservation()
			if _, err := runCycleWithProbeAndSender(homeDir, probe, sender, hooks, retirement, checks, states, obs); err != nil {
				return nil, err
			}
			logCycleObservation(obs)
		case pulse, ok := <-eventPulses:
			if !ok {
				// Event lane closed (no native surface / degraded): keep pure
				// polling for the rest of the run.
				eventPulses = nil
				continue
			}
			if pulse.outcome == EventWaitSignal {
				// Event-to-reprobe (BEO-17/P1b): a native wake hint only
				// triggers an immediate re-probe cycle; the cycle re-probes the
				// exact binding before any recovery/relaunch/dispose decision.
				// The hint itself is never lifecycle truth and never sets a
				// Task phase.
				WriteBeat(homeDir)
				obs := newCycleObservation()
				if _, err := runCycleWithProbeAndSender(homeDir, probe, sender, hooks, retirement, checks, states, obs); err != nil {
					return nil, err
				}
				logCycleObservation(obs)
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
	configureWatcherProcess(cmd)

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
	_, pid, ok := ReadBeat(homeDir)
	if !ok || pid <= 0 {
		return nil // no watcher running
	}

	// Validate that this PID belongs to our watcher before signaling.
	if !ValidatePIDOwnership(homeDir, pid) {
		return fmt.Errorf("watcher pid %d ownership could not be verified; refusing to signal", pid)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		ClearBeat(homeDir)
		return nil
	}

	if err := signalWatcherProcess(proc); err != nil {
		return fmt.Errorf("signaling watcher pid %d: %w", pid, err)
	}

	waitForWatcherExit(pid)
	return nil
}

// waitForWatcherExit observes the signalled watcher until it is gone or the
// bound expires. It exists for one reason: ArmBackground's restart path starts
// a replacement whose own AcquireWatch takes the watch flock, and that flock is
// still held while the old process runs.
//
// Expiry is not an error here because the one caller that restarts
// (completeHandshake, the only caller of ArmBackground(restart=true)) proves
// convergence itself via waitForNewWatcher; the other reachable caller, Stop,
// starts no replacement. A caller without that handshake would lose the "no
// watcher at all" signal entirely — that is the condition to re-read before
// adding one. Nothing durable depends on the old watcher being gone by now
// either: home.ReleaseWatcherLeaseIfMatches refuses to delete a successor's
// lease, and a watcher inside a slow cycle can outlast any bound we pick.
func waitForWatcherExit(pid int) {
	deadline := time.Now().Add(watcherStopWait)
	for time.Now().Before(deadline) {
		if !isProcessAlive(pid) {
			return
		}
		time.Sleep(watcherStopPoll)
	}
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

func scanFleetWithProbe(homeDir string, clearResolved bool, probe TaskEndpointProbe, states TaskStatePort, obs *cycleObservation) []*WakeReason {
	entries, err := os.ReadDir(filepath.Join(homeDir, "state"))
	if err != nil {
		return nil
	}

	var reasons []*WakeReason
	// Status-signal path: captain-relevant last lines (including Captain return-channel
	// files state/captain:<id>.status) wake General even when the pane is alive.
	seenStatus := map[string]bool{}
	_, captainHomeErr := os.Stat(filepath.Join(homeDir, home.CaptainProvenanceMarkerName))
	isCaptainHome := captainHomeErr == nil
	for _, match := range home.ScanGeneralRelevant(filepath.Join(homeDir, "state")) {
		if isCaptainHome {
			if meta, err := home.ReadMeta(homeDir, match.TaskID); err == nil && meta["kind"] == "captain" {
				continue
			}
		}
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
		id, err := home.ReverseDurableKey(strings.TrimSuffix(entry.Name(), ".meta"))
		if err != nil {
			continue
		}
		if seenStatus[id] {
			// Status signal already actionable for this id; skip stale scan.
			continue
		}
		if obs != nil {
			obs.scannedTasks++
		}
		reason := scanTaskWithProbe(homeDir, id, probe, states)
		if reason == nil {
			if clearResolved {
				clearWakeMarker(homeDir, id)
			}
			continue
		}
		if obs != nil && reason.Kind == "stale" {
			// Measured stale age is an internal observation only; it is never
			// written into WakeReason.Message or a fingerprint input, so a still-
			// stale task's fingerprint (and its duplicate suppression) is stable.
			obs.staleByTask[id] = staleAge(id, obs.now())
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
		id, err := home.ReverseDurableKey(strings.TrimSuffix(name, ".status"))
		if err != nil {
			continue
		}
		out[id] = struct{}{}
	}
	return out
}

func scanTaskWithProbe(homeDir, id string, probe TaskEndpointProbe, states TaskStatePort) *WakeReason {
	meta, err := home.ReadMeta(homeDir, id)
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

	if probe == nil {
		return &WakeReason{Kind: "check", TaskIDs: []string{id}, Message: "endpoint probe is not configured"}
	}
	paneAlive, err := probe.Probe(homeDir, meta)
	if err != nil {
		return &WakeReason{Kind: "check", TaskIDs: []string{id}, Message: fmt.Sprintf("endpoint probe failed: %v", err)}
	}
	if !paneAlive {
		if isStatusGeneralRelevant(homeDir, id) {
			// Signal path already covers general-relevant; if we still reach
			// here (race), surface once with a stable message.
			return handleStale(id, fmt.Sprintf("pane %s is dead (general-relevant status)", windowID))
		}
		if shouldAbsorbStale(homeDir, id, false, states) {
			resetStreak(id)
			return nil
		}
		return handleStale(id, fmt.Sprintf("pane %s is dead", windowID))
	}

	if statusPath, err := home.StatusFilePath(homeDir, id); err == nil {
		if fi, err := os.Stat(statusPath); err == nil {
			age := time.Since(fi.ModTime())
			if age > StaleThreshold() {
				if isStatusGeneralRelevant(homeDir, id) {
					return handleStale(id, fmt.Sprintf("pane %s idle beyond threshold (general-relevant status)", windowID))
				}
				if shouldAbsorbStale(homeDir, id, true, states) {
					resetStreak(id)
					return nil
				}
				// Stable message (no wall-clock age) so wake fingerprints dedupe.
				return handleStale(id, fmt.Sprintf("pane %s idle beyond threshold", windowID))
			}
		}
	}

	resetStreak(id)
	return nil
}

// recoveryDone tracks one-shot recovery independently for each watched home.
var recoveryDone sync.Map

// RunCycle performs one durable scan/enqueue cycle with condition dedupe.
// It is the shared path used by the persistent daemon and `munsu watch run`;
// both paths capture and log the cycle's internal observations.
func RunCycleWithProbeAndSender(homeDir string, probe TaskEndpointProbe, sender BoundSender, hooks WatcherHooks, retirement RetirementPort, checks CheckValidationPort, states TaskStatePort) (bool, error) {
	obs := newCycleObservation()
	emitted, err := runCycleWithProbeAndSender(homeDir, probe, sender, hooks, retirement, checks, states, obs)
	if err == nil {
		logCycleObservation(obs)
	}
	return emitted, err
}

// runRecovery executes the one-shot recovery on watcher startup.
// It retries pending inbox envelopes once with fingerprint dedup,
// runs the mailbox recovery hook (if any) once,
// then completes any pending poll retirements.
func runRecovery(homeDir string, sender BoundSender, hooks WatcherHooks) error {
	if sender == nil {
		return fmt.Errorf("mailbox recovery sender capability is required")
	}
	if hooks == nil {
		return fmt.Errorf("watcher hooks capability is required")
	}
	if _, loaded := recoveryDone.LoadOrStore(homeDir, true); loaded {
		return nil
	}

	// Recovery step 1: retry pending inbox envelopes via mailbox recovery.
	attempts, err := RecoverAllInboxesWithSender(sender, homeDir)
	if err != nil {
		recoveryDone.Delete(homeDir)
		return fmt.Errorf("mailbox recovery: %w", err)
	} else {
		for _, a := range attempts {
			if a.Err != nil {
				fmt.Fprintf(os.Stderr, "mailbox recovery: %s: %v\n", a.MessageID, a.Err)
			} else if a.Delivered {
				fmt.Fprintf(os.Stderr, "mailbox recovery: %s: delivered\n", a.MessageID)
			}
		}
	}

	// Recovery step 2: reconcile mailbox uplinks once.
	if err := hooks.Reconcile(homeDir, true); err != nil {
		fmt.Fprintf(os.Stderr, "mailbox reconcile recovery: %v\n", err)
	}
	return nil
}

// staleAge returns the measured duration a task has been continuously stale,
// from the first continuous stale detection. It is an INTERNAL observation:
// the value is never written into WakeReason.Message or any fingerprint input,
// so duplicate suppression is preserved (see cycleObservation).
func staleAge(id string, now time.Time) time.Duration {
	staleFirstSeenMu.Lock()
	defer staleFirstSeenMu.Unlock()
	first, ok := staleFirstSeen[id]
	if !ok {
		return 0
	}
	return now.Sub(first)
}

func runCycleWithProbeAndSender(homeDir string, probe TaskEndpointProbe, sender BoundSender, hooks WatcherHooks, retirement RetirementPort, checks CheckValidationPort, states TaskStatePort, obs *cycleObservation) (bool, error) {
	// A watcher with no way to validate a check artifact cannot decide anything
	// about one, and skipping every check would make that look like "no checks
	// are ready". Fail the cycle instead: the capability is required, and its
	// absence is not a per-check outcome.
	if checks == nil {
		return false, fmt.Errorf("check validation capability is required")
	}

	// Snapshot recovery state before the call — prevents double invocation
	// of the mailbox reconcile hook on cycle 1 (recovery handles startup).
	_, recoveryWasDone := recoveryDone.Load(homeDir)

	// Run one-shot recovery on first cycle for this home.
	if err := runRecovery(homeDir, sender, hooks); err != nil {
		return false, err
	}

	// Retirement recovery: scan pending records every cycle.
	// This handles crashes during the merged-PR retirement sequence.
	// Runs before check discovery so recovered tasks produce wake signals.
	resolved, recErrs := retirement.RecoverPendingRetirements(homeDir)
	if resolved > 0 || len(recErrs) > 0 {
		if resolved > 0 {
			fmt.Fprintf(os.Stderr, "poll retirement recovery: %d resolved\n", resolved)
		}
		for _, re := range recErrs {
			fmt.Fprintf(os.Stderr, "poll retirement recovery error (preserving artifacts): %v\n", re)
		}
	}

	// Per-cycle mailbox uplink recovery (cycles after startup only;
	// startup recovery already handles the first cycle via runRecovery).
	// The underlying ReconcileCaptainHook recovery is idempotent, so
	// calling it every cycle is safe. On error, the error is logged and
	// the cycle continues (bounded failure).
	if recoveryWasDone {
		if err := hooks.Reconcile(homeDir, false); err != nil {
			fmt.Fprintf(os.Stderr, "mailbox reconcile cycle: %v\n", err)
		}
	}

	// Per-cycle captain agent activation on receipt discovery.
	// After startup recovery (cycle 1), each subsequent cycle checks
	// for new soldier receipts that haven't yet triggered an activation
	// nudge to the captain agent pane. Idempotent: already-seen receipts
	// are skipped via durable markers. Nil hook = no-op.
	if recoveryWasDone {
		hooks.Activate(homeDir)
	}

	emitted := false
	for _, reason := range scanFleetWithProbe(homeDir, true, probe, states, obs) {
		if len(reason.TaskIDs) == 0 {
			continue
		}
		id := reason.TaskIDs[0]
		fingerprint := wakeFingerprint(homeDir, reason)
		marker := wakeMarkerPath(homeDir, id)
		if data, err := os.ReadFile(marker); err == nil && string(data) == fingerprint {
			if obs != nil {
				obs.suppressedDuplicates++
			}
			continue
		}
		if err := EnqueueWake(homeDir, reason.Kind, id, reason.Message); err != nil {
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
	plugins, err := DiscoverAllChecks(homeDir)
	if err != nil {
		return emitted, err
	}
	for _, plugin := range plugins {
		// Validate the check artifact before surfacing. The port exists, so
		// this error is about this artifact and nothing else: drop this check,
		// keep scanning the rest, and say so rather than swallowing it.
		if err := checks.ValidateCheck(plugin.Path); err != nil {
			fmt.Fprintf(os.Stderr, "poll check invalid (skipped): %v\n", err)
			continue
		}
		// Acceptance refusal leaves the externally authored artifact exactly
		// where it is and emits no wake; the refusal is reported for the
		// operator to resolve.
		if err := AcceptOrRefuseStale(plugin.Path); err != nil {
			fmt.Fprintf(os.Stderr, "poll check refused (left in place): %v\n", err)
			continue
		}

		checkID := plugin.Label
		msg := fmt.Sprintf("check ready: %s", plugin.Label)
		if plugin.Kind == CheckPerTask {
			// Include PR URL in message if available
			if meta, err := home.ReadMeta(homeDir, plugin.Label); err == nil {
				if prURL, ok := meta["pr_url"]; ok && prURL != "" {
					msg = fmt.Sprintf("PR poll ready for task %s: %s", plugin.Label, prURL)
				}
			}

			// Attempt crash-safe retirement for merged polls. The loop does
			// not re-validate the artifact here: RetireMergedPoll applies the
			// same rule to the same path as its first action, before any
			// query, record or mutation, and reports a refusal as
			// ErrCheckValidationRefused — handled below with the disposition a
			// caller-side check would have produced. A second copy here could
			// only ever agree, and would be one more disposition to keep in
			// step with the one at the top of this loop.
			//
			// On successful retirement, the poll is removed and a durable
			// status line is published. The check wake is NOT emitted — the
			// status scan will surface it as a signal wake on the next cycle.
			retireErr := retirement.RetireMergedPoll(homeDir, plugin.Label, plugin.Path)
			if retireErr == nil {
				// Poll retired successfully. Skip wake emission;
				// the status signal path will surface the publication.
				continue
			}
			if errors.Is(retireErr, domain.ErrCheckInvalidAfterPublication) {
				fmt.Fprintf(os.Stderr, "poll check invalid after publication: %v\n", retireErr)
				continue
			}
			if errors.Is(retireErr, domain.ErrCheckValidationRefused) {
				fmt.Fprintf(os.Stderr, "poll check refused (wake suppressed): %v\n", retireErr)
				continue
			}
			// Other retirement failures retain the normal check wake so the
			// poll can be tried again.
		}

		fingerprint := "check\n" + msg
		marker := wakeMarkerPath(homeDir, "check:"+checkID)
		if data, err := os.ReadFile(marker); err == nil && string(data) == fingerprint {
			if obs != nil {
				obs.suppressedDuplicates++
			}
			continue
		}
		if err := EnqueueWake(homeDir, "check", checkID, msg); err != nil {
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
	// Per-cycle mailbox recovery runs above (see comment), sharing the
	// same reconcile hook with startup recovery.
	// The watcher is recovery-only for pending envelope delivery.
	// Normal rank-aware communication goes directly via SendReport.

	return emitted, nil
}

func wakeFingerprint(homeDir string, reason *WakeReason) string {
	message := strings.TrimSuffix(reason.Message, "; demand-deep-inspection")
	status := ""
	if len(reason.TaskIDs) > 0 {
		if lines, err := home.ReadStatus(homeDir, reason.TaskIDs[0]); err == nil && len(lines) > 0 {
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
func isNoMistakesActive(homeDir, id string, states TaskStatePort) bool {
	s, err := states.ReadTaskState(homeDir, id)
	if err != nil {
		return false
	}
	return absorbStaleSignal(s)
}

// absorbStaleSignal returns true when the soldier state has an active
// no-mistakes run-step that should absorb a stale signal.
func absorbStaleSignal(s *ObservedTaskState) bool {
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
func shouldAbsorbStale(homeDir, id string, paneAlive bool, states TaskStatePort) bool {
	if isNoMistakesActive(homeDir, id, states) {
		return true
	}
	// Check pause status: absorb only if within the resurface threshold.
	if isStatusPaused(homeDir, id) {
		return !isPausedBeyondResurface(homeDir, id)
	}
	if !paneAlive {
		return false
	}
	switch home.AbsorbClass(id, filepath.Join(homeDir, "state")) {
	case domain.Working, domain.Paused:
		return true
	}
	return false
}

// isStatusPaused checks whether the task's last status line is a declared
// deliberate external-wait pause. Returns false if no status file exists.
func isStatusPaused(homeDir, id string) bool {
	lines, err := home.ReadStatus(homeDir, id)
	if err != nil || len(lines) == 0 {
		return false
	}
	return domain.IsPaused(lines[len(lines)-1])
}

// isPausedBeyondResurface reports whether a paused task has been paused
// longer than the resurface threshold and should surface as stale.
// Uses the status file's modification time as a proxy for pause duration.
func isPausedBeyondResurface(homeDir, id string) bool {
	statusPath, err := home.StatusFilePath(homeDir, id)
	if err != nil {
		return false
	}
	fi, err := os.Stat(statusPath)
	if err != nil {
		return false
	}
	return time.Since(fi.ModTime()) > pauseResurfaceThreshold
}

// isStatusGeneralRelevant checks whether the task's last status line contains
// a general-relevant verb. Returns false if no status file exists.
func isStatusGeneralRelevant(homeDir, id string) bool {
	lines, err := home.ReadStatus(homeDir, id)
	if err != nil || len(lines) == 0 {
		return false
	}
	return domain.GeneralRelevant(lines[len(lines)-1])
}
