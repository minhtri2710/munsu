package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
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
	BlockedItems  []string `json:"blocked_items"`
	DigestedCount int      `json:"digested_count"`

	// RoutineReports lists the routine report entries Return settled, as
	// "key: payload". Escalated entries are settled too but are printed under
	// Escalations; this is where the General sees the rest, so that every report
	// Return settles is one it was shown.
	RoutineReports []string `json:"routine_reports"`

	// UnacceptedReports lists drained report entries whose settlement could not
	// finish its work (a scout's completion), each with the task to finish by
	// hand.
	UnacceptedReports []UnacceptedReport `json:"unaccepted_reports"`

	// DigestNotRemoved names the digest file Return could not remove after
	// settling its reports, with the error. It is not a failed return: the
	// reports are settled and the file still holds them, so a re-run replays
	// the idempotent settlement and removes it.
	DigestNotRemoved string `json:"digest_not_removed,omitempty"`
}

// HasActionable reports whether the return report contains any item
// needing munsu attention before resuming normal work.
// The caller must check this before resuming normal work and re-run
// Return until it returns clean (HasActionable == false).
func (r *ReturnReport) HasActionable() bool {
	return len(r.Escalations) > 0 || len(r.BlockedItems) > 0 || len(r.UnacceptedReports) > 0 || r.DigestNotRemoved != ""
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
	if len(r.BlockedItems) > 0 {
		b.WriteString(fmt.Sprintf("  blocked items (%d):\n", len(r.BlockedItems)))
		for _, bItem := range r.BlockedItems {
			b.WriteString(fmt.Sprintf("    - %s\n", bItem))
		}
	}
	if len(r.RoutineReports) > 0 {
		b.WriteString(fmt.Sprintf("  routine reports settled (%d):\n", len(r.RoutineReports)))
		for _, rr := range r.RoutineReports {
			b.WriteString(fmt.Sprintf("    - %s\n", rr))
		}
	}
	if len(r.UnacceptedReports) > 0 {
		b.WriteString(fmt.Sprintf("  reports not finished (%d):\n", len(r.UnacceptedReports)))
		for _, u := range r.UnacceptedReports {
			b.WriteString(fmt.Sprintf("    - %s\n      %s\n", u, u.Hint))
		}
	}
	if r.DigestNotRemoved != "" {
		b.WriteString(fmt.Sprintf("  digest not removed: %s; its reports are accepted, run `munsu afk return` again once the cause is fixed to remove it\n", r.DigestNotRemoved))
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
//  4. Settle the report each report entry carries: the digest handed to the
//     General is its acknowledgment, as a resolved wake is. This runs inside
//     the drain, before the digest file is removed, so a crash or a failed
//     removal leaves the entries on disk and a re-run replays the idempotent
//     settlement. A failure is listed in the report; it never aborts the
//     return.
//  5. Summarize escalations and blocked items
func Return(homeDir string) (*ReturnReport, error) {
	report := &ReturnReport{}

	// 1. Stop daemon via identity lock.
	//
	// The daemon's pid comes from the AFK lock (home.ReadAFKLockPID), which
	// reports 0 unless a daemon holds the lock's flock right now. Return never
	// clears that lock: it is the daemon's flock, released only when the daemon
	// exits, so Return cannot hand the next home.AcquireAFKLock a free lock
	// while a live daemon still writes under it.
	//
	// Three exits below leave state/.afk in place, because Disable() is further
	// down. That is the intended residual state in all three: the daemon may
	// still be running, and clearing consent under it would tell the next
	// `munsu afk` start that AFK is off while it is on. The caller re-runs
	// Return; a daemon that has since exited no longer holds the lock, and the
	// same run clears consent. A lock whose holder cannot be read is refused
	// with the same residual state.
	daemonPID, err := home.ReadAFKLockPID(homeDir)
	if err != nil {
		return report, fmt.Errorf("reading AFK daemon lock: %w", err)
	}
	if daemonPID > 0 {
		if home.IsProcessAlive(daemonPID) {
			// Exit A -- PID is alive but unverifiable. home.IsProcessAlive answers
			// "some process holds this PID", not "our daemon does", and PIDs are
			// reused. Terminating on that answer alone kills whatever the OS
			// handed the number to. So refuse, and say why. Residual: lock + flag
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
			// The stop took effect. Clear the "afk" writer identity here, where the
			// death is confirmed and the identity is known to be the one that just
			// died. Idempotent: the daemon's own defer has normally already removed
			// it, and RemoveWriterIdentityIfMatches refuses to remove a successor's
			// artifact.
			clearDaemonIdentity(homeDir, identity)
		}
	}
	// Only a confirmed stopped daemon may have its consent cleared.
	//
	// 2. Clear the consent flag. A Disable failure aborts here, before the
	// drain, so this Return never reports success over a flag that still says
	// AFK is on.
	if err := Disable(homeDir); err != nil {
		return report, err
	}

	// 3. Drain the digest queue, settling the reports it carries before the
	// file is removed.
	be, err := drainDigest(homeDir, func(be *BatchedEscalation) {
		for _, entry := range be.Entries {
			if entry.Kind != home.ReportWakeKind {
				continue
			}
			if u := settleReport(homeDir, entry.Key, entry.Payload, entry.Ref); u != nil {
				report.UnacceptedReports = append(report.UnacceptedReports, *u)
				continue
			}
			if entry.Type == EscalationRoutine {
				report.RoutineReports = append(report.RoutineReports, fmt.Sprintf("%s: %s", entry.Key, entry.Payload))
			}
		}
	})
	var notRemoved *digestRemovalError
	if errors.As(err, &notRemoved) {
		report.DigestNotRemoved = notRemoved.Error()
	} else if err != nil {
		return report, fmt.Errorf("draining digest: %w", err)
	}

	if be == nil {
		return report, nil
	}

	report.DigestedCount = len(be.Entries)

	// 4. Summarize escalations.
	for _, entry := range be.Entries {
		if entry.Type != EscalationRoutine {
			s := fmt.Sprintf("[%s] %s: %s", entry.Type, entry.Key, entry.Payload)
			report.Escalations = append(report.Escalations, s)

			// Separate out blocked items for explicit surfacing.
			if blockedEntry(entry) {
				report.BlockedItems = append(report.BlockedItems, entry.Payload)
			}
		}
	}

	return report, nil
}

// blockedEntry reports whether a digest entry is a blocked report. Its payload
// takes one of the two producer shapes payloadHasMaterialMarker reads: a
// watcher signal wake's status line ("blocked: ...") or a report wake's
// "<key>: blocked: ...".
func blockedEntry(entry BatchedEntry) bool {
	lower := strings.ToLower(entry.Payload)
	return strings.HasPrefix(lower, "blocked:") || strings.HasPrefix(lower, strings.ToLower(entry.Key)+": blocked:") ||
		strings.Contains(lower, "\nblocked:")
}

// waitForDaemonExit observes a stopped daemon until it is gone or the bound
// expires, and reports whether it is gone.
//
// It replaces a flat 300ms sleep followed by a single check. That constant had
// no measurement behind it, and a fixed sleep either loses to a slow teardown
// or pads every fast exit.
//
// Expiry is an error at the one call site, unlike waitForWatcherExit
// (supervision_watcher.go), which treats it as tolerable. The difference is what
// sits after the wait: there, nothing -- the restarting caller proves
// convergence itself. Here, Disable() sits after it, so treating expiry as
// success would clear the consent flag for a daemon that may still be running
// and still writing under it.
func waitForDaemonExit(pid int) bool {
	deadline := time.Now().Add(afkStopWait)
	for {
		if !home.IsProcessAlive(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(afkStopPoll)
	}
}

// removeDigestFile is a variable only so a test can fail the removal that
// follows acceptance (TestAFKReturnKeepsTheDigestUntilItsReportsAreAccepted).
var removeDigestFile = os.Remove

// drainDigest reads the durable digest file, hands it to accept, and only then
// removes it: the digest is the only copy of the refs its entries carry, so it
// must outlive their acceptance. If the removal fails the file stays and a
// re-run accepts again (the ack is idempotent); the digest is still returned,
// with a *digestRemovalError, because by then it is fully accepted and the
// caller reports the leftover file instead of failing. An unparseable file holds no ref
// this code can read and is removed, so one corrupt digest cannot wedge every
// later return. Returns nil, nil when no digest exists.
func drainDigest(homeDir string, accept func(*BatchedEscalation)) (*BatchedEscalation, error) {
	path := filepath.Join(homeDir, digestFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var be BatchedEscalation
	if err := json.Unmarshal(data, &be); err != nil {
		_ = removeDigestFile(path)
		return nil, fmt.Errorf("unmarshal digest: %w", err)
	}
	accept(&be)
	if err := removeDigestFile(path); err != nil && !os.IsNotExist(err) {
		return &be, &digestRemovalError{path: path, err: err}
	}
	return &be, nil
}

// digestRemovalError is a digest file that outlived its acceptance.
type digestRemovalError struct {
	path string
	err  error
}

func (e *digestRemovalError) Error() string { return fmt.Sprintf("%s: %v", e.path, e.err) }
func (e *digestRemovalError) Unwrap() error { return e.err }
