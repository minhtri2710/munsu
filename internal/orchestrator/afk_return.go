package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	// afkStopWait bounds how long Return observes a stopped daemon, and
	// afkStopPoll is how often it looks. Same shape as waitForWatcherExit
	// (supervision_watcher.go), different verdict on expiry: see waitForDaemonExit.
	//
	// The bound is larger than the watcher's 2s because the daemon may be inside
	// a digest flush when it is stopped, and the poll is coarser because nothing
	// here waits on the result -- Return is interactive, not a hot loop.
	//
	// Vars rather than consts only so a test can shorten the bound and poll
	// (TestReturn_RefusesWhenDaemonSurvivesStopRequest); the defaults above are
	// the production values, byte-identical to the constants they replaced.
	afkStopWait = 5 * time.Second
	afkStopPoll = 50 * time.Millisecond
)

// ReturnReport summarizes the AFK daemon shutdown and digest drain.
// Returned by Return when the general signals they are back.
type ReturnReport struct {
	Escalations   []string `json:"escalations"`
	WedgeAlarms   []string `json:"wedge_alarms"`
	BlockedItems  []string `json:"blocked_items"`
	DigestedCount int      `json:"digested_count"`

	// LossyStop records that the daemon was stopped by a mechanism that can
	// skip its final digest flush (stopProcessIsLossy, afk_process_windows.go):
	// the drained digest may be missing up to one window of entries that never
	// reached the file. It is set only when Return actually stopped a daemon;
	// a Return that found nothing to stop carries no loss.
	LossyStop bool `json:"lossy_stop"`
}

// HasActionable reports whether the return report contains any item
// needing munsu attention before resuming normal work.
// The caller must check this before resuming normal work and re-run
// Return until it returns clean (HasActionable == false).
//
// A lossy stop is actionable even when the digest drained empty: entries the
// stop dropped are gone, and the caller must see the report that says so
// rather than a clean bill. Re-running Return clears the flag -- there is
// nothing left to stop or drain -- but cannot recover the lost window.
func (r *ReturnReport) HasActionable() bool {
	return len(r.Escalations) > 0 || len(r.WedgeAlarms) > 0 || len(r.BlockedItems) > 0 || r.LossyStop
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
	if r.LossyStop {
		b.WriteString("\nLossy stop: the daemon was killed without its final digest flush, so up to one batch window of entries never reached the digest and cannot be drained. Treat the record above as a lower bound, not the full window.\n")
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
//  1. Stop the daemon process, but only after the PID from the identity lock is
//     confirmed to still be the daemon that published the "afk" writer identity
//     (daemonIdentityForPID). An unverifiable PID is refused, not terminated.
//  2. Clear the consent flag (state/.afk)
//  3. Drain the durable digest queue (state/.afk-digest)
//  4. Summarize escalations, wedge alarms, and blocked items
func Return(homeDir string) (*ReturnReport, error) {
	report := &ReturnReport{}

	// 1. Stop daemon via identity lock.
	//
	// Three exits below leave state/.lock and state/.afk in place, because
	// os.Remove(lockPath) and Disable() are further down. That is the intended
	// residual state in all three: the daemon may still be running, so a Return
	// that cleared consent and lock would hand the next AcquireLock a free lock
	// while a live daemon still writes under it. The caller re-runs Return; a
	// daemon that has since exited takes the isProcessAlive(false) path and the
	// same run cleans both up.
	daemonPID := readDaemonPID(homeDir)
	if daemonPID > 0 {
		if isProcessAlive(daemonPID) {
			// Exit A -- PID is alive but unverifiable. isProcessAlive answers
			// "some process holds this PID", not "our daemon does", and PIDs are
			// reused. Terminating on that answer alone kills whatever the OS
			// handed the number to, and on windows stopProcess is an uncatchable
			// TerminateProcess. So refuse, and say why. Residual: lock + flag
			// kept, nothing signalled.
			identity, err := daemonIdentityForPID(homeDir, daemonPID)
			if err != nil {
				return report, fmt.Errorf("AFK daemon PID %d ownership could not be verified; refusing to stop: %w", daemonPID, err)
			}
			fmt.Fprintf(os.Stderr, "afk: return: stopping daemon PID %d\n", daemonPID)
			// Exit B -- the stop request itself failed. Residual: lock + flag
			// kept, daemon assumed still running.
			if err := stopProcess(daemonPID); err != nil {
				return report, fmt.Errorf("stopping AFK daemon PID %d: %w", daemonPID, err)
			}
			// Exit C -- stop requested, daemon still alive at the bound. Residual:
			// lock + flag kept for a daemon whose death is unconfirmed.
			if !waitForDaemonExit(daemonPID) {
				return report, fmt.Errorf("AFK daemon PID %d remained alive %s after stop request", daemonPID, afkStopWait)
			}
			// The stop took effect. stopProcessIsLossy reports whether it could
			// have skipped the daemon's final digest flush: false on unix where
			// stopProcess is SIGTERM and the daemon flushes in its deferred
			// shutdown (afk_daemon.go step 7), true on windows where it is
			// TerminateProcess and the flush never runs. Carry it into the
			// report so ReturnReport refuses to claim "All clear" over a digest
			// missing up to one window of entries (#530).
			//
			// This is option (b) of #530, chosen over the owner-clean route
			// (c) -- making the digester durable on arrival instead of holding
			// `entries []BatchedEntry` in RAM for the window (afk_digester.go)
			// -- because (c) is a durability plus concurrency change across
			// Digester / drainDigest / BatchedEscalation, materially wider than
			// #530 authorizes, and also closes a hole this stop does not open:
			// a unix SIGKILL, panic, or power loss drops the same window today.
			// (c) is the preferred successor -- not (a), the windows soft stop,
			// which builds shutdown IPC on a platform where home.Init fails
			// every write (#524) and nothing here can verify it. Removal
			// condition: revisit once the windows lane is a required check AND
			// the product actually runs on windows (#524/#525/#526 landed and
			// observed green).
			if stopProcessIsLossy() {
				report.LossyStop = true
			}
			// stopProcess is a lossy stop on windows (afk_process_windows.go):
			// Kill skips the daemon's deferred clearDaemonIdentity, so the "afk"
			// writer identity artifact outlives the process it describes and
			// other readers keep treating it as a live writer. Compensate here,
			// where the death is confirmed and the identity is known to be the
			// one that just died. Idempotent: on unix the daemon's own defer has
			// normally already removed it, and RemoveWriterIdentityIfMatches
			// refuses to remove a successor's artifact.
			clearDaemonIdentity(homeDir, identity)
		}
	}
	// Only a confirmed stopped daemon may have its lock and consent cleared.
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

// waitForDaemonExit observes a stopped daemon until it is gone or the bound
// expires, and reports whether it is gone.
//
// It replaces a flat 300ms sleep followed by a single check. That constant had
// no measurement behind it on any platform, and on windows it is measured
// against the wrong thing entirely: stopProcess is TerminateProcess, which
// returns as soon as the kernel accepts the request, and isProcessAlive reads
// GetExitCodeProcess == STILL_ACTIVE, which still holds while the process is
// being torn down. A fixed sleep either loses to that teardown or pads every
// fast exit.
//
// Expiry is an error at the one call site, unlike waitForWatcherExit
// (supervision_watcher.go), which treats it as tolerable. The difference is what
// sits after the wait: there, nothing -- the restarting caller proves
// convergence itself. Here, os.Remove(lockPath) and Disable() sit after it, so
// treating expiry as success would clear the singleton lock and the consent flag
// for a daemon that may still be running and still writing under both.
func waitForDaemonExit(pid int) bool {
	deadline := time.Now().Add(afkStopWait)
	for {
		if !isProcessAlive(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(afkStopPoll)
	}
}

// readDaemonPID reads the PID from state/.lock.
//
// The second field of the lock stays discarded on purpose. It is time.Now() at
// the moment AcquireLock wrote the file, not the PID's start time, so it cannot
// tell a reused PID from the original -- see daemonIdentityForPID
// (afk_identity.go), which is where that question is answered.
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
