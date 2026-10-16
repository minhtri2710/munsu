package orchestrator

import (
	"encoding/json"
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

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

const watcherPollInterval = 5 * time.Second

const (
	// watcherStopWait bounds how long StopWatcher observes a signalled
	// watcher, and watcherStopPoll is how often it looks. See waitForWatcherExit
	// for why the bound expiring is not a failure.
	watcherStopWait = 2 * time.Second
	watcherStopPoll = 20 * time.Millisecond
)

// WakeReason describes an actionable watcher condition.
type WakeReason struct {
	Kind    string // signal, stale, check
	TaskIDs []string
	Message string
	// ReportOwned marks a signal whose status line is a report: its report wake
	// is derived from the line, so the cycle records it but enqueues no second.
	ReportOwned bool
}

// Run starts the persistent watcher daemon. It records actionable conditions in
// the durable wake queue and continues polling until SIGTERM or SIGINT.
type TaskEndpointProbe interface {
	Probe(homeDir string, ep taskauthority.EndpointBinding, harness string) (bool, error)
}

// WatcherHooks is the per-cycle work the composition root adds to the watcher.
type WatcherHooks interface {
	Activate(homeDir string)
}

type processEventWakeKey struct {
	eventID    string
	generation uint64
}

// consumeProcessEventWakes drains this cycle's process-event wakes and routes
// each resolved, unacked event they announce to the owner of its event-id
// prefix. The drain is the claim: a crash between drain and ack leaves the
// record unacked and RecoverProcessEvents re-announces it, so every arm's
// action must be idempotent on re-entry. A wake whose prefix has no owner is
// logged and dropped and never re-queued -- a prefix nothing owns would
// re-queue forever -- and its record stays resolved and unacked for restart
// recovery.
func consumeProcessEventWakes(homeDir string, reviews ReviewVerdictPort) error {
	wakes, err := home.DrainWakesOfKind(homeDir, home.ProcessEventWakeKind)
	if err != nil {
		return err
	}
	seen := map[processEventWakeKey]bool{}
	for _, wake := range wakes {
		var announced ProcessEventWake
		if err := json.Unmarshal([]byte(wake.Payload), &announced); err != nil || announced.EventID == "" {
			fmt.Fprintf(os.Stderr, "process-event wake %q dropped: undecodable payload: %v\n", wake.Key, err)
			continue
		}
		key := processEventWakeKey{announced.EventID, announced.Generation}
		if seen[key] {
			continue
		}
		seen[key] = true
		rec, err := readProcessEventRecord(homeDir, announced.EventID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "process-event wake %q dropped: %v\n", announced.EventID, err)
			continue
		}
		if rec == nil {
			fmt.Fprintf(os.Stderr, "process-event wake %q dropped: no record\n", announced.EventID)
			continue
		}
		if announced.Generation != rec.Generation {
			fmt.Fprintf(os.Stderr, "process-event wake %q dropped: generation %d superseded by %d\n", announced.EventID, announced.Generation, rec.Generation)
			continue
		}
		if rec.Generation == rec.AckedGeneration {
			continue
		}
		if !rec.Resolved {
			fmt.Fprintf(os.Stderr, "process-event wake %q dropped: record is not resolved\n", announced.EventID)
			continue
		}
		switch {
		case strings.HasPrefix(announced.EventID, reviewVerdictEventPrefix):
			recordReviewVerdictWake(homeDir, reviews, announced, rec)
		default:
			fmt.Fprintf(os.Stderr, "process-event wake %q dropped: no owner for this event prefix\n", announced.EventID)
		}
	}
	return nil
}

// ActiveDelivery names one delivery journal an interrupted delivery left
// active.
type ActiveDelivery struct {
	ID, TaskID, Stage string
}

// ActiveDeliveryPort lists the active delivery journals of a home without
// resuming any. It returns an error wrapping home.ErrLockTimeout while a live
// delivery holds the delivery scope.
type ActiveDeliveryPort interface {
	ActiveDeliveries(homeDir string) ([]ActiveDelivery, error)
}

// deliveryRecoveryKey keys the wake of a delivery journal read failure.
const deliveryRecoveryKey = "delivery-recovery"

// wakeActiveDeliveries wakes the General once per delivery journal an
// interrupted delivery left active, and once per journal read failure. The
// daemon resumes nothing; resume stays operator-run. A held delivery scope is
// a live Deliver that completes its own journal, so it wakes nothing.
func wakeActiveDeliveries(homeDir string, port ActiveDeliveryPort, obs *cycleObservation) error {
	active, err := port.ActiveDeliveries(homeDir)
	if errors.Is(err, home.ErrLockTimeout) {
		return nil
	}
	if err != nil {
		_, err = enqueueCheckWakeOnce(homeDir, deliveryRecoveryKey, fmt.Sprintf("delivery journals could not be read: %v; run `munsu watch run` to retry recovery", err), obs)
		return err
	}
	// A readable index ends the failure, so the next failure wakes again.
	if err := os.Remove(home.WatcherSeenMarkerPath(homeDir, "check:"+deliveryRecoveryKey)); err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, d := range active {
		msg := fmt.Sprintf("delivery journal %s of task %s is still active at stage %s after an interrupted delivery; run `munsu watch run` or pr-merge to resume it", d.ID, d.TaskID, d.Stage)
		if _, err := enqueueCheckWakeOnce(homeDir, "delivery:"+d.ID, msg, obs); err != nil {
			return err
		}
	}
	return nil
}

// RunWithProbeSenderAndEvents starts the persistent watcher and also runs the
// bounded native observation event lane: a validated event hint
// triggers an immediate re-probe cycle; every other outcome keeps the polling
// ticker as the cadence authority (the watcher is never silent). A nil observation
// event port keeps the watcher on pure polling.
func RunWithProbeSenderAndEvents(homeDir string, probe TaskEndpointProbe, sender RecoverySender, hooks WatcherHooks, reviews ReviewVerdictPort, events ObservationEventPort, deliveries ActiveDeliveryPort) (*WakeReason, error) {
	return run(homeDir, time.NewTicker, signalChannel(), probe, sender, hooks, reviews, events, deliveries)
}

func signalChannel() <-chan os.Signal {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	return sigCh
}

func run(homeDir string, newTicker func(time.Duration) *time.Ticker, sigCh <-chan os.Signal, probe TaskEndpointProbe, sender RecoverySender, hooks WatcherHooks, reviews ReviewVerdictPort, events ObservationEventPort, deliveries ActiveDeliveryPort) (*WakeReason, error) {
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
	_, err = home.ClaimWatcherLease(homeDir, pid)
	if err != nil {
		return nil, fmt.Errorf("claiming watcher lease: %w", err)
	}
	defer home.ReleaseWatcherLeaseIfMatches(homeDir, pid)

	// Write watcher identity on start and clear it on exit.
	identity := NewIdentity(homeDir)
	if err := WriteIdentity(homeDir, identity); err != nil {
		return nil, fmt.Errorf("writing watcher identity: %w", err)
	}
	defer ClearIdentityIfMatches(homeDir, identity)

	if err := WriteBeat(homeDir); err != nil {
		return nil, fmt.Errorf("writing watcher beat: %w", err)
	}
	ticker := newTicker(watcherPollInterval)
	defer ticker.Stop()

	// Optional native event lane: bounded event wait feeding
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

	// cycle is one daemon cycle boundary: the beat, the active delivery
	// journal wakes, then the scan.
	cycle := func() error {
		if err := WriteBeat(homeDir); err != nil {
			return fmt.Errorf("writing watcher beat: %w", err)
		}
		obs := newCycleObservation()
		if err := wakeActiveDeliveries(homeDir, deliveries, obs); err != nil {
			return err
		}
		if _, err := runCycleWithProbeAndSender(homeDir, probe, sender, hooks, reviews, obs); err != nil {
			return err
		}
		logCycleObservation(obs)
		return nil
	}

	for {
		select {
		case <-sigCh:
			return &WakeReason{Kind: "signal", Message: "watcher interrupted"}, nil
		case <-ticker.C:
			if err := cycle(); err != nil {
				return nil, recordFatalCycle(homeDir, err)
			}
		case pulse, ok := <-eventPulses:
			if !ok {
				// Event lane closed (no native surface / degraded): keep pure
				// polling for the rest of the run.
				eventPulses = nil
				continue
			}
			if pulse.outcome == EventWaitSignal {
				// Event-to-reprobe: a native wake hint only
				// triggers an immediate re-probe cycle; the cycle re-probes the
				// exact binding before any recovery/relaunch/dispose decision.
				// The hint itself is never lifecycle truth and never sets a
				// Task phase.
				if err := cycle(); err != nil {
					return nil, recordFatalCycle(homeDir, err)
				}
			}
		}
	}
}

// recordFatalCycle appends the cycle error that ends the watcher to the event
// log, since a detached watcher's stderr goes nowhere, and returns it. A home
// whose state directory is gone gets no record: writing one would recreate it.
func recordFatalCycle(homeDir string, err error) error {
	if _, statErr := os.Stat(filepath.Join(homeDir, "state")); statErr != nil {
		return err
	}
	payload := strings.Join(strings.Fields(err.Error()), " ")
	if _, logErr := Append(homeDir, "watcher.cycle.failed", "munsu:watcher", "", payload); logErr != nil {
		return errors.Join(err, fmt.Errorf("recording watcher cycle failure: %w", logErr))
	}
	return err
}

// startWatcherProcess and signalWatcher are unexported seams for tests.
// Production uses defaultStartWatcher and signalWatcherProcess. Tests
// substitute them to verify the identity clearance contract without spawning a
// real daemon, and to make a live watcher refuse the stop signal.
var (
	startWatcherProcess = defaultStartWatcher
	signalWatcher       = signalWatcherProcess
)

func defaultStartWatcher(homeDir string) (int, error) {
	execPath, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("finding munsu binary: %w", err)
	}

	cmd := exec.Command(execPath, "watch", "--home", homeDir)
	cmd.Dir = homeDir
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Env = append(os.Environ(), "MUNSU_HOME="+homeDir)
	configureWatcherProcess(cmd)

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting watcher: %w", err)
	}
	return cmd.Process.Pid, nil
}

// StartWatcher launches a detached watcher for homeDir and returns its PID.
// It clears the stale identity first so that handshake polling never reads
// stale state (e.g. old CommitSHA) before the subprocess writes its own.
func StartWatcher(homeDir string) (int, error) {
	ClearIdentity(homeDir)
	return startWatcherProcess(homeDir)
}

// ArmBackground launches the watcher as a background process.
// If restart is true, stops any existing watcher first and refuses to launch
// when that watcher's ownership cannot be proven or it cannot be signalled.
func ArmBackground(homeDir string, restart bool) error {
	if restart {
		if _, err := StopWatcher(homeDir); err != nil {
			return err
		}
	}
	pid, err := StartWatcher(homeDir)
	if err != nil {
		return err
	}
	fmt.Printf("Watcher armed (pid %d)\n", pid)
	return nil
}

// WatcherStopState is what StopWatcher observed of the watcher it targeted.
type WatcherStopState string

const (
	StopAlreadyStopped   WatcherStopState = "already-stopped"
	StopIdentityMismatch WatcherStopState = "identity-mismatch"
	StopExited           WatcherStopState = "stopped"
	StopUnresponsive     WatcherStopState = "unresponsive"
)

// WatcherStop reports the PID StopWatcher targeted and what became of it.
type WatcherStop struct {
	PID   int
	State WatcherStopState
}

// StopWatcher signals the running watcher identified by beat + identity and
// observes it for a bounded time. Idempotent: no beat is StopAlreadyStopped.
//
// Ownership must be proven from the identity file; beat-only state is
// ambiguous and returns StopIdentityMismatch with an error. A signal the
// kernel refuses returns StopUnresponsive with an error: EPERM means a
// process exists that we could not stop. Only an observed exit reads as
// StopExited, and only then are the beat and identity cleared -- a watcher
// still alive keeps the evidence a later stop needs to target it. The watch
// flock is never removed here; it releases when its holder exits.
func StopWatcher(homeDir string) (WatcherStop, error) {
	_, pid, ok := ReadBeat(homeDir)
	if !ok || pid <= 0 {
		return WatcherStop{State: StopAlreadyStopped}, nil
	}
	stop := WatcherStop{PID: pid, State: StopIdentityMismatch}
	if !ValidatePIDOwnership(homeDir, pid) {
		return stop, fmt.Errorf("watcher pid %d ownership could not be verified; refusing to signal", pid)
	}

	stop.State = StopUnresponsive
	proc, err := os.FindProcess(pid)
	if err == nil {
		err = signalWatcher(proc)
	}
	if err != nil {
		return stop, fmt.Errorf("signaling watcher pid %d: %w", pid, err)
	}
	if !waitForWatcherExit(pid) {
		return stop, nil
	}
	ClearBeat(homeDir)
	ClearIdentity(homeDir)
	stop.State = StopExited
	return stop, nil
}

// waitForWatcherExit observes the signalled watcher until it is gone or the
// bound expires, and reports whether it saw the exit. It exists because
// ArmBackground's restart path starts a replacement whose own AcquireWatch
// takes the watch flock, and that flock is still held while the old process
// runs.
//
// Expiry is not an error: StopWatcher reports it as StopUnresponsive. The
// one caller that restarts (completeHandshake, the only caller of
// ArmBackground(restart=true)) proves convergence itself via
// waitForNewWatcher. Nothing durable depends on the old watcher being gone by
// now either: home.ReleaseWatcherLeaseIfMatches refuses to delete a
// successor's lease, and a watcher inside a slow cycle can outlast any bound
// we pick.
func waitForWatcherExit(pid int) bool {
	deadline := time.Now().Add(watcherStopWait)
	for time.Now().Before(deadline) {
		if !home.IsProcessAlive(pid) {
			return true
		}
		time.Sleep(watcherStopPoll)
	}
	return !home.IsProcessAlive(pid)
}

var (
	// staleFirstSeen tracks the first time a task was continuously stale.
	// Persists across scanFleet calls within a process; protected for concurrent package use.
	staleFirstSeenMu sync.Mutex
	staleFirstSeen   = map[string]time.Time{}
)

func scanFleetWithProbe(homeDir string, clearResolved bool, probe TaskEndpointProbe, lv config.SeatLiveness, obs *cycleObservation) []*WakeReason {
	var reasons []*WakeReason
	// Status-signal path: Human-needed last lines (needs-decision, blocked,
	// failed) wake General even when the pane is alive. Completion, readiness
	// and merge lines are audit projections and wake nothing here.
	seenStatus := map[string]bool{}
	isCaptainHome, _ := home.IsCaptainHome(homeDir)
	for _, match := range home.ScanGeneralRelevant(filepath.Join(homeDir, "state")) {
		if isCaptainHome {
			if meta, err := home.ReadMeta(homeDir, match.TaskID); err == nil && meta["kind"] == "captain" {
				continue
			}
		}
		seenStatus[match.TaskID] = true
		reason := &WakeReason{
			Kind:        "signal",
			TaskIDs:     []string{match.TaskID},
			Message:     match.LastLine,
			ReportOwned: domain.IsReportLine(match.LastLine),
		}
		if !reason.ReportOwned {
			if r := repeatedFailureReason(homeDir, match.TaskID, lv, reason); r != nil {
				reason = r
			}
		}
		reasons = append(reasons, reason)
	}
	// Endpoint path: the canonical live, bound soldier tasks are the ones
	// probed; a task past working or with no bound endpoint has no seat to
	// watch.
	bound, err := LiveBoundEndpoints(homeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "watcher: listing bound soldier tasks: %v\n", err)
	}
	for _, ep := range bound {
		id := ep.TaskID
		if seenStatus[id] {
			// Status signal already actionable for this id; skip stale scan.
			continue
		}
		if obs != nil {
			obs.scannedTasks++
		}
		reason := scanTaskWithProbe(homeDir, ep, probe, lv)
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

// repeatedFailureReason turns a seat repeating its last failed:/blocked: status
// line into one stale incident per stage: the nudge at lv.RepeatNudge identical
// lines and the end at lv.RepeatEnd. The message names the stage and never the
// count, so each stage keeps one fingerprint. A different last line ends the
// run, and the line is an ordinary signal again.
//
// A stage replaces signal only once the seat's watcher marker shows that line's
// signal, or an earlier stage of it, was already queued: the General receives
// the failure text first, and the next cycle escalates to the stage reached.
func repeatedFailureReason(homeDir, id string, lv config.SeatLiveness, signal *WakeReason) *WakeReason {
	lines, err := home.ReadStatus(homeDir, id)
	if err != nil || len(lines) == 0 {
		return nil
	}
	last := lines[len(lines)-1]
	verb := domain.LineVerb(strings.TrimSpace(last))
	if verb != "failed" && verb != "blocked" {
		return nil
	}
	run := 0
	for i := len(lines) - 1; i >= 0 && lines[i] == last; i-- {
		run++
	}
	stage := func(label string) *WakeReason {
		return &WakeReason{Kind: "stale", TaskIDs: []string{id}, Message: fmt.Sprintf("seat repeated the same %s status line (%s)", verb, label)}
	}
	nudge, end := stage("nudge: steer it"), stage("end: stuck, fail or relaunch it")
	var due *WakeReason
	switch {
	case run >= lv.RepeatEnd:
		due = end
	case run >= lv.RepeatNudge:
		due = nudge
	default:
		return nil
	}
	seen, err := os.ReadFile(home.WatcherSeenMarkerPath(homeDir, id))
	if err != nil {
		return nil
	}
	for _, queued := range []*WakeReason{signal, nudge, end} {
		if string(seen) == wakeFingerprint(homeDir, queued) {
			return due
		}
	}
	return nil
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

func scanTaskWithProbe(homeDir string, ep BoundEndpoint, probe TaskEndpointProbe, lv config.SeatLiveness) *WakeReason {
	id, windowID := ep.TaskID, ep.Endpoint.Handle

	if probe == nil {
		return &WakeReason{Kind: "check", TaskIDs: []string{id}, Message: "endpoint probe is not configured"}
	}
	paneAlive, err := probe.Probe(homeDir, ep.Endpoint, ep.Harness)
	if err != nil {
		return &WakeReason{Kind: "check", TaskIDs: []string{id}, Message: fmt.Sprintf("endpoint probe failed: %v", err)}
	}
	if !paneAlive {
		if isStatusGeneralRelevant(homeDir, id) {
			// Signal path already covers general-relevant; if we still reach
			// here (race), surface once with a stable message.
			return handleStale(id, fmt.Sprintf("pane %s is dead (general-relevant status)", windowID))
		}
		if shouldAbsorbStale(homeDir, id, false, lv) {
			resetStreak(id)
			return nil
		}
		return handleStale(id, fmt.Sprintf("pane %s is dead", windowID))
	}

	if statusPath, err := home.StatusFilePath(homeDir, id); err == nil {
		if fi, err := os.Stat(statusPath); err == nil {
			age := time.Since(fi.ModTime())
			if age > lv.SilentAfter {
				if isStatusGeneralRelevant(homeDir, id) {
					return handleStale(id, fmt.Sprintf("pane %s idle beyond threshold (general-relevant status)", windowID))
				}
				if shouldAbsorbStale(homeDir, id, true, lv) {
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
func RunCycleWithProbeAndSender(homeDir string, probe TaskEndpointProbe, sender RecoverySender, hooks WatcherHooks, reviews ReviewVerdictPort) (bool, error) {
	obs := newCycleObservation()
	emitted, err := runCycleWithProbeAndSender(homeDir, probe, sender, hooks, reviews, obs)
	if err == nil {
		logCycleObservation(obs)
	}
	return emitted, err
}

// runRecovery executes the one-shot recovery on watcher startup.
// It retries pending inbox envelopes once with fingerprint dedup,
// runs the mailbox recovery hook (if any) once.
func runRecovery(homeDir string, sender RecoverySender, hooks WatcherHooks) error {
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

func runCycleWithProbeAndSender(homeDir string, probe TaskEndpointProbe, sender RecoverySender, hooks WatcherHooks, reviews ReviewVerdictPort, obs *cycleObservation) (bool, error) {
	lv, err := config.ReadSeatLiveness(homeDir)
	if err != nil {
		return false, err
	}

	// Snapshot recovery state before the call: the activation hook runs on
	// cycles after the first (recovery handles startup).
	_, recoveryWasDone := recoveryDone.Load(homeDir)

	// Run one-shot recovery on first cycle for this home.
	if err := runRecovery(homeDir, sender, hooks); err != nil {
		return false, err
	}

	// Process-event recovery: re-announce resolved, unacked events. One-shot
	// per home per process; a failed run is retried next cycle.
	if reannounced, err := RecoverProcessEvents(homeDir); err != nil {
		fmt.Fprintf(os.Stderr, "process-event recovery error: %v\n", err)
	} else if reannounced > 0 {
		fmt.Fprintf(os.Stderr, "process-event recovery: %d re-announced\n", reannounced)
	}

	// Derive the report wakes of new report lines before the activation hook
	// dispatches the queue. A failure is logged; the next claim derives them.
	if err := home.DeriveReportWakes(homeDir); err != nil {
		fmt.Fprintf(os.Stderr, "report wake derivation: %v\n", err)
	}

	// Per-cycle activation (cycles after startup only). Nil hook = no-op.
	if recoveryWasDone {
		hooks.Activate(homeDir)
	}

	emitted := false
	var humanNeeded []humanNeededEvent
	for _, reason := range scanFleetWithProbe(homeDir, true, probe, lv, obs) {
		if len(reason.TaskIDs) == 0 {
			continue
		}
		id := reason.TaskIDs[0]
		fingerprint := wakeFingerprint(homeDir, reason)
		marker := home.WatcherSeenMarkerPath(homeDir, id)
		if data, err := os.ReadFile(marker); err == nil && string(data) == fingerprint {
			if obs != nil {
				obs.suppressedDuplicates++
			}
			continue
		}
		if !reason.ReportOwned {
			if err := EnqueueWake(homeDir, reason.Kind, id, reason.Message); err != nil {
				return emitted, fmt.Errorf("enqueue watcher wake: %w", err)
			}
			emitted = true
		}
		if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
			return emitted, err
		}
		if err := os.WriteFile(marker, []byte(fingerprint), 0644); err != nil {
			return emitted, err
		}
		if reason.Kind == "signal" {
			humanNeeded = append(humanNeeded, humanNeededEvent{TaskID: id, Line: reason.Message})
		}
	}
	popupHumanNeeded(homeDir, humanNeeded)

	// Consume this cycle's process-event wakes.
	registerReviewVerdictEvents(homeDir, reviews)
	if err := consumeProcessEventWakes(homeDir, reviews); err != nil {
		return emitted, err
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

// enqueueCheckWakeOnce enqueues a check wake for id unless the last check
// wake enqueued for id carried the same message, and reports whether it
// enqueued one.
func enqueueCheckWakeOnce(homeDir, id, msg string, obs *cycleObservation) (bool, error) {
	fingerprint := "check\n" + msg
	marker := home.WatcherSeenMarkerPath(homeDir, "check:"+id)
	if data, err := os.ReadFile(marker); err == nil && string(data) == fingerprint {
		if obs != nil {
			obs.suppressedDuplicates++
		}
		return false, nil
	}
	if err := EnqueueWake(homeDir, "check", id, msg); err != nil {
		return false, fmt.Errorf("enqueue check wake: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
		return false, err
	}
	if err := os.WriteFile(marker, []byte(fingerprint), 0644); err != nil {
		return false, err
	}
	return true, nil
}

func wakeFingerprint(homeDir string, reason *WakeReason) string {
	message := reason.Message
	status := ""
	if len(reason.TaskIDs) > 0 {
		if lines, err := home.ReadStatus(homeDir, reason.TaskIDs[0]); err == nil && len(lines) > 0 {
			status = lines[len(lines)-1]
		}
	}
	return reason.Kind + "\n" + message + "\n" + status
}

func clearWakeMarker(homeDir, id string) {
	_ = os.Remove(home.WatcherSeenMarkerPath(homeDir, id))
}

// handleStale creates a stale WakeReason and records when the task was first
// seen stale, for the cycle observation log.
func handleStale(id, msg string) *WakeReason {
	staleFirstSeenMu.Lock()
	if _, exists := staleFirstSeen[id]; !exists {
		staleFirstSeen[id] = time.Now()
	}
	staleFirstSeenMu.Unlock()

	return &WakeReason{
		Kind:    "stale",
		TaskIDs: []string{id},
		Message: msg,
	}
}

// resetStreak clears the stale-first-seen timestamp for a task.
// Called when a task is provably working or its status changes.
func resetStreak(id string) {
	staleFirstSeenMu.Lock()
	delete(staleFirstSeen, id)
	staleFirstSeenMu.Unlock()
}

// shouldAbsorbStale reports whether a stale condition is benign.
// paneAlive gates status-only "working" absorb: a dead pane with a leftover
// working: line is still actionable; an alive idle pane with working: is healthy.
// A paused task beyond the resurface threshold is NOT absorbed — it surfaces as stale.
// A task whose last status is done has reported completion (an audit
// projection, not attention), so its pane state is not a stale condition.
func shouldAbsorbStale(homeDir, id string, paneAlive bool, lv config.SeatLiveness) bool {
	if isStatusDone(homeDir, id) {
		return true
	}
	// Check pause status: absorb only if within the resurface threshold.
	if isStatusPaused(homeDir, id) {
		return !isPausedBeyondResurface(homeDir, id, lv.SilentAfter)
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

// isStatusDone checks whether the task's last status line is a completion
// report. Returns false if no status file exists.
func isStatusDone(homeDir, id string) bool {
	lines, err := home.ReadStatus(homeDir, id)
	if err != nil || len(lines) == 0 {
		return false
	}
	return domain.LineVerb(strings.TrimSpace(lines[len(lines)-1])) == "done"
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
func isPausedBeyondResurface(homeDir, id string, after time.Duration) bool {
	statusPath, err := home.StatusFilePath(homeDir, id)
	if err != nil {
		return false
	}
	fi, err := os.Stat(statusPath)
	if err != nil {
		return false
	}
	return time.Since(fi.ModTime()) > after
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
