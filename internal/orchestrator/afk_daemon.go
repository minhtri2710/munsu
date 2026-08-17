package orchestrator

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
)

// Daemon is the Go-native AFK sub-supervisor daemon.
// It manages the consent flag, identity lock, and wake triage
// Phase 2.2+: runLoop integrates digester, wedge detection, and stale clearing.
//
// The daemon diagnoses and accumulates; it never repairs. Reaching the general
// while they are away has two owners: the uplink notify path for immediate
// notice, and `munsu afk return` for reconciliation (ADR-0013).
type Daemon struct {
	homeDir  string
	lock     *Lock
	digester *Digester
	wedge    *WedgeDetector

	// ready, when non-nil, is closed by Start immediately after the signal
	// handler is installed and before any other work. It is the daemon's
	// readiness seam: once it is closed, SIGTERM starts an orderly shutdown, and
	// nothing the daemon writes — lock, writer identity, consent flag — can have
	// become visible any earlier.
	ready chan struct{}
}

// Start runs the AFK daemon foreground process:
//  1. Install the SIGTERM/SIGINT handler and close d.ready.
//  2. Acquire the identity lock (idempotent — no-op if already running).
//  3. Set the consent flag (state/.afk).
//  4. Clear stale artifacts from any prior session.
//  5. Start the runLoop goroutine (triage → feed digester → check wedge → clear stale → flush).
//  6. Block until SIGTERM/SIGINT.
//  7. Flush any remaining digest entries.
//  8. Clear the consent flag.
//  9. Release the identity lock.
//
// Step 1 comes first on purpose, and comes before the lock rather than merely
// before the flag: state/.lock and the writer identity are side effects visible
// outside the process too, so any of them appearing while SIGTERM still carries
// its default disposition would let an outside observer signal a daemon that
// cannot yet catch it — killing it outright and skipping steps 7 through 9.
//
// Returns an error if lock acquisition or flag writing fails.
// Returns nil on clean shutdown.
func (d *Daemon) Start(homeDir string) error {
	d.homeDir = homeDir

	// 1. Install the signal handler before any externally observable side effect.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)
	if d.ready != nil {
		close(d.ready)
	}

	// 2. Acquire identity lock (idempotent).
	lock, acquired, err := AcquireLock(homeDir)
	if err != nil {
		return fmt.Errorf("acquiring afk lock: %w", err)
	}
	if !acquired {
		return fmt.Errorf("afk daemon already running (lock held)")
	}
	d.lock = lock
	identity, err := publishDaemonIdentity(homeDir)
	if err != nil {
		d.lock.Release()
		return fmt.Errorf("publishing afk identity: %w", err)
	}
	defer clearDaemonIdentity(homeDir, identity)

	// 3. Set consent flag.
	flagPath := filepath.Join(homeDir, afkFlagFile)
	if err := os.MkdirAll(filepath.Dir(flagPath), 0755); err != nil {
		d.lock.Release()
		return fmt.Errorf("creating flag directory: %w", err)
	}
	if err := os.WriteFile(flagPath, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0644); err != nil {
		d.lock.Release()
		return fmt.Errorf("setting afk flag: %w", err)
	}
	fmt.Fprintf(os.Stderr, "afk: daemon started, consent flag set\n")

	// 4. Clear stale artifacts from any prior session.
	if err := ClearStaleArtifacts(homeDir); err != nil {
		fmt.Fprintf(os.Stderr, "afk: stale artifact clear error (non-fatal): %v\n", err)
	}

	// Initialize digester and wedge detector.
	d.digester = NewDigester(homeDir)
	d.wedge = NewWedgeDetector(homeDir)

	// Load optional AFK config (digest window, wedge thresholds).
	loadAfkConfig(homeDir, d.digester, d.wedge)
	// 5. Start the runLoop goroutine.
	stopCh := make(chan struct{})
	go d.runLoop(stopCh)

	// 6. Wait for signal.
	<-sigCh
	close(stopCh)
	fmt.Fprintf(os.Stderr, "afk: signal received, shutting down\n")

	// 7. Flush any remaining digest entries.
	if err := d.digester.Flush(time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "afk: final digest flush error (non-fatal): %v\n", err)
	}

	// 8. Clear consent flag.
	os.Remove(flagPath)

	// 9. Release lock.
	d.lock.Release()

	return nil
}

// loadAfkConfig reads optional AFK config files and applies them to the
// digester and wedge detector. Missing or invalid config values are silently
// ignored — defaults apply.
func loadAfkConfig(homeDir string, dig *Digester, wdg *WedgeDetector) {
	if val, err := config.Get(homeDir, "afk-digest-window"); err == nil {
		if d, err := time.ParseDuration(val); err == nil {
			dig.SetWindow(d)
			fmt.Fprintf(os.Stderr, "afk: digest window set to %s from config\n", d)
		}
	}
	if val, err := config.Get(homeDir, "afk-max-defer"); err == nil {
		if d, err := time.ParseDuration(val); err == nil {
			dig.SetMaxDefer(d)
			fmt.Fprintf(os.Stderr, "afk: max-defer set to %s from config\n", d)
		}
	}
	if val, err := config.Get(homeDir, "afk-wedge-stale-beat"); err == nil {
		if d, err := time.ParseDuration(val); err == nil {
			wdg.SetStaleThreshold(d)
			fmt.Fprintf(os.Stderr, "afk: wedge stale-beat set to %s from config\n", d)
		}
	}
	if val, err := config.Get(homeDir, "afk-wedge-max-repeat"); err == nil {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			wdg.SetWakeCountMax(n)
			fmt.Fprintf(os.Stderr, "afk: wedge max-repeat set to %d from config\n", n)
		}
	}
}

// runLoop is the main supervision loop:
//
//	triage → feed digester → check wedge → clear stale → flush if window expired.
//
// Runs until stopCh is closed.
func (d *Daemon) runLoop(stopCh chan struct{}) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case now := <-ticker.C:
			d.triageCycle(now)
		}
	}
}

// triageCycle performs one iteration:
//
//	triage → feed digester → feed wedge → check wedge (beat/repeat/max-defer) → clear stale → flush.
func (d *Daemon) triageCycle(now time.Time) {
	// 1. Run triage (drain wake queue and classify).
	digest, err := OneCycle(d.homeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "afk: triage error (non-fatal): %v\n", err)
		return
	}

	// 2. Feed digester with triage results.
	d.digester.Feed(digest)

	// 3. Feed wedge detector (for repeated wake detection).
	if digest != nil {
		// Track the most common wake key from the cycle.
		if len(digest.Escalated) > 0 {
			d.wedge.FeedWake(digest.Escalated[0].Key)
		} else if len(digest.Routines) > 0 {
			d.wedge.FeedWake(digest.Routines[0].Key)
		} else {
			d.wedge.ResetWake()
		}

		// Print triage summary.
		fmt.Fprintf(os.Stderr, "afk: triage cycle: %d escalated, %d routine\n",
			len(digest.Escalated), len(digest.Routines))
	} else {
		d.wedge.ResetWake()
	}

	// 4. Check for wedge conditions (beat stale, repeat, max-defer) and feed into digester.
	if alarm := d.wedge.Check(now); alarm != nil {
		fmt.Fprintf(os.Stderr, "afk: wedge alarm: %s\n", alarm.Reason)
		d.digester.FeedWedge(alarm)
	}

	// 4b. Max-defer alarm: check if entries have been stuck in the digester too long.
	if firstAt := d.digester.FirstAt(); !firstAt.IsZero() {
		maxDefer := d.digester.MaxDeferDuration()
		if alarm := d.wedge.CheckDigestStuck(firstAt, maxDefer, now); alarm != nil {
			fmt.Fprintf(os.Stderr, "afk: max-defer alarm: %s\n", alarm.Reason)
			d.digester.FeedWedge(alarm)
		}
	}

	// 5. Clear stale check markers (session-scoped per-cycle artifacts).
	if err := ClearStaleCheckedMarkers(d.homeDir); err != nil {
		fmt.Fprintf(os.Stderr, "afk: stale check marker clear error (non-fatal): %v\n", err)
	}

	// 6. Flush digester if the window has expired.
	if d.digester.ShouldFlush(now) {
		if err := d.digester.Flush(now); err != nil {
			fmt.Fprintf(os.Stderr, "afk: digest flush error (non-fatal): %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "afk: digest flushed\n")
		}
	}
}
