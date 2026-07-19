package afk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ReturnReport summarizes the AFK daemon shutdown and digest drain.
// Returned by Return when the general signals they are back.
type ReturnReport struct {
	Escalations   []string `json:"escalations"`
	WedgeAlarms   []string `json:"wedge_alarms"`
	BlockedItems  []string `json:"blocked_items"`
	DigestedCount int      `json:"digested_count"`
}

// HasActionable reports whether the return report contains any item
// needing munsu attention before resuming normal work.
// The caller must check this before resuming normal work and re-run
// Return until it returns clean (HasActionable == false).
func (r *ReturnReport) HasActionable() bool {
	return len(r.Escalations) > 0 || len(r.WedgeAlarms) > 0 || len(r.BlockedItems) > 0
}

// String returns a human-readable summary of the report.
func (r *ReturnReport) String() string {
	var b strings.Builder
	b.WriteString("AFK return report\n")
	b.WriteString(fmt.Sprintf("  entries drained: %d\n", r.DigestedCount))
	if len(r.Escalations) > 0 {
		b.WriteString(fmt.Sprintf("  escalations (%d):\n", len(r.Escalations)))
		for _, e := range r.Escalations {
			b.WriteString(fmt.Sprintf("    - %s\n", e))
		}
	} else {
		b.WriteString("  escalations: none\n")
	}
	if len(r.WedgeAlarms) > 0 {
		b.WriteString(fmt.Sprintf("  wedge alarms (%d):\n", len(r.WedgeAlarms)))
		for _, w := range r.WedgeAlarms {
			b.WriteString(fmt.Sprintf("    - %s\n", w))
		}
	} else {
		b.WriteString("  wedge alarms: none\n")
	}
	if len(r.BlockedItems) > 0 {
		b.WriteString(fmt.Sprintf("  blocked items (%d):\n", len(r.BlockedItems)))
		for _, bItem := range r.BlockedItems {
			b.WriteString(fmt.Sprintf("    - %s\n", bItem))
		}
	}
	if r.HasActionable() {
		b.WriteString("\nActionable items remain — reconcile before resuming normal work.\n")
	} else {
		b.WriteString("All clear — ready to resume normal work.\n")
	}
	return b.String()
}

// Return performs an ordered shutdown of the AFK daemon and drains the
// durable digest queue. Idempotent: if the daemon is not running, stop
// steps are skipped but the digest is still drained.
//
// Steps:
//  1. Stop the daemon process (SIGTERM via identity lock PID)
//  2. Clear the consent flag (state/.afk)
//  3. Drain the durable digest queue (state/.afk-digest)
//  4. Summarize escalations, wedge alarms, and blocked items
func Return(homeDir string) (*ReturnReport, error) {
	report := &ReturnReport{}

	// 1. Stop daemon via identity lock.
	daemonPID := readDaemonPID(homeDir)
	if daemonPID > 0 {
		if isProcessAlive(daemonPID) {
			fmt.Fprintf(os.Stderr, "afk: return: stopping daemon PID %d\n", daemonPID)
			if err := syscall.Kill(daemonPID, syscall.SIGTERM); err != nil {
				fmt.Fprintf(os.Stderr, "afk: return: sending SIGTERM to PID %d: %v\n", daemonPID, err)
			}
			// Brief grace period for the daemon to clean up flag and lock.
			time.Sleep(300 * time.Millisecond)
		}
	}
	// Always attempt lock cleanup regardless of whether a PID was parsed.
	lockPath := filepath.Join(homeDir, afkLockFile)
	os.Remove(lockPath)

	// 2. Clear consent flag idempotently.
	Disable(homeDir)

	// 3. Drain and summarize the digest queue.
	be, err := drainDigest(homeDir)
	if err != nil {
		return report, fmt.Errorf("draining digest: %w", err)
	}

	if be == nil {
		return report, nil
	}

	report.DigestedCount = len(be.Entries)

	// 4. Summarize escalations and wedge alarms.
	for _, entry := range be.Entries {
		if entry.Type != EscalationRoutine {
			s := fmt.Sprintf("[%s] %s: %s", entry.Type, entry.Key, entry.Payload)
			report.Escalations = append(report.Escalations, s)

			// Separate out blocked items for explicit surfacing.
			lower := strings.ToLower(entry.Payload)
			if strings.HasPrefix(lower, "blocked:") || strings.Contains(lower, "\nblocked:") {
				report.BlockedItems = append(report.BlockedItems, entry.Payload)
			}
		}
	}

	if be.WedgeAlarm != nil {
		report.WedgeAlarms = append(report.WedgeAlarms, be.WedgeAlarm.Reason)
	}

	return report, nil
}

// readDaemonPID reads the PID from state/.afk.lock.
func readDaemonPID(homeDir string) int {
	lockPath := filepath.Join(homeDir, afkLockFile)
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return 0
	}
	pid, _ := parseLockContent(data)
	return pid
}

// drainDigest reads and removes the durable digest file.
// Returns nil, nil when no digest exists.
func drainDigest(homeDir string) (*BatchedEscalation, error) {
	path := filepath.Join(homeDir, digestFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	// Remove regardless of parse outcome — drain is one-shot.
	os.Remove(path)

	var be BatchedEscalation
	if err := json.Unmarshal(data, &be); err != nil {
		return nil, fmt.Errorf("unmarshal digest: %w", err)
	}
	return &be, nil
}
