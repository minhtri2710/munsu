package afk

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// Daemon is the Go-native AFK sub-supervisor daemon.
// It manages the consent flag, identity lock, and wake triage lifecycle.
// Phase 2.2: runLoop integrates digester, wedge detection, and stale clearing.
type Daemon struct {
	homeDir string
	lock    *Lock
	digester *Digester
	wedge    *WedgeDetector
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

// runLoop is the main supervision loop:
//   triage → feed digester → check wedge → clear stale → flush if window expired.
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

// triageCycle performs one iteration: triage → feed digester → check wedge → clear stale.
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

	// 4. Check for wedge conditions and feed into digester.
	if alarm := d.wedge.Check(now); alarm != nil {
		fmt.Fprintf(os.Stderr, "afk: wedge alarm: %s\n", alarm.Reason)
		d.digester.FeedWedge(alarm)
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
