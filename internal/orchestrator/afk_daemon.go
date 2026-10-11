package orchestrator

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
)

// Daemon is the Go-native AFK sub-supervisor daemon.
// It manages the consent flag, identity lock, and wake triage
// Phase 2.2+: runLoop integrates the digester, the watcher beat check, and stale clearing.
//
// The daemon diagnoses and accumulates; it never repairs. Reaching the general
// while they are away has two owners: the uplink notify path for immediate
// notice, and `munsu afk return` for reconciliation.
type Daemon struct {
	homeDir  string
	lock     *home.AFKLock
	digester *Digester

	// startedAt and beatAlarmed drive the dead-watcher alarm: one digest entry
	// per incident, latched until the beat is fresh again.
	startedAt   time.Time
	beatAlarmed bool

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
//  5. Start the runLoop goroutine (triage → feed digester → check watcher beat → clear stale → flush).
//  6. Block until SIGTERM/SIGINT.
//  7. Flush any remaining digest entries.
//  8. Clear the consent flag.
//  9. Release the identity lock.
//
// Step 1 comes first on purpose, and comes before the lock rather than merely
// before the flag: the AFK lock (home.AFKLockPath) and the writer identity are side effects visible
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
	lock, acquired, err := home.AcquireAFKLock(homeDir)
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

	// Initialize digester.
	d.digester = NewDigester(homeDir)
	d.startedAt = time.Now()

	// Load optional AFK config (digest window).
	loadAfkConfig(homeDir, d.digester)
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
// digester. Missing or invalid config values are silently ignored — defaults
// apply.
func loadAfkConfig(homeDir string, dig *Digester) {
	if val, err := config.Get(homeDir, "afk-digest-window"); err == nil {
		if d, err := time.ParseDuration(val); err == nil {
			dig.SetWindow(d)
			fmt.Fprintf(os.Stderr, "afk: digest window set to %s from config\n", d)
		}
	}
}

// runLoop is the main supervision loop:
//
//	triage → feed digester → check watcher beat → clear stale → flush if window expired.
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
//	triage → feed digester → check watcher beat → clear stale → flush.
func (d *Daemon) triageCycle(now time.Time) {
	// 1. Run triage (drain wake queue and classify).
	digest, err := OneCycle(d.homeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "afk: triage error (non-fatal): %v\n", err)
		return
	}

	// 2. Feed digester with triage results.
	d.digester.Feed(digest)

	// 3. Print the triage summary.
	if digest != nil {
		fmt.Fprintf(os.Stderr, "afk: triage cycle: %d escalated, %d routine\n",
			len(digest.Escalated), len(digest.Routines))
	}

	// 4. Check the watcher beat.
	d.checkBeat(now)

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

// checkBeat feeds the digester one wedge entry per incident of a stale or
// missing watcher beat. A dead watcher cannot report itself, so this check runs
// here, in the one process that is alive. The incident ends when the beat is
// fresh again.
func (d *Daemon) checkBeat(now time.Time) {
	beat := ReadBeatStatus(d.homeDir, now)
	if !beat.Stale {
		d.beatAlarmed = false
		return
	}
	if d.beatAlarmed || (!beat.Exists && now.Sub(d.startedAt) < StaleThreshold()) {
		return
	}
	d.beatAlarmed = true
	reason := "watcher beat never set"
	if beat.Exists {
		reason = fmt.Sprintf("watcher beat stale: age=%s threshold=%s", beat.Age.Round(time.Second), StaleThreshold())
	}
	fmt.Fprintf(os.Stderr, "afk: wedge alarm: %s\n", reason)
	d.digester.FeedWedge(reason, now)
}
