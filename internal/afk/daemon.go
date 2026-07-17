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
// Later phases will add batched digest writing, captain-pane injection,
// and the return gate.
type Daemon struct {
	homeDir string
	lock    *Lock
}

// Start runs the AFK daemon foreground process:
//  1. Acquire the identity lock (idempotent — no-op if already running).
//  2. Set the consent flag (state/.afk).
//  3. Run one wake-triage cycle (consent-gated).
//  4. Block until SIGTERM/SIGINT.
//  5. Clear the consent flag.
//  6. Release the identity lock.
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

	// 3. Run one triage cycle (only while consent flag exists).
	if IsActive(homeDir) {
		digest, err := OneCycle(homeDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "afk: triage error (non-fatal): %v\n", err)
		}
		if digest != nil {
			fmt.Fprintf(os.Stderr, "afk: triage cycle: %d escalated, %d routine\n",
				len(digest.Escalated), len(digest.Routines))
		}
	}

	// 4. Wait for signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh
	fmt.Fprintf(os.Stderr, "afk: signal received, shutting down\n")

	// 5. Clear consent flag.
	os.Remove(flagPath)

	// 6. Release lock.
	d.lock.Release()

	return nil
}
