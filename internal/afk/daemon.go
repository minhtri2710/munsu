package afk

import (
	"encoding/json"
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
// It manages the consent flag, identity lock, and wake triage lifecycle.
// Phase 2.2: runLoop integrates digester, wedge detection, and stale clearing.
type Daemon struct {
	homeDir  string
	lock     *Lock
	digester *Digester
	wedge    *WedgeDetector
	capture  PaneCapture
	backend  Backend
	injector *Injector
}

// SetPaneCapture sets the pane capture interface for checking target safety.
// Must be called before Start if target safety checking is desired.
func (d *Daemon) SetPaneCapture(cap PaneCapture) {
	d.capture = cap
	d.maybeInitInjector()
}

// SetBackend sets the backend for sending keystrokes to the general pane.
// Must be called before Start if inject is desired.
func (d *Daemon) SetBackend(backend Backend) {
	d.backend = backend
	d.maybeInitInjector()
}

// maybeInitInjector creates the injector when both backend and capture are available.
func (d *Daemon) maybeInitInjector() {
	if d.backend != nil && d.capture != nil && d.injector == nil {
		d.injector = NewInjector(d.backend, d.capture, d.homeDir)
	}
}

// Start runs the AFK daemon foreground process:
//  1. Acquire the identity lock (idempotent — no-op if already running).
//  2. Set the consent flag (state/.afk).
//  3. Clear stale artifacts from any prior session.
//  4. Start the runLoop goroutine (triage → feed digester → check wedge → clear stale → flush).
//  5. Block until SIGTERM/SIGINT.
//  6. Flush any remaining digest entries.
//  7. Clear the consent flag.
//  8. Release the identity lock.
//
// Returns an error if lock acquisition or flag writing fails.
// Returns nil on clean shutdown.
func (d *Daemon) Start(homeDir string) error {
	d.homeDir = homeDir

	// 1. Acquire identity lock (idempotent).
	lock, acquired, err := AcquireLock(homeDir)
	if err != nil {
		return fmt.Errorf("acquiring afk lock: %w", err)
	}
	if !acquired {
		return fmt.Errorf("afk daemon already running (lock held)")
	}
	d.lock = lock

	// 2. Set consent flag.
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

	// 3. Clear stale artifacts from any prior session.
	if err := ClearStaleArtifacts(homeDir); err != nil {
		fmt.Fprintf(os.Stderr, "afk: stale artifact clear error (non-fatal): %v\n", err)
	}

// Initialize digester and wedge detector.
	d.digester = NewDigester(homeDir)
	d.wedge = NewWedgeDetector(homeDir)

	// Load optional AFK config (digest window, wedge thresholds).
	loadAfkConfig(homeDir, d.digester, d.wedge)
	// 4. Start the runLoop goroutine.
	stopCh := make(chan struct{})
	go d.runLoop(stopCh)

	// 5. Wait for signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh
	close(stopCh)
	fmt.Fprintf(os.Stderr, "afk: signal received, shutting down\n")

	// 6. Flush any remaining digest entries.
	if err := d.digester.Flush(time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "afk: final digest flush error (non-fatal): %v\n", err)
	}

	// 7. Clear consent flag.
	os.Remove(flagPath)

	// 8. Release lock.
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
//	triage → feed digester → check target safety → feed wedge → check wedge (beat/repeat/max-defer) → clear stale → flush.
func (d *Daemon) triageCycle(now time.Time) {
	// Track whether we need to attempt injection after flush.
	var lastSafe bool

	// 1. Run triage (drain wake queue and classify).
	digest, err := OneCycle(d.homeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "afk: triage error (non-fatal): %v\n", err)
		return
	}

	// 2. Feed digester with triage results.
	d.digester.Feed(digest)

	// Check general-pane target safety when there are entries AND capture is configured.
	// Safety is checked even for routine-only cycles so self-handle can avoid
	// writing routine entries to the digest when the composer is empty.
	if digest != nil && (len(digest.Escalated) > 0 || len(digest.Routines) > 0) && d.capture != nil {
		target, err := ResolveTargetWithSource(d.homeDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "afk: target resolution error (non-fatal): %v\n", err)
		} else if err := ValidateTargetOwnership(&target); err != nil {
			fmt.Fprintf(os.Stderr, "afk: target ownership error (non-fatal): %v\n", err)
		} else {
			safe, verdict, err := IsSafeInjectTarget(d.capture, target.Handle)
			if err != nil {
				fmt.Fprintf(os.Stderr, "afk: target safety capture error (non-fatal): %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "afk: target safety: source=%s safe=%v verdict=%s\n", target.Source, safe, verdict)
				lastSafe = safe
				d.digester.SetTargetSafety(safe, verdict.String())
			}
		}
	}

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

	// 7. Inject (phase 2.4): after flush, if safe and injector is set.
	if d.injector != nil && lastSafe {
		if err := d.tryInject(); err != nil {
			fmt.Fprintf(os.Stderr, "afk: inject error (non-fatal): %v\n", err)
		}
	}
}

// tryInject reads the latest digest file and attempts to inject
// it into the general pane through the injector. Safe only when
// the injector is configured and all safety gates pass.
func (d *Daemon) tryInject() error {
	path := filepath.Join(d.homeDir, digestFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading digest for inject: %w", err)
	}

	var be BatchedEscalation
	if err := json.Unmarshal(data, &be); err != nil {
		return fmt.Errorf("unmarshal digest for inject: %w", err)
	}

	// Nothing to inject.
	if len(be.Entries) == 0 && be.WedgeAlarm == nil {
		return nil
	}

	if _, err := d.injector.InjectIfSafe(&be); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "afk: injected %d entries into general pane\n", len(be.Entries))
	return nil
}
