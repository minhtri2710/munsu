// Package afk provides away-mode supervision (sub-supervisor daemon).
package afk

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	afkFlagFile  = "state/.afk"
	pollInterval = 30 * time.Second
)

// Start begins the afk daemon: sets the durable afk flag, then runs a
// supervision loop that batches routine wakes and escalates captain-relevant
// events. Blocks until SIGTERM/SIGINT, then clears the flag.
func Start(homeDir string) error {
	flagPath := filepath.Join(homeDir, afkFlagFile)
	if err := os.WriteFile(flagPath, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0644); err != nil {
		return fmt.Errorf("setting afk flag: %w", err)
	}
	fmt.Println("AFK daemon started, flag set")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	stopCh := make(chan struct{})
	go runLoop(homeDir, stopCh)

	<-sigCh
	close(stopCh)
	os.Remove(flagPath)
	fmt.Println("AFK daemon stopped, flag cleared")
	return nil
}

// runLoop is the main supervision loop.
func runLoop(homeDir string, stopCh chan struct{}) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			scanStatusFiles(homeDir)
		}
	}
}

// scanStatusFiles checks for captain-relevant events in status files.
func scanStatusFiles(homeDir string) {
	stateDir := filepath.Join(homeDir, "state")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".status") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(stateDir, entry.Name()))
		if err != nil {
			continue
		}

		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) == 0 {
			continue
		}

		lastLine := strings.TrimSpace(lines[len(lines)-1])
		if lastLine == "" {
			continue
		}

		// Escalate captain-relevant states
		if strings.HasPrefix(lastLine, "done:") ||
			strings.HasPrefix(lastLine, "failed:") ||
			strings.HasPrefix(lastLine, "needs-decision:") {
			fmt.Printf("[AFK] %s: %s\n", entry.Name(), lastLine)
		}
	}
}

// IsActive checks if the afk daemon is running (flag file exists).
func IsActive(homeDir string) bool {
	_, err := os.Stat(filepath.Join(homeDir, afkFlagFile))
	return err == nil
}
